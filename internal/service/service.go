// Package service is the business-logic layer that sits between the
// HTTP / MCP handlers and the adapter contracts. It hosts the
// composite operations the user-facing surfaces invoke:
//
//   - Imprint  -- chunker → cells → primary insert → enqueue embed
//   - Update   -- CAS via watermark, re-chunk, selective re-embed
//   - Patch    -- atomic diff-op apply + the moat (text_md5 skip)
//   - Append   -- chunk-aware append
//   - Recall   -- mode dispatch (lookup/keyword/vector/hybrid) + graph_expansion
//   - Link / Unlink / Neighbors / Traverse — context graph wrappers
//   - Ledger append helpers
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axiom-studio/memora/internal/chunker"
	embedqueue "github.com/axiom-studio/memora/internal/embed/queue"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Service is the assembled set of collaborators handlers depend on.
type Service struct {
	Primary  adapter.PrimaryStore
	Vector   adapter.VectorStore
	Ledger   adapter.LedgerStore
	Embedder embedding.Provider
	Identity map[string]adapter.IdentityProvider
	Pool     *embedqueue.Pool
}

// IdentityFor returns the configured provider or falls back to "opaque".
func (s *Service) IdentityFor(name string) adapter.IdentityProvider {
	if name == "" {
		name = string(types.IdentityProviderOpaque)
	}
	if p, ok := s.Identity[name]; ok {
		return p
	}
	return s.Identity[string(types.IdentityProviderOpaque)]
}

// Imprint creates a Memory, chunks it, persists the cells, and runs
// embedding inline so the returned response reflects recall_ready
// for the synchronous test/dev path. For high-throughput production
// deployers the worker pool's Submit() is what the handler should
// call; v0.1 keeps things synchronous to make the moat visible.
func (s *Service) Imprint(ctx context.Context, workspaceID, agentID string, req api.ImprintRequest) (*api.ImprintResponse, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required for imprint", types.ErrInvalidInput)
	}
	ws, err := s.Primary.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	chunkerID := req.ChunkerID
	if chunkerID == "" {
		chunkerID = ws.ChunkerID
	}
	ck, err := chunker.Get(chunkerID)
	if err != nil {
		return nil, err
	}
	mem := &types.Memory{
		WorkspaceID:      workspaceID,
		CollectionID:     req.CollectionID,
		Content:          req.Content,
		Tags:             req.Tags,
		WrittenByAgentID: agentID,
	}
	start := time.Now()
	wmk, err := s.Primary.ImprintMemory(ctx, mem)
	if err != nil {
		return nil, err
	}
	cells := ck.Chunk(req.Content)
	for i := range cells {
		cells[i].MemoryID = mem.ID
		cells[i].WrittenByAgentID = agentID
		cells[i].CellID = types.NewID(types.CellIDPrefix)
	}
	if err := s.Primary.UpsertCells(ctx, mem.ID, cells); err != nil {
		return nil, err
	}
	// Inline-embed so the response reports a finalized recall_ready.
	embedRes, err := embedqueue.EmbedNow(ctx, embedqueue.EmbedderDeps{
		Primary: s.Primary, Vector: s.Vector, Provider: s.Embedder,
	}, workspaceID, req.CollectionID, mem.ID, nil, cells)
	if err == nil {
		_ = s.Primary.UpsertCells(ctx, mem.ID, cells)
	}
	recallReady := err == nil
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "imprint", Target: mem.ID, AgentID: agentID,
		WatermarkAfter: wmk,
		LatencyMS:      int(time.Since(start).Milliseconds()),
		Metadata:       map[string]any{"cells_created": len(cells), "cells_re_embedded": len(embedRes.Reembed)},
	})
	return &api.ImprintResponse{
		MemoryID:         mem.ID,
		Watermark:        wmk,
		ContentMD5:       mem.ContentMD5,
		CellsCreated:     len(cells),
		RecallReady:      recallReady,
		WrittenByAgentID: agentID,
		LedgerID:         ledgerID,
		LatencyMS:        int(time.Since(start).Milliseconds()),
	}, nil
}

