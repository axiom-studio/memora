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
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// OpEmbedFailed is the ledger op recorded when an async embed job
// exhausts its retry budget. Operators page on a surge of these rows.
const OpEmbedFailed = "embed_failed"

// Job describes one Cell that needs (or might need) embedding.
type Job struct {
	WorkspaceID  string
	CollectionID string
	MemoryID     string
	Cell         types.Cell
}

// EmbedderDeps is the set of collaborators the worker pool needs.
// Wired by the server at startup. Ledger is optional — when nil, the
// pool will not record `embed_failed` rows. Production builds wire it;
// some tests omit it for terseness.
type EmbedderDeps struct {
	Metadata adapter.MetadataStore
	Vector   adapter.VectorStore
	Provider embedding.Provider
	Ledger   adapter.LedgerStore
}

// PoolConfig configures the embed worker pool. Zero-valued fields fall
// back to defaults documented per field.
type PoolConfig struct {
	// Workers is the number of goroutines draining the job channel.
	// Default = runtime.NumCPU(). Each worker holds its own batch
	// buffer; cross-worker coalescing is intentionally absent (would
	// require a single drainer, defeating parallelism).
	Workers int

	// BatchSize is the max number of cells passed to Provider.Embed in
	// one call. Default = 16. TODO: switch to
	// Provider.Capabilities().MaxBatchSize once #2115 lands.
	BatchSize int

	// BatchFlushInterval is the max wall-clock a partial batch waits
	// for additional jobs before flushing. Default = 100ms. Lower
	// values reduce tail latency; higher values improve batching.
	BatchFlushInterval time.Duration

	// RetryAttempts is the total Provider.Embed attempts per batch
	// (including the first). Default = 3.
	RetryAttempts int

	// RetryBaseDelay is the first inter-attempt sleep. Subsequent
	// attempts use exponential backoff (base * 2^(attempt-1)). Default
	// = 2 * time.Second.
	RetryBaseDelay time.Duration
}

func (c PoolConfig) withDefaults() PoolConfig {
	if c.Workers <= 0 {
		c.Workers = runtime.NumCPU()
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 16
	}
	if c.BatchFlushInterval <= 0 {
		c.BatchFlushInterval = 100 * time.Millisecond
	}
	if c.RetryAttempts <= 0 {
		c.RetryAttempts = 3
	}
	if c.RetryBaseDelay <= 0 {
		c.RetryBaseDelay = 2 * time.Second
	}
	return c
}

// Result is the post-embed bookkeeping summary returned by the
// synchronous EmbedNow helper. Async submissions write to the stores
// directly and don't return a Result.
type Result struct {
	Reembed    []string    // cell_ids that produced a fresh vector
	Skipped    []string    // cell_ids whose text_md5 was unchanged
	Embeddings [][]float32 // parallel to newCells; nil entry means cell was skipped
}

// Pool is an in-process worker pool. Submit() enqueues a job; the pool
// drains in FIFO order across N workers (default = NumCPU). Each
// worker batches up to PoolConfig.BatchSize cells per Provider.Embed
// call to amortize per-request overhead.
type Pool struct {
	deps     EmbedderDeps
	cfg      PoolConfig
	jobs     chan Job
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	inFlight sync.Map // cell_id → struct{} dedup gate
}

