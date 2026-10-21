// Package service is the business-logic layer that sits between the
// HTTP / MCP handlers and the adapter contracts. It hosts the
// composite operations the user-facing surfaces invoke:
//
//   - Imprint  -- chunker → cells → metadata insert → enqueue embed
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
	"sync"
	"time"

	"log/slog"

	"github.com/axiom-studio/memora/internal/autolink"
	"github.com/axiom-studio/memora/internal/chunker"
	embedqueue "github.com/axiom-studio/memora/internal/embed/queue"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Service is the assembled set of collaborators handlers depend on.
type Service struct {
	Metadata adapter.MetadataStore
	Vector   adapter.VectorStore
	Ledger   adapter.LedgerStore
	Content  adapter.ContentStore // nil = legacy-only (no dual-write)
	Graph    adapter.GraphStore   // nil = graph ops unavailable
	Embedder embedding.Provider
	Identity map[string]adapter.IdentityProvider
	// NodeID and NodeName identify this node to federation peers and in
	// results (see cmd/memora-core resolveNode).
	NodeID   string
	NodeName string
	// Pool, when set, takes cell embedding off the write path: writes
	// queue vectorless cells and the pool flips recall_ready once they
	// are all embedded (`embedding.async`). nil = embed inline.
	Pool *embedqueue.Pool

	// noAutoLink holds memory IDs imprinted with auto_link=false while
	// Pool is set, so the pool's post-embed hook skips them.
	noAutoLink sync.Map
}

// PostEmbedAutoLinkHook returns a closure suitable for embedqueue.PoolConfig.PostEmbedHook.
// It calls autolink.AutoLink for the given memory after the async embed pool flips recall_ready.
func (s *Service) PostEmbedAutoLinkHook() func(ctx context.Context, workspaceID, memoryID string, cells []types.Cell, embeddings [][]float32) {
	if s.Graph == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID, memoryID string, cells []types.Cell, embeddings [][]float32) {
		if _, skip := s.noAutoLink.LoadAndDelete(memoryID); skip {
			return
		}
		mem := &types.Memory{ID: memoryID, WorkspaceID: workspaceID}
		// Best-effort: AutoLink logs and ledgers its own failures.
		_, _ = autolink.AutoLink(ctx, autolink.Deps{
			Metadata: s.Metadata, Vector: s.Vector,
			Graph: s.Graph, Ledger: s.Ledger,
			Logger: slog.Default(),
		}, workspaceID, mem, cells, embeddings, s.Embedder.ModelID())
	}
}