// Update replaces a Memory's content with CAS.
func (s *Service) Update(ctx context.Context, workspaceID, memoryID, agentID, ifMatch string, req api.UpdateRequest) (*api.UpdateResponse, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required", types.ErrInvalidInput)
	}
	expected := ifMatch
	if expected == "" {
		expected = req.ExpectedWatermark
	}
	if expected == "" {
		return nil, fmt.Errorf("%w: If-Match or expected_watermark required for update", types.ErrInvalidInput)
	}
	existing, err := s.Primary.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	mem := *existing
	mem.Content = req.Content
	mem.Tags = req.Tags
	mem.LastModifiedByAgentID = agentID
	start := time.Now()
	newWmk, err := s.Primary.UpdateMemory(ctx, memoryID, expected, &mem)
	if err != nil {
		return nil, err
	}
	// Re-chunk + selective re-embed.
	ck, _ := chunker.Get(existing.WorkspaceID) // ignore; not workspace-aware here
	if ck == nil {
		ck, _ = chunker.Get("default")
	}
	oldCells, _ := s.Primary.GetCells(ctx, memoryID)
	newCells := ck.Chunk(req.Content)
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := embedqueue.EmbedNow(ctx, embedqueue.EmbedderDeps{
		Primary: s.Primary, Vector: s.Vector, Provider: s.Embedder,
	}, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Primary.UpsertCells(ctx, memoryID, newCells)
	// Drop cells beyond the new count.
	if len(oldCells) > len(newCells) {
		for _, c := range oldCells[len(newCells):] {
			_ = s.Vector.DeleteVectors(ctx, []adapter.VectorKey{{WorkspaceID: workspaceID, CellID: c.CellID}})
		}
	}
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "update", Target: memoryID, AgentID: agentID,
		WatermarkBefore: expected, WatermarkAfter: newWmk,
		LatencyMS: int(time.Since(start).Milliseconds()),
		Metadata: map[string]any{
			"cells_re_embedded": len(embedRes.Reembed),
			"cells_skipped":     len(embedRes.Skipped),
		},
	})
	return &api.UpdateResponse{
		MemoryID:              memoryID,
		Watermark:             newWmk,
		ContentMD5:            types.MD5Hex(req.Content),
		CellsReembed:          len(embedRes.Reembed),
		CellsSkipped:          len(embedRes.Skipped),
		LastModifiedByAgentID: agentID,
		LedgerID:              ledgerID,
		LatencyMS:             int(time.Since(start).Milliseconds()),
	}, nil
}

// Patch is the moat operation: atomic diff-op apply + selective re-embed.
func (s *Service) Patch(ctx context.Context, workspaceID, memoryID, agentID, ifMatch string, req api.PatchRequest) (*api.PatchResponse, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required", types.ErrInvalidInput)
	}
	expected := ifMatch
	if expected == "" {
		expected = req.ExpectedWatermark
	}
	if expected == "" {
		return nil, fmt.Errorf("%w: If-Match or expected_watermark required for patch", types.ErrInvalidInput)
	}
	start := time.Now()
	newWmk, _, newContent, err := s.Primary.PatchMemory(ctx, memoryID, expected, req.Patch, agentID)
	if err != nil {
		return nil, err
	}
	existing, _ := s.Primary.GetMemory(ctx, memoryID)
	ck, _ := chunker.Get("default")
	oldCells, _ := s.Primary.GetCells(ctx, memoryID)
	newCells := ck.Chunk(newContent)
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := embedqueue.EmbedNow(ctx, embedqueue.EmbedderDeps{
		Primary: s.Primary, Vector: s.Vector, Provider: s.Embedder,
	}, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Primary.UpsertCells(ctx, memoryID, newCells)
	if len(oldCells) > len(newCells) {
		for _, c := range oldCells[len(newCells):] {
			_ = s.Vector.DeleteVectors(ctx, []adapter.VectorKey{{WorkspaceID: workspaceID, CellID: c.CellID}})
		}
	}
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "patch", Target: memoryID, AgentID: agentID,
		WatermarkBefore: expected, WatermarkAfter: newWmk,
		LatencyMS: int(time.Since(start).Milliseconds()),
		Metadata: map[string]any{
			"patches_applied":   len(req.Patch),
			"cells_re_embedded": len(embedRes.Reembed),
			"cells_skipped":     len(embedRes.Skipped),
			"cells_added":       max(0, len(newCells)-len(oldCells)),
			"cells_removed":     max(0, len(oldCells)-len(newCells)),
		},
	})
	return &api.PatchResponse{
		MemoryID:              memoryID,
		Watermark:             newWmk,
		ContentMD5:            types.MD5Hex(newContent),
		PatchesApplied:        len(req.Patch),
		CellsReembed:          len(embedRes.Reembed),
		CellsSkipped:          len(embedRes.Skipped),
		CellsAdded:            max(0, len(newCells)-len(oldCells)),
		CellsRemoved:          max(0, len(oldCells)-len(newCells)),
		LastModifiedByAgentID: agentID,
		LedgerID:              ledgerID,
		LatencyMS:             int(time.Since(start).Milliseconds()),
	}, nil
}

