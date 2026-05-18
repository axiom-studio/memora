// Package queue provides the in-process embed worker pool that turns
// Cells into vector entries. It is the place where the patch-moat
// economic property is enforced: cells whose text_md5 is unchanged
// since the last successful embed are not re-embedded.
package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
)

// Job describes one Cell that needs (or might need) embedding.
type Job struct {
	WorkspaceID  string
	CollectionID string
	MemoryID     string
	Cell         types.Cell
}

// EmbedderDeps is the set of collaborators the worker pool needs.
// Wired by the server at startup.
type EmbedderDeps struct {
	Primary  adapter.PrimaryStore
	Vector   adapter.VectorStore
	Provider embedding.Provider
}

// Result is the post-embed bookkeeping summary returned by the
// synchronous EmbedNow helper. Async submissions write to the stores
// directly and don't return a Result.
type Result struct {
	Reembed []string // cell_ids that produced a fresh vector
	Skipped []string // cell_ids whose text_md5 was unchanged
}

// Pool is an in-process worker pool. Submit() enqueues a job; the pool
// drains in FIFO order across N workers (default = NumCPU).
type Pool struct {
	deps     EmbedderDeps
	jobs     chan Job
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  chan struct{}
}

// NewPool returns a started Pool.
func NewPool(deps EmbedderDeps, workers int) *Pool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	p := &Pool{
		deps:    deps,
		jobs:    make(chan Job, 1024),
		stopped: make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Submit enqueues a job. Non-blocking unless the buffer is full.
func (p *Pool) Submit(j Job) {
	p.jobs <- j
}

// Close stops accepting new jobs and waits for in-flight work to drain.
func (p *Pool) Close() {
	p.stopOnce.Do(func() {
		close(p.jobs)
	})
	p.wg.Wait()
	close(p.stopped)
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for j := range p.jobs {
		_ = p.run(context.Background(), j)
	}
}

func (p *Pool) run(ctx context.Context, j Job) error {
	if p.deps.Provider == nil {
		return errors.New("embed queue: no provider configured")
	}
	// Skip re-embed if the cell's text_md5 is unchanged and a vector_key
	// already exists. This is the moat at the row level.
	if j.Cell.VectorKey != "" && j.Cell.TextMD5 != "" {
		existing, err := p.deps.Primary.GetCells(ctx, j.MemoryID)
		if err == nil {
			for _, c := range existing {
				if c.CellID == j.Cell.CellID && c.TextMD5 == j.Cell.TextMD5 && c.VectorKey != "" {
					return nil // nothing to do
				}
			}
		}
	}
	vecs, err := p.deps.Provider.Embed(ctx, []string{j.Cell.Text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vecs) == 0 {
		return errors.New("embed: provider returned no vectors")
	}
	key := adapter.VectorKey{
		WorkspaceID:  j.WorkspaceID,
		CollectionID: j.CollectionID,
		MemoryID:     j.MemoryID,
		CellID:       j.Cell.CellID,
	}
	if err := p.deps.Vector.PutVector(ctx, adapter.VectorPut{Key: key, Embedding: vecs[0]}); err != nil {
		return fmt.Errorf("put vector: %w", err)
	}
	if err := p.deps.Primary.UpdateCellVectorKey(ctx, j.Cell.CellID, j.Cell.CellID, p.deps.Provider.ModelID()); err != nil {
		return fmt.Errorf("update cell vector_key: %w", err)
	}
	return nil
}

// EmbedNow runs the embed inline (no worker hand-off) and reports
// per-cell re-embed / skip counts — used by the Patch handler so the
// HTTP response can surface the moat metric synchronously.
func EmbedNow(ctx context.Context, deps EmbedderDeps, ws, coll, memoryID string, oldCells, newCells []types.Cell) (Result, error) {
	var res Result
	oldByCellID := map[string]types.Cell{}
	for _, c := range oldCells {
		oldByCellID[c.CellID] = c
	}
	// Also index by seq for diff-by-position when the cell ids differ.
	oldBySeq := map[int]types.Cell{}
	for _, c := range oldCells {
		oldBySeq[c.Seq] = c
	}

	for i := range newCells {
		nc := &newCells[i]
		var match types.Cell
		var matched bool
		if nc.CellID != "" {
			match, matched = oldByCellID[nc.CellID]
		}
		if !matched {
			match, matched = oldBySeq[nc.Seq]
		}
		if matched && match.TextMD5 == nc.TextMD5 && match.VectorKey != "" {
			// Preserve the existing cell_id + vector_key so the embedded
			// vector keeps pointing at the same row.
			nc.CellID = match.CellID
			nc.VectorKey = match.VectorKey
			nc.EmbeddingModel = match.EmbeddingModel
			res.Skipped = append(res.Skipped, nc.CellID)
			continue
		}
		if nc.CellID == "" {
			nc.CellID = types.NewID(types.CellIDPrefix)
		}
		vecs, err := deps.Provider.Embed(ctx, []string{nc.Text})
		if err != nil {
			return res, err
		}
		key := adapter.VectorKey{WorkspaceID: ws, CollectionID: coll, MemoryID: memoryID, CellID: nc.CellID}
		if err := deps.Vector.PutVector(ctx, adapter.VectorPut{Key: key, Embedding: vecs[0]}); err != nil {
			return res, err
		}
		nc.VectorKey = nc.CellID
		nc.EmbeddingModel = deps.Provider.ModelID()
		res.Reembed = append(res.Reembed, nc.CellID)
	}
	return res, nil
}