// GraphLink delegates to the configured GraphStore.
func (s *Service) GraphLink(ctx context.Context, e types.Edge) (types.Edge, error) {
	if s.Graph == nil {
		return types.Edge{}, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.Link(ctx, e)
}

// GraphUnlink delegates to the configured GraphStore.
func (s *Service) GraphUnlink(ctx context.Context, workspaceID, edgeID, agentID string) error {
	if s.Graph == nil {
		return fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.Unlink(ctx, workspaceID, edgeID, agentID)
}

// GraphLinkBatch delegates to the configured GraphStore.
func (s *Service) GraphLinkBatch(ctx context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.LinkBatch(ctx, edges)
}

// GraphCascadeForget delegates to the configured GraphStore.
func (s *Service) GraphCascadeForget(ctx context.Context, workspaceID, memoryID, agentID string) (int, error) {
	if s.Graph == nil {
		return 0, nil
	}
	return s.Graph.CascadeForget(ctx, workspaceID, memoryID, agentID)
}

// GraphNeighbors delegates to the configured GraphStore.
func (s *Service) GraphNeighbors(ctx context.Context, workspaceID, memoryID string, opts adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
	if s.Graph == nil {
		return nil, nil, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.Neighbors(ctx, workspaceID, memoryID, opts)
}

// GraphTraverse delegates to the configured GraphStore.
func (s *Service) GraphTraverse(ctx context.Context, workspaceID, seedMemoryID string, opts adapter.TraverseOpts) (adapter.TraverseResult, error) {
	if s.Graph == nil {
		return adapter.TraverseResult{}, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.Traverse(ctx, workspaceID, seedMemoryID, opts)
}

// GraphStats delegates to the configured GraphStore.
func (s *Service) GraphStats(ctx context.Context, workspaceID string) (int, map[string]int, error) {
	if s.Graph == nil {
		return 0, nil, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	return s.Graph.Stats(ctx, workspaceID)
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

// ResolveAgent verifies an agent's identity and returns its agent_id.
// Providers that identify the agent from its proof (adapter.IdentityKeyer)
// get the UUIDv4 bound to that identity, minted the first time it is
// seen; a claimedID, if given, must be that ID. Every other provider
// verifies claimedID itself, which must be a UUID.
func (s *Service) ResolveAgent(ctx context.Context, provider, claimedID string, proof map[string]any) (string, error) {
	prov := s.IdentityFor(provider)
	keyer, ok := prov.(adapter.IdentityKeyer)
	if !ok {
		if _, err := types.ParseID("agent_id", claimedID); err != nil {
			return "", err
		}
		if err := prov.Verify(ctx, adapter.IdentityVerifyInput{AgentID: claimedID, IdentityProof: proof}); err != nil {
			return "", err
		}
		return claimedID, nil
	}
	key, err := keyer.IdentityKey(ctx, proof)
	if err != nil {
		return "", err
	}
	agentID, err := s.Metadata.ResolveIdentityKey(ctx, key, types.NewID())
	if err != nil {
		return "", fmt.Errorf("resolve %s identity: %w", prov.Name(), err)
	}
	if claimedID != "" && claimedID != agentID {
		return "", fmt.Errorf("%s: agent_id %q is not the agent this identity_proof identifies", prov.Name(), claimedID)
	}
	return agentID, nil
}

func (s *Service) workspaceChunker(ctx context.Context, workspaceID string) (chunker.Chunker, error) {
	ws, err := s.Metadata.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return chunker.Get(ws.ChunkerID)
}

// Imprint creates a Memory, chunks it, persists the cells, and embeds
// them. By default embedding runs inline, so the response reports a
// final recall_ready. With a Pool (`embedding.async`) the cells are
// queued instead: the response says recall_ready=false and the pool
// flips it once every cell has a vector.
func (s *Service) Imprint(ctx context.Context, workspaceID, agentID string, req api.ImprintRequest) (*api.ImprintResponse, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required for imprint", types.ErrInvalidInput)
	}
	ws, err := s.Metadata.GetWorkspace(ctx, workspaceID)
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
	if cfg := req.ChunkerConfig; len(cfg) > 0 {
		if c, ok := ck.(chunker.Configurable); ok {
			if err := c.Configure(cfg); err != nil {
				return nil, fmt.Errorf("%w: chunker config: %s", types.ErrInvalidInput, err)
			}
		}
	}
	mem := &types.Memory{
		WorkspaceID:      workspaceID,
		CollectionID:     req.CollectionID,
		Content:          req.Content,
		Tags:             req.Tags,
		WrittenByAgentID: agentID,
	}
	start := time.Now()

	// INVARIANT: write ordering is content → metadata → embed → ledger.
	// If content succeeds but metadata fails, content blobs are orphaned
	// and reclaimed by the orphan GC sweeper (§7.3 option a). The
	// reverse (metadata first, content second) would leave a memory
	// visible but unreadable — a worse failure mode.

	mem.ID = types.NewID()
	cells, err := ck.Chunk(ctx, req.Content)
	if err != nil {
		return nil, err
	}
	for i := range cells {
		cells[i].MemoryID = mem.ID
		cells[i].WrittenByAgentID = agentID
		cells[i].CellID = types.NewID()
	}

	// Step 1: content writes (orphan-safe — GC reclaims if step 2 fails).
	if s.Content != nil {
		_ = s.Content.PutMemoryContent(ctx, workspaceID, mem.ID, types.MD5Hex(req.Content), req.Content)
		for _, cell := range cells {
			_ = s.Content.PutCellContent(ctx, workspaceID, mem.ID, cell.CellID, cell.TextMD5, cell.Text)
		}
	}

	// Step 2: metadata writes.
	wmk, err := s.Metadata.ImprintMemory(ctx, mem)
	if err != nil {
		return nil, err
	}
	if err := s.Metadata.UpsertCells(ctx, mem.ID, cells); err != nil {
		return nil, err
	}
	// Inline-embed so the response reports a finalized recall_ready;
	// with a Pool the cells are queued and recall_ready flips later.
	wantAutoLink := req.AutoLink == nil || *req.AutoLink
	embedRes, err := s.embedCells(ctx, workspaceID, req.CollectionID, mem.ID, nil, cells)
	recallReady := false
	switch {
	case err != nil:
	case s.Pool != nil:
		if s.Graph != nil && !wantAutoLink {
			s.noAutoLink.Store(mem.ID, struct{}{})
		}
		s.submitPendingEmbeds(ctx, workspaceID, req.CollectionID, mem.ID)
	default:
		_ = s.Metadata.UpsertCells(ctx, mem.ID, cells)
		s.markRecallReady(ctx, mem.ID)
		recallReady = true
	}

	var autoLinked []types.Edge
	if recallReady && s.Graph != nil && wantAutoLink {
		autoLinked, _ = autolink.AutoLink(ctx, autolink.Deps{
			Metadata: s.Metadata, Vector: s.Vector,
			Graph: s.Graph, Ledger: s.Ledger,
			Logger: slog.Default(),
		}, workspaceID, mem, cells, embedRes.Embeddings, s.Embedder.ModelID())
	}

	ledgerMeta := map[string]any{"cells_created": len(cells), "cells_re_embedded": len(embedRes.Reembed)}
	if len(autoLinked) > 0 {
		ledgerMeta["auto_link_edges"] = len(autoLinked)
	}
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "imprint", Target: mem.ID, AgentID: agentID,
		WatermarkAfter: wmk,
		LatencyMS:      int(time.Since(start).Milliseconds()),
		Metadata:       ledgerMeta,
	})
	return &api.ImprintResponse{
		ID:               mem.ID,
		Watermark:        wmk,
		ContentMD5:       mem.ContentMD5,
		CellsCreated:     len(cells),
		RecallReady:      recallReady,
		AutoLinkedEdges:  len(autoLinked),
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
	existing, err := s.Metadata.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	mem := *existing
	mem.Content = req.Content
	mem.Tags = req.Tags
	mem.LastModifiedByAgentID = agentID
	start := time.Now()
	newWmk, err := s.Metadata.UpdateMemory(ctx, workspaceID, memoryID, expected, &mem)
	if err != nil {
		return nil, err
	}
	ck, err := s.workspaceChunker(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	oldCells, _ := s.Metadata.GetCells(ctx, workspaceID, memoryID)
	newCells, err := ck.Chunk(ctx, req.Content)
	if err != nil {
		return nil, err
	}
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := s.embedCells(ctx, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Metadata.UpsertCells(ctx, memoryID, newCells)
	s.reconcileCells(ctx, workspaceID, memoryID, oldCells, newCells)
	s.submitPendingEmbeds(ctx, workspaceID, existing.CollectionID, memoryID)
	s.markRecallReady(ctx, memoryID)
	if s.Content != nil {
		_ = s.Content.PutMemoryContent(ctx, workspaceID, memoryID, types.MD5Hex(req.Content), req.Content)
		for _, cell := range newCells {
			_ = s.Content.PutCellContent(ctx, workspaceID, memoryID, cell.CellID, cell.TextMD5, cell.Text)
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
		ID:                    memoryID,
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
	newWmk, _, newContent, err := s.Metadata.PatchMemory(ctx, workspaceID, memoryID, expected, req.Patch, agentID)
	if err != nil {
		return nil, err
	}
	existing, _ := s.Metadata.GetMemory(ctx, memoryID)
	ck, err := s.workspaceChunker(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	oldCells, _ := s.Metadata.GetCells(ctx, workspaceID, memoryID)
	newCells, err := ck.Chunk(ctx, newContent)
	if err != nil {
		return nil, err
	}
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := s.embedCells(ctx, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Metadata.UpsertCells(ctx, memoryID, newCells)
	s.reconcileCells(ctx, workspaceID, memoryID, oldCells, newCells)
	s.submitPendingEmbeds(ctx, workspaceID, existing.CollectionID, memoryID)
	s.markRecallReady(ctx, memoryID)
	if s.Content != nil {
		_ = s.Content.PutMemoryContent(ctx, workspaceID, memoryID, types.MD5Hex(newContent), newContent)
		for _, cell := range newCells {
			_ = s.Content.PutCellContent(ctx, workspaceID, memoryID, cell.CellID, cell.TextMD5, cell.Text)
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
		ID:                    memoryID,
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
	newWmk, md5, err := s.Metadata.AppendMemory(ctx, workspaceID, memoryID, expected, req.Content, agentID)
	if err != nil {
		return nil, err
	}
	existing, _ := s.Metadata.GetMemory(ctx, memoryID)
	ck, err := s.workspaceChunker(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	oldCells, _ := s.Metadata.GetCells(ctx, workspaceID, memoryID)
	newCells, err := ck.Chunk(ctx, existing.Content)
	if err != nil {
		return nil, err
	}
	for i := range newCells {
		newCells[i].MemoryID = memoryID
		newCells[i].WrittenByAgentID = agentID
	}
	embedRes, _ := s.embedCells(ctx, workspaceID, existing.CollectionID, memoryID, oldCells, newCells)
	_ = s.Metadata.UpsertCells(ctx, memoryID, newCells)
	s.reconcileCells(ctx, workspaceID, memoryID, oldCells, newCells)
	s.submitPendingEmbeds(ctx, workspaceID, existing.CollectionID, memoryID)
	s.markRecallReady(ctx, memoryID)
	if s.Content != nil {
		_ = s.Content.PutMemoryContent(ctx, workspaceID, memoryID, md5, existing.Content)
		for _, cell := range newCells {
			_ = s.Content.PutCellContent(ctx, workspaceID, memoryID, cell.CellID, cell.TextMD5, cell.Text)
		}
	}
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "append", Target: memoryID, AgentID: agentID,
		WatermarkAfter: newWmk,
		LatencyMS:      int(time.Since(start).Milliseconds()),
		Metadata:       map[string]any{"cells_added": len(embedRes.Reembed), "cells_skipped": len(embedRes.Skipped)},
	})
	return &api.AppendResponse{
		ID:                    memoryID,
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
	// INVARIANT: forget ordering is metadata → content → vector → ledger.
	// Metadata goes first so the memory becomes invisible immediately.
	// If content delete fails, the blob is orphaned but unreachable —
	// the GC sweeper will reclaim it. Never delete content first, or a
	// concurrent reader could see a memory with no body.
	if err := s.Metadata.ForgetMemory(ctx, workspaceID, memoryID); err != nil {
		return nil, err
	}
	if s.Content != nil {
		_ = s.Content.DeleteAllForMemory(ctx, workspaceID, memoryID)
	}
	if s.Vector != nil {
		_ = s.Vector.DeleteVectors(ctx, []adapter.VectorKey{
			{WorkspaceID: workspaceID, MemoryID: memoryID},
		})
	}
	n, _ := s.GraphCascadeForget(ctx, workspaceID, memoryID, agentID)
	wmk := types.NewWatermark()
	ledgerID := s.appendLedger(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID, Op: "forget", Target: memoryID, AgentID: agentID,
		WatermarkAfter: wmk, Metadata: map[string]any{"cascaded_edges": n},
	})
	return &api.ForgetResponse{
		ID:            memoryID,
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
			m, err := s.Metadata.GetMemory(ctx, id)
			if err != nil || m.WorkspaceID != workspaceID {
				continue // other workspaces' memories are indistinguishable from missing ones
			}
			seeds = append(seeds, api.RecallHit{
				MemoryID: m.ID, Text: m.Content, Watermark: m.HeadWatermark, Via: "seed",
				WrittenByAgentID: m.WrittenByAgentID,
			})
		}
	case api.RecallModeKeyword:
		hits, err := s.keywordSearch(ctx, adapter.VectorQuery{WorkspaceID: workspaceID}, req, k)
		if err != nil {
			return nil, err
		}
		seeds = hits
		totalScanned = len(hits)
	case api.RecallModeVector:
		hits, err := s.vectorSearch(ctx, adapter.VectorQuery{WorkspaceID: workspaceID}, req, k)
		if err != nil {
			return nil, err
		}
		seeds = hits
		totalScanned = len(hits)
	case api.RecallModeHybrid:
		kw, _ := s.keywordSearch(ctx, adapter.VectorQuery{WorkspaceID: workspaceID}, req, k)
		vec, _ := s.vectorSearch(ctx, adapter.VectorQuery{WorkspaceID: workspaceID}, req, k)
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

// Search runs a keyword, vector or hybrid query across every workspace
// on the node, or the subset in req.WorkspaceIDs, and returns the top k
// hits, each carrying its workspace_id.
//
// Both sides are single store queries over the scope: the full-text
// index for keyword, the vector store for vector.
func (s *Service) Search(ctx context.Context, req api.SearchRequest) (*api.RecallResponse, error) {
	start := time.Now()
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("%w: query is required", types.ErrInvalidInput)
	}
	for _, id := range req.WorkspaceIDs {
		if _, err := types.ParseID("workspace_ids", id); err != nil {
			return nil, err
		}
	}
	mode := req.Mode
	if mode == "" {
		mode = api.RecallModeHybrid
	}
	k := req.K
	if k <= 0 {
		k = 5
	}
	recallReq := api.RecallRequest{Query: req.Query, Mode: mode, K: k, Filters: req.Filters, Weights: req.Weights}
	scope := adapter.VectorQuery{WorkspaceIDs: req.WorkspaceIDs, AllWorkspaces: len(req.WorkspaceIDs) == 0}

	var hits []api.RecallHit
	var scanned int
	switch mode {
	case api.RecallModeKeyword:
		kw, err := s.keywordSearch(ctx, scope, recallReq, k)
		if err != nil {
			return nil, err
		}
		hits, scanned = kw, len(kw)
	case api.RecallModeVector:
		vec, err := s.vectorSearch(ctx, scope, recallReq, k)
		if err != nil {
			return nil, err
		}
		hits, scanned = vec, len(vec)
	case api.RecallModeHybrid:
		kw, _ := s.keywordSearch(ctx, scope, recallReq, k)
		vec, _ := s.vectorSearch(ctx, scope, recallReq, k)
		hits, scanned = hybridMerge(kw, vec, req.Weights, k), len(kw)+len(vec)
	default:
		return nil, fmt.Errorf("%w: search mode must be keyword, vector or hybrid, got %q", types.ErrInvalidInput, mode)
	}
	sortHits(hits)
	if len(hits) > k {
		hits = hits[:k]
	}
	return &api.RecallResponse{
		Results:                hits,
		TotalCandidatesScanned: scanned,
		LatencyMS:              int(time.Since(start).Milliseconds()),
	}, nil
}

// markRecallReady persists recall_ready once every cell of the memory has a
// vector key. The update is conditional and idempotent, so calling it after
// a partially failed embed is harmless (Issue #4379).
// embedCells applies the moat classification to newCells. Without a Pool
// it embeds the changed cells inline; with one it only classifies, leaving
// them vectorless for submitPendingEmbeds, so the re-embed and skip counts
// are the same in both modes.
func (s *Service) embedCells(ctx context.Context, workspaceID, collectionID, memoryID string, oldCells, newCells []types.Cell) (embedqueue.Result, error) {
	if s.Pool == nil {
		return embedqueue.EmbedNow(ctx, embedqueue.EmbedderDeps{
			Metadata: s.Metadata, Vector: s.Vector, Provider: s.Embedder,
		}, workspaceID, collectionID, memoryID, oldCells, newCells)
	}
	reembed, skipped := embedqueue.ClassifyCells(oldCells, newCells)
	res := embedqueue.Result{Skipped: skipped}
	for _, i := range reembed {
		res.Reembed = append(res.Reembed, newCells[i].CellID)
	}
	return res, nil
}

// reconcileCells removes what an edit leaves behind once the new cells are
// stored (#6037): the stored cells beyond the new count, with their content,
// and every vector no current cell references, which covers a removed cell's
// vector and pre-#6037 orphans left under an old cell_id. It runs before
// submitPendingEmbeds, so the pool never re-embeds a removed cell and cannot
// have its fresh vector deleted. Best-effort: failures are logged.
func (s *Service) reconcileCells(ctx context.Context, workspaceID, memoryID string, oldCells, newCells []types.Cell) {
	removed, err := s.Metadata.DeleteCellsFrom(ctx, workspaceID, memoryID, len(newCells))
	if err != nil {
		slog.Default().Warn("delete removed cells failed", "memory_id", memoryID, "err", err)
	}
	if s.Content != nil {
		for _, id := range removed {
			if err := s.Content.DeleteCellContent(ctx, workspaceID, memoryID, id); err != nil && !errors.Is(err, types.ErrNotFound) {
				slog.Default().Warn("delete removed cell content failed", "memory_id", memoryID, "cell_id", id, "err", err)
			}
		}
	}
	if s.Vector == nil {
		return
	}
	live := make(map[string]bool, len(newCells))
	for _, c := range newCells {
		if c.VectorKey != "" {
			live[c.VectorKey] = true
		}
	}
	var stale []adapter.VectorKey
	seen := map[string]bool{}
	for _, c := range oldCells {
		for _, k := range []string{c.CellID, c.VectorKey} {
			if k != "" && !live[k] && !seen[k] {
				seen[k] = true
				stale = append(stale, adapter.VectorKey{WorkspaceID: workspaceID, CellID: k})
			}
		}
	}
	if len(stale) > 0 {
		if err := s.Vector.DeleteVectors(ctx, stale); err != nil {
			slog.Default().Warn("delete stale vectors failed", "memory_id", memoryID, "err", err)
		}
	}
}

// submitPendingEmbeds queues every stored cell of memoryID that has no
// vector yet. It reads the stored rows rather than the cells just written
// because UpsertCells keeps a row's original cell_id when it rewrites a
// seq, and the pool records the vector against that id.
func (s *Service) submitPendingEmbeds(ctx context.Context, workspaceID, collectionID, memoryID string) {
	if s.Pool == nil {
		return
	}
	cells, err := s.Metadata.GetCells(ctx, workspaceID, memoryID)
	if err != nil {
		slog.Default().Warn("async embed: list cells failed; memory stays not recall_ready", "memory_id", memoryID, "err", err)
		return
	}
	for _, c := range cells {
		if c.VectorKey == "" {
			s.Pool.Submit(embedqueue.Job{WorkspaceID: workspaceID, CollectionID: collectionID, MemoryID: memoryID, Cell: c})
		}
	}
}

func (s *Service) markRecallReady(ctx context.Context, memoryID string) {
	if _, err := s.Metadata.FlipRecallReadyIfAllEmbedded(ctx, memoryID); err != nil {
		slog.Default().Warn("recall_ready flip failed", "memory_id", memoryID, "err", err)
	}
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
		e.LedgerID = types.NewID()
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

// keywordSearch hits the FTS5 virtual table on the SQLite MetadataStore.
// Adapter-specific; falls back to a substring scan for adapters without FTS.
// keywordSearch ranks memories in scope against the store's full-text
// index (one query, node-wide or scoped) and turns the top k into hits.
// scope carries only workspace scope fields; WorkspaceID is mapped onto
// the keyword query's list form. Filters other than collection run after
// ranking (S-4 pushes them down); each hit costs one memory load (S-5
// batches them).
func (s *Service) keywordSearch(ctx context.Context, scope adapter.VectorQuery, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	kq := adapter.KeywordQuery{
		WorkspaceIDs:  scope.WorkspaceIDs,
		AllWorkspaces: scope.AllWorkspaces,
		Query:         req.Query,
		CollectionID:  req.Filters.CollectionID,
		K:             k,
	}
	if scope.WorkspaceID != "" {
		kq.WorkspaceIDs = []string{scope.WorkspaceID}
	}
	ranked, err := s.Metadata.SearchKeyword(ctx, kq)
	if err != nil {
		return nil, err
	}
	hits := make([]api.RecallHit, 0, len(ranked))
	for _, r := range ranked {
		m, err := s.Metadata.GetMemory(ctx, r.MemoryID)
		if err != nil || !passesFilter(*m, req.Filters) {
			continue
		}
		hits = append(hits, api.RecallHit{
			MemoryID:         m.ID,
			WorkspaceID:      m.WorkspaceID,
			Text:             excerpt(m.Content, req.Query, 240),
			Tags:             m.Tags,
			Watermark:        m.HeadWatermark,
			WrittenByAgentID: m.WrittenByAgentID,
			KeywordScore:     r.Score,
			Score:            r.Score,
			Via:              "seed",
		})
	}
	return hits, nil
}

// vectorSearch embeds the query and searches the vector store within
// scope, which carries only the workspace scope fields of a VectorQuery.
func (s *Service) vectorSearch(ctx context.Context, scope adapter.VectorQuery, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	if s.Embedder == nil {
		return nil, nil
	}
	vecs, err := s.Embedder.Embed(ctx, []string{req.Query})
	if err != nil || len(vecs) == 0 {
		return nil, nil //nolint:nilerr // embedding failure degrades recall to keyword results by design
	}
	scope.Embedding = vecs[0]
	scope.K = k
	scope.Filter = adapter.VectorFilter{CollectionID: req.Filters.CollectionID}
	res, err := s.Vector.Query(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make([]api.RecallHit, 0, len(res))
	for _, v := range res {
		m, err := s.Metadata.GetMemory(ctx, v.Key.MemoryID)
		if err != nil {
			continue
		}
		if !passesFilter(*m, req.Filters) {
			continue
		}
		out = append(out, api.RecallHit{
			MemoryID:         m.ID,
			WorkspaceID:      m.WorkspaceID,
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
		tr, err := s.GraphTraverse(ctx, workspaceID, seed.MemoryID, adapter.TraverseOpts{
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
				m, err := s.Metadata.GetMemory(ctx, hit.Memory.MemoryID)
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
	case errors.Is(err, types.ErrAlreadyExists), errors.Is(err, types.ErrAlreadyLinked), errors.Is(err, types.ErrSyntheticEdge), errors.Is(err, types.ErrNotEmpty):
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
	case errors.Is(err, types.ErrNotEmpty):
		return "not_empty"
	case errors.Is(err, types.ErrInvalidInput):
		return "invalid_input"
	default:
		return "internal_error"
	}
}