// Append tacks body onto a Memory.
func (s *Service) Append(ctx context.Context, workspaceID, memoryID, agentID, ifMatch string, req api.AppendRequest) (*api.AppendResponse, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required", types.ErrInvalidInput)
	}
	start := time.Now()
	expected := ifMatch
	if expected == "" {
		expected = req.ExpectedWatermark
	}
	newWmk, md5, err := s.Primary.AppendMemory(ctx, memoryID, expected, req.Content, agentID)
	if err != nil {
		return nil, err
	}
	existing, _ := s.Primary.GetMemory(ctx, memoryID)
	ck, _ := chunker.Get("default")
	oldCells, _ := s.Primary.GetCells(ctx, memoryID)
	newCells := ck.Chunk(existing.Content)
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := embedqueue.EmbedNow(ctx, embedqueue.EmbedderDeps{
		Primary: s.Primary, Vector: s.Vector, Provider: s.Embedder,
	}, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Primary.UpsertCells(ctx, memoryID, newCells)
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "append", Target: memoryID, AgentID: agentID,
		WatermarkAfter: newWmk,
		LatencyMS:      int(time.Since(start).Milliseconds()),
		Metadata:       map[string]any{"cells_added": len(embedRes.Reembed), "cells_skipped": len(embedRes.Skipped)},
	})
	return &api.AppendResponse{
		MemoryID:              memoryID,
		Watermark:             newWmk,
		ContentMD5:            md5,
		CellsAdded:            len(embedRes.Reembed),
		LastModifiedByAgentID: agentID,
		LedgerID:              ledgerID,
		LatencyMS:             int(time.Since(start).Milliseconds()),
	}, nil
}

// Forget removes a Memory and cascades incident edges.
func (s *Service) Forget(ctx context.Context, workspaceID, memoryID, agentID string) (*api.ForgetResponse, error) {
	if err := s.Primary.ForgetMemory(ctx, memoryID); err != nil {
		return nil, err
	}
	n, _ := s.Primary.GraphCascadeForget(ctx, memoryID, agentID)
	wmk := types.NewWatermark()
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "forget", Target: memoryID, AgentID: agentID,
		WatermarkAfter: wmk, Metadata: map[string]any{"cascaded_edges": n},
	})
	return &api.ForgetResponse{
		MemoryID:      memoryID,
		Watermark:     wmk,
		CascadedEdges: n,
		LedgerID:      ledgerID,
	}, nil
}

