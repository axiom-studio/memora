package service

import (
	"context"
	"fmt"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
)

// ReembedOptions scope a re-embed run.
type ReembedOptions struct {
	WorkspaceID string // "" = every workspace
	DryRun      bool   // count only; write nothing
	Batch       int    // memories per batch (default 100)
}

// ReembedResult counts a re-embed run. Stale cells are those without a
// vector from the active model and dimension; Reembedded is how many of
// them this run wrote (0 on a dry run).
type ReembedResult struct {
	Model      string `json:"model"`
	Dim        int    `json:"dim"`
	Workspaces int    `json:"workspaces"`
	Memories   int    `json:"memories"`
	Cells      int    `json:"cells"`
	Stale      int    `json:"stale"`
	Reembedded int    `json:"reembedded"`
	// Orphans counts memories that are deleted or gone but still had
	// vectors; a run (not a dry run) removes those vectors.
	Orphans int  `json:"orphan_memories"`
	DryRun  bool `json:"dry_run"`
}

// Reembed gives every cell in scope a vector from the active embedding
// model (S-7). It walks memories in batches and embeds only the cells
// whose vector is missing or from another model or dimension, so a run
// that is interrupted resumes where it stopped, and a finished run is a
// no-op. Each batch is written on its own. progress, when set, is called
// after every batch.
func (s *Service) Reembed(ctx context.Context, o ReembedOptions, progress func(ReembedResult)) (ReembedResult, error) {
	res := ReembedResult{DryRun: o.DryRun}
	if s.Embedder == nil || !embedding.Searchable(s.Embedder) {
		return res, fmt.Errorf("%w: reembed needs a real embedding model (the active one produces no searchable vectors)", types.ErrCapability)
	}
	mav, ok := s.Vector.(adapter.ModelAwareVectorStore)
	if !ok {
		return res, fmt.Errorf("%w: the vector store does not record embedding models per row", types.ErrCapability)
	}
	res.Model, res.Dim = s.Embedder.ModelID(), s.Embedder.Dim()
	if o.Batch <= 0 {
		o.Batch = 100
	}

	var workspaces []string
	if o.WorkspaceID != "" {
		if _, err := types.ParseID("workspace_id", o.WorkspaceID); err != nil {
			return res, err
		}
		if _, err := s.Metadata.GetWorkspace(ctx, o.WorkspaceID); err != nil {
			return res, err
		}
		workspaces = []string{o.WorkspaceID}
	} else {
		for cursor := ""; ; {
			page, next, err := s.Metadata.ListWorkspacesPaged(ctx, cursor, 500)
			if err != nil {
				return res, err
			}
			for _, w := range page {
				workspaces = append(workspaces, w.ID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}

	for _, ws := range workspaces {
		res.Workspaces++
		if err := s.removeOrphanVectors(ctx, mav, ws, o, &res); err != nil {
			return res, err
		}
		for cursor := ""; ; {
			mems, next, err := s.Metadata.ListMemoriesPaged(ctx, ws, "", cursor, o.Batch)
			if err != nil {
				return res, err
			}
			if err := s.reembedBatch(ctx, mav, mems, o.DryRun, &res); err != nil {
				return res, err
			}
			if progress != nil {
				progress(res)
			}
			if next == "" || len(mems) == 0 {
				break
			}
			cursor = next
		}
	}
	return res, nil
}

func (s *Service) reembedBatch(ctx context.Context, mav adapter.ModelAwareVectorStore, mems []types.Memory, dryRun bool, res *ReembedResult) error {
	if len(mems) == 0 {
		return nil
	}
	res.Memories += len(mems)
	ids := make([]string, len(mems))
	byID := make(map[string]types.Memory, len(mems))
	for i, m := range mems {
		ids[i] = m.ID
		byID[m.ID] = m
	}
	cellsByMem, err := s.Metadata.GetCellsForMemories(ctx, ids)
	if err != nil {
		return err
	}
	var cells []types.Cell
	for _, id := range ids {
		cells = append(cells, cellsByMem[id]...)
	}
	res.Cells += len(cells)
	cellIDs := make([]string, len(cells))
	for i, c := range cells {
		cellIDs[i] = c.CellID
	}
	fresh, err := mav.FreshCells(ctx, cellIDs)
	if err != nil {
		return err
	}
	var stale []types.Cell
	for _, c := range cells {
		if !fresh[c.CellID] {
			stale = append(stale, c)
		}
	}
	res.Stale += len(stale)
	if dryRun || len(stale) == 0 {
		return nil
	}

	texts := make([]string, len(stale))
	for i, c := range stale {
		texts[i] = c.Text
	}
	vecs, err := s.Embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed %d cells: %s", len(texts), embedding.Redact(err.Error()))
	}
	if len(vecs) != len(stale) {
		return fmt.Errorf("embedder returned %d vectors for %d cells", len(vecs), len(stale))
	}
	puts := make([]adapter.VectorPut, len(stale))
	touched := map[string]bool{}
	for i, c := range stale {
		m := byID[c.MemoryID]
		puts[i] = adapter.VectorPut{
			Key:       adapter.VectorKey{WorkspaceID: m.WorkspaceID, CollectionID: m.CollectionID, MemoryID: c.MemoryID, CellID: c.CellID},
			Embedding: vecs[i],
		}
		touched[c.MemoryID] = true
	}
	if err := s.Vector.PutVectorsBatch(ctx, puts); err != nil {
		return err
	}
	for _, c := range stale {
		if err := s.Metadata.UpdateCellVectorKey(ctx, c.CellID, c.CellID, s.Embedder.ModelID()); err != nil {
			return err
		}
	}
	for id := range touched {
		s.markRecallReady(ctx, id)
	}
	res.Reembedded += len(stale)
	return nil
}

// removeOrphanVectors deletes the vectors of memories in ws that are
// forgotten or missing from the metadata store: before #6179, Forget
// left them behind, and they count as stale vectors that no re-embed
// could ever refresh.
func (s *Service) removeOrphanVectors(ctx context.Context, mav adapter.ModelAwareVectorStore, ws string, o ReembedOptions, res *ReembedResult) error {
	for after := ""; ; {
		ids, err := mav.VectorMemories(ctx, ws, after, o.Batch)
		if err != nil || len(ids) == 0 {
			return err
		}
		live, err := s.Metadata.GetMemories(ctx, ids)
		if err != nil {
			return err
		}
		alive := make(map[string]bool, len(live))
		for _, m := range live {
			if m.WorkspaceID == ws {
				alive[m.ID] = true
			}
		}
		var gone []adapter.VectorKey
		for _, id := range ids {
			if !alive[id] {
				gone = append(gone, adapter.VectorKey{WorkspaceID: ws, MemoryID: id})
			}
		}
		res.Orphans += len(gone)
		if len(gone) > 0 && !o.DryRun {
			if err := s.Vector.DeleteVectors(ctx, gone); err != nil {
				return err
			}
		}
		after = ids[len(ids)-1]
	}
}