// NewPool returns a started Pool. cfg may be the zero value; defaults
// are applied per PoolConfig field documentation.
func NewPool(deps EmbedderDeps, cfg PoolConfig) *Pool {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		deps:    deps,
		cfg:     cfg,
		jobs:    make(chan Job, 1024),
		stopped: make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	for i := 0; i < cfg.Workers; i++ {
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
// Concurrent in-flight retry-backoff sleeps are interrupted via the
// internal context.
func (p *Pool) Close() {
	p.stopOnce.Do(func() {
		close(p.jobs)
	})
	p.wg.Wait()
	p.cancel()
	close(p.stopped)
}

func (p *Pool) worker() {
	defer p.wg.Done()
	batch := make([]Job, 0, p.cfg.BatchSize)
	timer := time.NewTimer(p.cfg.BatchFlushInterval)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(p.cfg.BatchFlushInterval)
	}
	drain := func() {
		if len(batch) > 0 {
			p.runBatch(p.ctx, batch)
			batch = batch[:0]
		}
		resetTimer()
	}
	for {
		select {
		case j, ok := <-p.jobs:
			if !ok {
				if len(batch) > 0 {
					p.runBatch(p.ctx, batch)
				}
				return
			}
			batch = append(batch, j)
			if len(batch) >= p.cfg.BatchSize {
				drain()
			}
		case <-timer.C:
			drain()
		}
	}
}

// runBatch processes one batch end-to-end: skip-check (per-memory
// dedup'd GetCells), in-flight cell dedup, batched Provider.Embed with
// retry, per-cell persistence, and conditional recall_ready flip.
func (p *Pool) runBatch(ctx context.Context, jobs []Job) {
	if p.deps.Provider == nil {
		return
	}
	// Every memory touched by this batch is a flip candidate; the SQL
	// guard inside FlipRecallReadyIfAllEmbedded is the source of truth
	// for whether the row actually mutates.
	candidates := make(map[string]struct{}, len(jobs))
	for _, j := range jobs {
		candidates[j.MemoryID] = struct{}{}
	}

	// Per-memory cell cache for the moat skip-check.
	cellsByMemory := map[string][]types.Cell{}
	loadCells := func(memID string) []types.Cell {
		if cs, ok := cellsByMemory[memID]; ok {
			return cs
		}
		cs, err := p.deps.Metadata.GetCells(ctx, memID)
		if err != nil {
			return nil
		}
		cellsByMemory[memID] = cs
		return cs
	}

	type pending struct {
		job Job
	}
	keep := make([]pending, 0, len(jobs))
	for _, j := range jobs {
		// In-flight dedup: if another worker (or an earlier item in
		// this same batch) is already embedding this cell_id, drop.
		if _, dup := p.inFlight.LoadOrStore(j.Cell.CellID, struct{}{}); dup {
			continue
		}
		// Moat skip-check: same text_md5 + existing vector_key → no
		// re-embed. Release the in-flight gate immediately.
		if j.Cell.VectorKey != "" && j.Cell.TextMD5 != "" {
			skipped := false
			for _, c := range loadCells(j.MemoryID) {
				if c.CellID == j.Cell.CellID && c.TextMD5 == j.Cell.TextMD5 && c.VectorKey != "" {
					skipped = true
					break
				}
			}
			if skipped {
				p.inFlight.Delete(j.Cell.CellID)
				continue
			}
		}
		keep = append(keep, pending{job: j})
	}

	if len(keep) == 0 {
		// Skips and dedups still might have completed a memory — try
		// the flip and exit.
		p.flipCandidates(ctx, candidates)
		return
	}

	texts := make([]string, len(keep))
	for i := range keep {
		texts[i] = keep[i].job.Cell.Text
	}

	vecs, embedErr := p.embedWithRetry(ctx, texts)
	if embedErr != nil {
		for _, k := range keep {
			p.logEmbedFailed(k.job, embedErr)
			p.inFlight.Delete(k.job.Cell.CellID)
		}
		// Memory rows that had all-but-the-failed-cell embedded won't
		// flip (the failed cell's vector_key stays empty); the call is
		// still safe.
		p.flipCandidates(ctx, candidates)
		return
	}

	for i, k := range keep {
		j := k.job
		key := adapter.VectorKey{
			WorkspaceID:  j.WorkspaceID,
			CollectionID: j.CollectionID,
			MemoryID:     j.MemoryID,
			CellID:       j.Cell.CellID,
		}
		if err := p.deps.Vector.PutVector(ctx, adapter.VectorPut{Key: key, Embedding: vecs[i]}); err != nil {
			p.logEmbedFailed(j, fmt.Errorf("put vector cell %s memory %s: %w", j.Cell.CellID, j.MemoryID, err))
			p.inFlight.Delete(j.Cell.CellID)
			continue
		}
		if err := p.deps.Metadata.UpdateCellVectorKey(ctx, j.Cell.CellID, j.Cell.CellID, p.deps.Provider.ModelID()); err != nil {
			p.logEmbedFailed(j, fmt.Errorf("update cell_vector_key cell %s memory %s: %w", j.Cell.CellID, j.MemoryID, err))
			p.inFlight.Delete(j.Cell.CellID)
			continue
		}
		p.inFlight.Delete(j.Cell.CellID)
	}
	p.flipCandidates(ctx, candidates)
}

// embedWithRetry calls Provider.Embed with exponential backoff, honoring
// ctx cancellation between attempts. Returns (vectors, nil) on success
// or (nil, lastErr) after the retry budget is exhausted.
func (p *Pool) embedWithRetry(ctx context.Context, texts []string) ([][]float32, error) {
	var lastErr error
	for attempt := 1; attempt <= p.cfg.RetryAttempts; attempt++ {
		v, err := p.deps.Provider.Embed(ctx, texts)
		if err == nil {
			if len(v) != len(texts) {
				lastErr = fmt.Errorf("embed: provider returned %d vectors, expected %d", len(v), len(texts))
			} else {
				return v, nil
			}
		} else {
			lastErr = fmt.Errorf("embed attempt %d/%d: %w", attempt, p.cfg.RetryAttempts, err)
		}
		if attempt >= p.cfg.RetryAttempts {
			break
		}
		delay := p.cfg.RetryBaseDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (p *Pool) flipCandidates(ctx context.Context, ids map[string]struct{}) {
	for memID := range ids {
		_, _ = p.deps.Metadata.FlipRecallReadyIfAllEmbedded(ctx, memID)
	}
}

func (p *Pool) logEmbedFailed(j Job, cause error) {
	if p.deps.Ledger == nil {
		return
	}
	_ = p.deps.Ledger.Append(context.Background(), api.LedgerEntry{
		LedgerID:    types.NewID(types.LedgerIDPrefix),
		WorkspaceID: j.WorkspaceID,
		Op:          OpEmbedFailed,
		Target:      j.MemoryID,
		AgentID:     j.Cell.WrittenByAgentID,
		Timestamp:   time.Now().UTC(),
		Metadata: map[string]any{
			"cell_id": j.Cell.CellID,
			"error":   cause.Error(),
		},
	})
}

// EmbedNow runs the embed inline (no worker hand-off) and reports
// per-cell re-embed / skip counts — used by the Patch handler so the
// HTTP response can surface the moat metric synchronously.
func EmbedNow(ctx context.Context, deps EmbedderDeps, ws, coll, memoryID string, oldCells, newCells []types.Cell) (Result, error) {
	if deps.Provider == nil {
		return Result{}, errors.New("embed queue: no provider configured")
	}
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

	res.Embeddings = make([][]float32, len(newCells))
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
		res.Embeddings[i] = vecs[0]
	}
	return res, nil
}