// Recall dispatches to keyword / vector / hybrid / lookup.
func (s *Service) Recall(ctx context.Context, workspaceID string, req api.RecallRequest) (*api.RecallResponse, error) {
	start := time.Now()
	mode := req.Mode
	if mode == "" {
		mode = api.RecallModeHybrid
	}
	k := req.K
	if k <= 0 {
		k = 5
	}
	var seeds []api.RecallHit
	var totalScanned int
	switch mode {
	case api.RecallModeLookup:
		for _, id := range req.MemoryIDs {
			m, err := s.Primary.GetMemory(ctx, id)
			if err != nil {
				continue
			}
			seeds = append(seeds, api.RecallHit{
				MemoryID: m.ID, Text: m.Content, Watermark: m.HeadWatermark, Via: "seed",
				WrittenByAgentID: m.WrittenByAgentID,
			})
		}
	case api.RecallModeKeyword:
		hits, err := s.keywordSearch(ctx, workspaceID, req, k)
		if err != nil {
			return nil, err
		}
		seeds = hits
		totalScanned = len(hits)
	case api.RecallModeVector:
		hits, err := s.vectorSearch(ctx, workspaceID, req, k)
		if err != nil {
			return nil, err
		}
		seeds = hits
		totalScanned = len(hits)
	case api.RecallModeHybrid:
		kw, _ := s.keywordSearch(ctx, workspaceID, req, k)
		vec, _ := s.vectorSearch(ctx, workspaceID, req, k)
		seeds = hybridMerge(kw, vec, req.Weights, k)
		totalScanned = len(kw) + len(vec)
	default:
		return nil, fmt.Errorf("%w: unknown recall mode %q", types.ErrInvalidInput, mode)
	}

	results := seeds
	var graphExpanded int
	if req.GraphExpansion != nil && req.GraphExpansion.Depth > 0 && len(seeds) > 0 {
		expanded, n, err := s.graphExpand(ctx, workspaceID, seeds, *req.GraphExpansion)
		if err == nil {
			results = append(results, expanded...)
			graphExpanded = n
		}
	}
	// Sort by score desc and truncate to k + maxNeighbors.
	cap := k
	if req.GraphExpansion != nil {
		cap += req.GraphExpansion.MaxNeighbors
	}
	if cap <= 0 {
		cap = k
	}
	sortHits(results)
	if len(results) > cap {
		results = results[:cap]
	}
	return &api.RecallResponse{
		Results:                results,
		TotalCandidatesScanned: totalScanned,
		GraphNodesExpanded:     graphExpanded,
		LatencyMS:              int(time.Since(start).Milliseconds()),
	}, nil
}

// appendLedger writes a ledger entry, ignoring errors (audit log is
// best-effort but the row is always attempted).
func (s *Service) appendLedger(ctx context.Context, e api.LedgerEntry) string {
	return s.AppendLedger(ctx, e)
}

// AppendLedger is the public wrapper used by HTTP/MCP handlers that
// run their own primary-store calls (e.g. graph edge ops) and need to
// record an audit entry without going through a service.* method.
func (s *Service) AppendLedger(ctx context.Context, e api.LedgerEntry) string {
	if s.Ledger == nil {
		return ""
	}
	if e.LedgerID == "" {
		e.LedgerID = types.NewID(types.LedgerIDPrefix)
	}
	_ = s.Ledger.Append(ctx, e)
	return e.LedgerID
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// keywordSearch hits the FTS5 virtual table on the SQLite PrimaryStore.
// Adapter-specific; falls back to a substring scan for adapters without FTS.
func (s *Service) keywordSearch(ctx context.Context, workspaceID string, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	// SQLite-specific FTS query exposed via a typed Querier interface
	// would be cleaner. v0.1 lives without it by scanning ListMemories.
	mems, err := s.Primary.ListMemories(ctx, workspaceID, req.Filters.CollectionID, 500)
	if err != nil {
		return nil, err
	}
	var hits []api.RecallHit
	q := strings.ToLower(req.Query)
	for _, m := range mems {
		if !passesFilter(m, req.Filters) {
			continue
		}
		text := strings.ToLower(m.Content)
		if !strings.Contains(text, q) {
			continue
		}
		hits = append(hits, api.RecallHit{
			MemoryID:         m.ID,
			Text:             excerpt(m.Content, req.Query, 240),
			Tags:             m.Tags,
			Watermark:        m.HeadWatermark,
			WrittenByAgentID: m.WrittenByAgentID,
			KeywordScore:     keywordScore(text, q),
			Score:            keywordScore(text, q),
			Via:              "seed",
		})
	}
	sortHits(hits)
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func (s *Service) vectorSearch(ctx context.Context, workspaceID string, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	if s.Embedder == nil {
		return nil, nil
	}
	vecs, err := s.Embedder.Embed(ctx, []string{req.Query})
	if err != nil || len(vecs) == 0 {
		return nil, nil
	}
	res, err := s.Vector.Query(ctx, adapter.VectorQuery{
		WorkspaceID: workspaceID,
		Embedding:   vecs[0],
		K:           k,
		Filter: adapter.VectorFilter{
			CollectionID: req.Filters.CollectionID,
		},
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.RecallHit, 0, len(res))
	for _, v := range res {
		m, err := s.Primary.GetMemory(ctx, v.Key.MemoryID)
		if err != nil {
			continue
		}
		if !passesFilter(*m, req.Filters) {
			continue
		}
		out = append(out, api.RecallHit{
			MemoryID:         m.ID,
			CellID:           v.Key.CellID,
			Score:            v.Score,
			VectorScore:      v.Score,
			Text:             excerpt(m.Content, req.Query, 240),
			Tags:             m.Tags,
			Watermark:        m.HeadWatermark,
			WrittenByAgentID: m.WrittenByAgentID,
			Via:              "seed",
		})
	}
	return out, nil
}

func passesFilter(m types.Memory, f api.RecallFilters) bool {
	if f.CollectionID != "" && m.CollectionID != f.CollectionID {
		return false
	}
	if f.AgentID != "" && m.WrittenByAgentID != f.AgentID {
		return false
	}
	if len(f.AgentIDIn) > 0 {
		ok := false
		for _, a := range f.AgentIDIn {
			if m.WrittenByAgentID == a {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.AgentIDNotIn) > 0 {
		for _, a := range f.AgentIDNotIn {
			if m.WrittenByAgentID == a {
				return false
			}
		}
	}
	if f.TsAfter != nil && m.CreatedAt.Before(*f.TsAfter) {
		return false
	}
	if f.TsBefore != nil && m.CreatedAt.After(*f.TsBefore) {
		return false
	}
	for k, vs := range f.Tags {
		ok := false
		got := m.Tags[k]
		for _, v := range vs {
			if got == v {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func excerpt(content, query string, max int) string {
	if max <= 0 {
		max = 200
	}
	lc := strings.ToLower(content)
	lq := strings.ToLower(query)
	idx := strings.Index(lc, lq)
	if idx < 0 {
		if len(content) > max {
			return content[:max]
		}
		return content
	}
	start := idx - max/2
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > len(content) {
		end = len(content)
	}
	return content[start:end]
}

func keywordScore(content, q string) float64 {
	if content == "" || q == "" {
		return 0
	}
	c := float64(strings.Count(content, q))
	if c == 0 {
		return 0
	}
	return c / (1 + float64(len(content))/1000.0)
}

func hybridMerge(kw, vec []api.RecallHit, w api.RecallWeights, k int) []api.RecallHit {
	wv := w.Vector
	wk := w.Keyword
	if wv == 0 && wk == 0 {
		wv, wk = 0.7, 0.3
	}
	merged := map[string]api.RecallHit{}
	for _, h := range vec {
		h.Score = wv * h.Score
		merged[h.MemoryID] = h
	}
	for _, h := range kw {
		if existing, ok := merged[h.MemoryID]; ok {
			existing.Score += wk * h.Score
			existing.KeywordScore = h.KeywordScore
			merged[h.MemoryID] = existing
		} else {
			h.Score = wk * h.Score
			merged[h.MemoryID] = h
		}
	}
	out := make([]api.RecallHit, 0, len(merged))
	for _, h := range merged {
		out = append(out, h)
	}
	sortHits(out)
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func sortHits(h []api.RecallHit) {
	// Simple insertion sort — typical k is small.
	for i := 1; i < len(h); i++ {
		j := i
		for j > 0 && h[j].Score > h[j-1].Score {
			h[j], h[j-1] = h[j-1], h[j]
			j--
		}
	}
}

func (s *Service) graphExpand(ctx context.Context, workspaceID string, seeds []api.RecallHit, exp api.GraphExpansion) ([]api.RecallHit, int, error) {
	if exp.Depth <= 0 {
		return nil, 0, nil
	}
	dir := exp.Direction
	if dir == "" {
		dir = api.GraphDirOut
	}
	weight := exp.Weight
	if weight == 0 {
		weight = 0.2
	}
	const decay = 0.7
	var out []api.RecallHit
	visited := map[string]bool{}
	for _, seed := range seeds {
		visited[seed.MemoryID] = true
	}
	for _, seed := range seeds {
		tr, err := s.Primary.GraphTraverse(ctx, workspaceID, seed.MemoryID, adapter.TraverseOpts{
			Depth:     exp.Depth,
			Direction: dir,
			EdgeTypes: exp.EdgeTypes,
		})
		if err != nil {
			continue
		}
		for _, layer := range tr.Layers {
			for _, hit := range layer {
				if visited[hit.Memory.MemoryID] {
					continue
				}
				visited[hit.Memory.MemoryID] = true
				m, err := s.Primary.GetMemory(ctx, hit.Memory.MemoryID)
				if err != nil {
					continue
				}
				score := weight * pow(decay, hit.Layer) * seed.Score
				out = append(out, api.RecallHit{
					MemoryID:         m.ID,
					Text:             excerpt(m.Content, "", 240),
					Tags:             m.Tags,
					Watermark:        m.HeadWatermark,
					WrittenByAgentID: m.WrittenByAgentID,
					Score:            score,
					Via:              "graph",
					GraphProvenance: &api.GraphProvenance{
						FromMemoryID: seed.MemoryID,
						EdgeID:       hit.ViaEdgeID,
						EdgeType:     hit.ViaEdgeType,
						Layer:        hit.Layer,
					},
				})
			}
		}
	}
	return out, len(out), nil
}

func pow(base float64, exp int) float64 {
	out := 1.0
	for i := 0; i < exp; i++ {
		out *= base
	}
	return out
}

// IsClientError returns true if err should map to 4xx rather than 5xx.
func IsClientError(err error) bool {
	return errors.Is(err, types.ErrNotFound) ||
		errors.Is(err, types.ErrCAS) ||
		errors.Is(err, types.ErrAlreadyExists) ||
		errors.Is(err, types.ErrQuotaExceeded) ||
		errors.Is(err, types.ErrInvalidInput) ||
		errors.Is(err, types.ErrPatchAnchor) ||
		errors.Is(err, types.ErrDepthExceeded) ||
		errors.Is(err, types.ErrSyntheticEdge) ||
		errors.Is(err, types.ErrAlreadyLinked) ||
		errors.Is(err, types.ErrCapability)
}

// HTTPStatus maps a service error to an HTTP status code.
func HTTPStatus(err error) int {
	switch {
	case errors.Is(err, types.ErrNotFound):
		return 404
	case errors.Is(err, types.ErrCAS):
		return 412
	case errors.Is(err, types.ErrAlreadyExists), errors.Is(err, types.ErrAlreadyLinked), errors.Is(err, types.ErrSyntheticEdge):
		return 409
	case errors.Is(err, types.ErrQuotaExceeded):
		return 429
	case errors.Is(err, types.ErrInvalidInput), errors.Is(err, types.ErrPatchAnchor), errors.Is(err, types.ErrDepthExceeded):
		return 400
	case errors.Is(err, types.ErrCapability):
		return 501
	default:
		return 500
	}
}

// ErrorCode returns the canonical error_code string for the envelope.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, types.ErrNotFound):
		return "not_found"
	case errors.Is(err, types.ErrCAS):
		return "cas_conflict"
	case errors.Is(err, types.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, types.ErrAlreadyLinked):
		return "already_linked_via_synthetic"
	case errors.Is(err, types.ErrSyntheticEdge):
		return "cannot_unlink_synthetic_edge"
	case errors.Is(err, types.ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, types.ErrPatchAnchor):
		return "patch_anchor_not_found"
	case errors.Is(err, types.ErrDepthExceeded):
		return "graph_traverse_depth_exceeded"
	case errors.Is(err, types.ErrCapability):
		return "capability_unavailable"
	case errors.Is(err, types.ErrInvalidInput):
		return "invalid_input"
	default:
		return "internal_error"
	}
}
