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
	"unicode/utf8"

	"log/slog"

	"github.com/axiom-studio/memora/internal/autolink"
	"github.com/axiom-studio/memora/internal/chunker"
	embedqueue "github.com/axiom-studio/memora/internal/embed/queue"
	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/internal/rank"
	"github.com/axiom-studio/memora/internal/reqctx"
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
	// Federation, when set, serves Search requests with federated: true
	// by fanning out to this node's peers. nil = federation off.
	Federation *federation.Searcher
	// Reranker re-scores merged federated hits when a search sets
	// rerank: true. nil = federation.NoopReranker (keep the fused order).
	Reranker federation.Reranker
	// Pool, when set, takes cell embedding off the write path: writes
	// queue vectorless cells and the pool flips recall_ready once they
	// are all embedded (`embedding.async`). nil = embed inline.
	Pool *embedqueue.Pool

	// noAutoLink holds memory IDs imprinted with auto_link=false while
	// Pool is set, so the pool's post-embed hook skips them.
	noAutoLink sync.Map
}

// requireAgent rejects a write whose agent is not a UUID. HTTP and MCP
// check at their boundary; this guards every other caller (the console,
// background workers, CLI tools), so no write can record a non-UUID
// agent in last_modified_by, watermark history or the ledger (#4378).
func requireAgent(agentID string) error {
	_, err := types.ParseID("agent_id", agentID)
	return err
}

// TouchAgent records that agentID wrote in workspaceID, registering it
// there if the workspace has never seen it. Unlike RegisterAgent it never
// reactivates a deactivated agent or overwrites its registration (#6169).
func (s *Service) TouchAgent(ctx context.Context, workspaceID, agentID, identityProvider string) error {
	if t, ok := s.Metadata.(adapter.AgentToucher); ok {
		return t.TouchAgent(ctx, workspaceID, agentID, identityProvider)
	}
	if _, err := s.Metadata.GetAgent(ctx, workspaceID, agentID); !errors.Is(err, types.ErrNotFound) {
		return err
	}
	return s.Metadata.RegisterAgent(ctx, &types.Agent{AgentID: agentID, WorkspaceID: workspaceID, IdentityProvider: identityProvider})
}

// RegisterAgent registers or re-registers an agent explicitly (REST,
// console, MCP). It is the one way to reactivate a deactivated agent,
// and it is ledgered as agent_register, noting a reactivation. byAgent
// is who did it, when known.
func (s *Service) RegisterAgent(ctx context.Context, a *types.Agent, byAgent string) error {
	prev, err := s.Metadata.GetAgent(ctx, a.WorkspaceID, a.AgentID)
	if err != nil && !errors.Is(err, types.ErrNotFound) {
		return err
	}
	if err := s.Metadata.RegisterAgent(ctx, a); err != nil {
		return err
	}
	s.AppendLedger(ctx, api.LedgerEntry{
		WorkspaceID: a.WorkspaceID, Op: "agent_register", Target: a.AgentID, AgentID: byAgent,
		Metadata: map[string]any{"reactivated": prev != nil && !prev.Active, "new": prev == nil},
	})
	return nil
}

// DeactivateAgent stops an agent writing in a workspace (#6169); it is
// ledgered as agent_deactivate. byAgent is who did it, when known.
func (s *Service) DeactivateAgent(ctx context.Context, workspaceID, agentID, byAgent string) error {
	if err := s.Metadata.DeactivateAgent(ctx, workspaceID, agentID); err != nil {
		return err
	}
	s.AppendLedger(ctx, api.LedgerEntry{WorkspaceID: workspaceID, Op: "agent_deactivate", Target: agentID, AgentID: byAgent})
	return nil
}

// admitAgent is requireAgent plus revocation (#6169): an agent that is
// registered in workspaceID and deactivated there may not write. An
// agent the workspace has never registered is admitted (the write
// middleware registers it).
func (s *Service) admitAgent(ctx context.Context, workspaceID, agentID string) error {
	if err := requireAgent(agentID); err != nil {
		return err
	}
	if s.Metadata == nil || workspaceID == "" {
		return nil
	}
	a, err := s.Metadata.GetAgent(ctx, workspaceID, agentID)
	switch {
	case errors.Is(err, types.ErrNotFound):
		return nil
	case err != nil:
		return err
	case !a.Active:
		return fmt.Errorf("%w: agent %s is deactivated in workspace %s", types.ErrAgentDeactivated, agentID, workspaceID)
	}
	return nil
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

// GraphLink links two memories; see Link.
func (s *Service) GraphLink(ctx context.Context, e types.Edge) (types.Edge, error) {
	out, _, err := s.Link(ctx, e)
	return out, err
}

// Link creates an edge and ledgers it as "link", returning the ledger
// ID. Every caller (REST, MCP, console) goes through it, so every link
// is audited (#6171).
func (s *Service) Link(ctx context.Context, e types.Edge) (types.Edge, string, error) {
	if s.Graph == nil {
		return types.Edge{}, "", fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	if err := s.admitAgent(ctx, e.WorkspaceID, e.CreatedByAgentID); err != nil {
		return types.Edge{}, "", err
	}
	out, err := s.Graph.Link(ctx, e)
	if err != nil {
		return types.Edge{}, "", err
	}
	ledgerID := s.AppendLedger(ctx, linkEntry(out, false))
	return out, ledgerID, nil
}

func linkEntry(e types.Edge, batch bool) api.LedgerEntry {
	meta := map[string]any{"edge_type": string(e.EdgeType), "source_memory_id": e.SourceMemoryID, "target_memory_id": e.TargetMemoryID}
	if batch {
		meta["batch"] = true
	}
	return api.LedgerEntry{
		WorkspaceID: e.WorkspaceID, Op: "link", Target: e.EdgeID, AgentID: e.CreatedByAgentID,
		WatermarkAfter: e.Watermark, Metadata: meta,
	}
}

// GraphUnlink delegates to the configured GraphStore.
func (s *Service) GraphUnlink(ctx context.Context, workspaceID, edgeID, agentID string) error {
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return err
	}
	if s.Graph == nil {
		return fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	if err := s.Graph.Unlink(ctx, workspaceID, edgeID, agentID); err != nil {
		return err
	}
	// The graph store keeps no record of who removed an edge; the ledger
	// does.
	s.AppendLedger(ctx, api.LedgerEntry{WorkspaceID: workspaceID, Op: "unlink", Target: edgeID, AgentID: agentID})
	return nil
}

// GraphLinkBatch delegates to the configured GraphStore.
func (s *Service) GraphLinkBatch(ctx context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("%w: graph store not configured", types.ErrCapability)
	}
	checked := map[[2]string]bool{}
	for _, e := range edges {
		k := [2]string{e.WorkspaceID, e.CreatedByAgentID}
		if checked[k] || requireAgent(e.CreatedByAgentID) != nil {
			continue // a malformed agent fails its own edge in the store
		}
		checked[k] = true
		if err := s.admitAgent(ctx, e.WorkspaceID, e.CreatedByAgentID); err != nil {
			return nil, err
		}
	}
	results, err := s.Graph.LinkBatch(ctx, edges)
	if err != nil {
		return nil, err
	}
	// One "link" entry per edge created, as a single link writes.
	for _, r := range results {
		if r.Status == "ok" && r.Index >= 0 && r.Index < len(edges) {
			e := edges[r.Index]
			e.EdgeID = r.EdgeID
			s.AppendLedger(ctx, linkEntry(e, true))
		}
	}
	return results, nil
}

// CreatePin saves a query and ledgers it as "pin" (#6171). p.CreatedBy
// is the agent that pins it.
func (s *Service) CreatePin(ctx context.Context, p *types.Pin) error {
	if p.CreatedBy != "" {
		if err := s.admitAgent(ctx, p.WorkspaceID, p.CreatedBy); err != nil {
			return err
		}
	}
	if err := s.Metadata.CreatePin(ctx, p); err != nil {
		return err
	}
	s.AppendLedger(ctx, api.LedgerEntry{
		WorkspaceID: p.WorkspaceID, Op: "pin", Target: p.PinID, AgentID: p.CreatedBy, WatermarkAfter: p.Watermark,
		Metadata: map[string]any{"query": p.Query, "mode": p.Mode, "k": p.K, "label": p.Label},
	})
	return nil
}

// DeletePin removes a saved query and ledgers it as "unpin".
func (s *Service) DeletePin(ctx context.Context, workspaceID, pinID, agentID string) error {
	if agentID != "" {
		if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
			return err
		}
	}
	if err := s.Metadata.DeletePin(ctx, workspaceID, pinID); err != nil {
		return err
	}
	s.AppendLedger(ctx, api.LedgerEntry{WorkspaceID: workspaceID, Op: "unpin", Target: pinID, AgentID: agentID})
	return nil
}

// GraphCascadeForget delegates to the configured GraphStore.
func (s *Service) GraphCascadeForget(ctx context.Context, workspaceID, memoryID, agentID string) (int, error) {
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return 0, err
	}
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
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
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
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
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
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
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
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
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
	if err := s.admitAgent(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
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
		// A memory key removes every vector of the memory. On failure the
		// vectors are orphaned but never returned (hydrate drops deleted
		// memories); `memora-core reembed` removes them.
		if err := s.Vector.DeleteVectors(ctx, []adapter.VectorKey{
			{WorkspaceID: workspaceID, MemoryID: memoryID},
		}); err != nil {
			slog.Default().Warn("forget: delete vectors failed; run memora-core reembed to remove them",
				"workspace_id", workspaceID, "memory_id", memoryID, "err", err)
		}
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
	if err := validateRecallIDs(req.Filters, req.MemoryIDs); err != nil {
		return nil, err
	}
	mode := req.Mode
	if mode == "" {
		mode = api.RecallModeHybrid
	}
	switch {
	case req.K > MaxK:
		return nil, fmt.Errorf("%w: k is at most %d, got %d", types.ErrInvalidInput, MaxK, req.K)
	case len(req.MemoryIDs) > MaxK:
		return nil, fmt.Errorf("%w: memory_ids takes at most %d IDs, got %d", types.ErrInvalidInput, MaxK, len(req.MemoryIDs))
	}
	k := req.K
	if k <= 0 {
		k = 5
	}
	var seeds []api.RecallHit
	var totalScanned int
	var degraded []string
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
		hits, scanned, deg, err := s.hybridSearch(ctx, adapter.VectorQuery{WorkspaceID: workspaceID}, req, k)
		if err != nil {
			return nil, err
		}
		seeds, totalScanned, degraded = hits, scanned, deg
	default:
		return nil, fmt.Errorf("%w: unknown recall mode %q", types.ErrInvalidInput, mode)
	}
	if mode != api.RecallModeLookup {
		var err error
		if seeds, err = s.hydrate(ctx, seeds, hydrateOpts{
			query: req.Query, filters: req.Filters, includeContent: req.IncludeContent,
			maxContentBytes: req.MaxContentBytes, includeCells: req.IncludeCells,
		}); err != nil {
			return nil, err
		}
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
	applyResponseBudget(results, mode == api.RecallModeLookup)
	return &api.RecallResponse{
		Results:                results,
		Degraded:               degraded,
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
//
// With req.Federated it also fans out to this node's peers (F-4): the
// local search runs first, then every selected peer is asked in
// parallel. A peer that fails, times out or is rate-limited is listed in
// failed_peers and never fails the search.
func (s *Service) Search(ctx context.Context, req api.SearchRequest) (*api.RecallResponse, error) {
	if !req.Federated {
		if len(req.Peers) > 0 || req.PerPeerK != 0 || req.TimeoutMS != 0 || req.Rerank {
			return nil, fmt.Errorf("%w: peers, per_peer_k, timeout_ms and rerank need federated: true", types.ErrInvalidInput)
		}
		return s.localSearch(ctx, req)
	}
	return s.federatedSearch(ctx, req)
}

func (s *Service) federatedSearch(ctx context.Context, req api.SearchRequest) (*api.RecallResponse, error) {
	start := time.Now()
	switch {
	case s.Federation == nil:
		return nil, fmt.Errorf("%w: federation is not enabled on this node", types.ErrInvalidInput)
	case len(req.WorkspaceIDs) > 0:
		return nil, fmt.Errorf("%w: workspace_ids cannot be combined with federated: peers search their whole node", types.ErrInvalidInput)
	case req.PerPeerK < 0 || req.TimeoutMS < 0:
		return nil, fmt.Errorf("%w: per_peer_k and timeout_ms must not be negative", types.ErrInvalidInput)
	case req.PerPeerK > MaxK:
		return nil, fmt.Errorf("%w: per_peer_k is at most %d, got %d", types.ErrInvalidInput, MaxK, req.PerPeerK)
	}
	local, err := s.localSearch(ctx, req)
	if err != nil {
		return nil, err
	}
	for i := range local.Results {
		local.Results[i].NodeID, local.Results[i].NodeName = s.NodeID, s.NodeName
	}
	k := req.K
	if k <= 0 {
		k = 5
	}
	perPeerK := req.PerPeerK
	if perPeerK == 0 {
		perPeerK = k
	}
	fr, err := s.Federation.Fanout(ctx, local, api.FederationSearchRequest{
		Query: req.Query, Mode: req.Mode, K: perPeerK,
		IncludeContent: req.IncludeContent, MaxContentBytes: req.MaxContentBytes,
	}, federation.SearchOpts{Peers: req.Peers, Timeout: time.Duration(req.TimeoutMS) * time.Millisecond})
	if err != nil {
		return nil, err
	}
	s.AppendLedger(ctx, federation.SearchEntry(req.Query, fr))
	var hits []api.RecallHit
	if req.Rerank {
		// The re-scorer sees a wider pool than k, so it can promote hits
		// that fusion ranked just below the cut.
		pool := federation.Merge(fr, min(4*k, maxRerankPool))
		reranker := s.Reranker
		if reranker == nil {
			reranker = federation.NoopReranker{}
		}
		if hits, err = reranker.Rerank(ctx, req.Query, pool); err != nil {
			slog.Default().Warn("federated search: rerank failed; keeping the fused order", "err", err)
			hits = pool
		}
		if len(hits) > k {
			hits = hits[:k]
		}
	} else {
		hits = federation.Merge(fr, k)
	}
	applyResponseBudget(hits, false)
	return &api.RecallResponse{
		Results:                hits,
		Degraded:               local.Degraded,
		TotalCandidatesScanned: fr.TotalScanned,
		PartialSuccess:         fr.PartialSuccess(),
		FailedPeers:            fr.FailedPeers(),
		PeerLatencyMS:          fr.PeerLatencyMS,
		LatencyMS:              int(time.Since(start).Milliseconds()),
	}, nil
}

// Filter size limits (validateRecallIDs).
const (
	maxFilterTagKeys = 32
	maxFilterValues  = 100
)

// maxRerankPool caps how many fused hits a Reranker is given.
const maxRerankPool = 200

// localSearch is Search on this node only.
func (s *Service) localSearch(ctx context.Context, req api.SearchRequest) (*api.RecallResponse, error) {
	start := time.Now()
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("%w: query is required", types.ErrInvalidInput)
	}
	for _, id := range req.WorkspaceIDs {
		if _, err := types.ParseID("workspace_ids", id); err != nil {
			return nil, err
		}
	}
	if err := validateRecallIDs(req.Filters, nil); err != nil {
		return nil, err
	}
	mode := req.Mode
	if mode == "" {
		mode = api.RecallModeHybrid
	}
	if req.K > MaxK {
		return nil, fmt.Errorf("%w: k is at most %d, got %d", types.ErrInvalidInput, MaxK, req.K)
	}
	k := req.K
	if k <= 0 {
		k = 5
	}
	recallReq := api.RecallRequest{Query: req.Query, Mode: mode, K: k, Filters: req.Filters, Weights: req.Weights}
	scope := adapter.VectorQuery{WorkspaceIDs: req.WorkspaceIDs, AllWorkspaces: len(req.WorkspaceIDs) == 0}

	var hits []api.RecallHit
	var scanned int
	var degraded []string
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
		var err error
		if hits, scanned, degraded, err = s.hybridSearch(ctx, scope, recallReq, k); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: search mode must be keyword, vector or hybrid, got %q", types.ErrInvalidInput, mode)
	}
	sortHits(hits)
	if len(hits) > k {
		hits = hits[:k]
	}
	hits, err := s.hydrate(ctx, hits, hydrateOpts{
		query: req.Query, filters: req.Filters, includeContent: req.IncludeContent,
		maxContentBytes: req.MaxContentBytes, includeCells: req.IncludeCells,
	})
	if err != nil {
		return nil, err
	}
	applyResponseBudget(hits, false)
	return &api.RecallResponse{
		Results:                hits,
		Degraded:               degraded,
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
	if e.RequestID == "" {
		e.RequestID = reqctx.RequestID(ctx)
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

// keywordSearch ranks memories in scope against the store's full-text
// index (one query, node-wide or scoped) and returns the top k as
// candidate hits: IDs and scores only, for hydrate to fill. scope carries
// only workspace scope fields; WorkspaceID is mapped onto the keyword
// query's list form.
func (s *Service) keywordSearch(ctx context.Context, scope adapter.VectorQuery, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	kq := adapter.KeywordQuery{
		WorkspaceIDs:  scope.WorkspaceIDs,
		AllWorkspaces: scope.AllWorkspaces,
		Query:         req.Query,
		CollectionID:  req.Filters.CollectionID,
		Filter:        memoryFilter(req.Filters),
		K:             k,
	}
	if scope.WorkspaceID != "" {
		kq.WorkspaceIDs = []string{scope.WorkspaceID}
	}
	ranked, err := s.Metadata.SearchKeyword(ctx, kq)
	if err != nil {
		return nil, err
	}
	hits := make([]api.RecallHit, len(ranked))
	for i, r := range ranked {
		hits[i] = api.RecallHit{MemoryID: r.MemoryID, WorkspaceID: r.WorkspaceID, KeywordScore: r.Score, Score: r.Score, Via: "seed"}
	}
	return hits, nil
}

// vectorSearch embeds the query and searches the vector store within
// scope, which carries only the workspace scope fields of a VectorQuery.
func (s *Service) vectorSearch(ctx context.Context, scope adapter.VectorQuery, req api.RecallRequest, k int) ([]api.RecallHit, error) {
	if s.Embedder == nil || s.Vector == nil {
		return nil, fmt.Errorf("%w: vector search: no embedder or vector store configured", types.ErrCapability)
	}
	if !embedding.Searchable(s.Embedder) {
		return nil, fmt.Errorf("%w: vector search unavailable: embedding model %s produces no searchable vectors; configure a real model (e.g. hash:default offline, or openai/voyage/gemini/local)",
			types.ErrCapability, s.Embedder.ModelID())
	}
	vecs, err := s.Embedder.Embed(ctx, []string{req.Query})
	if err != nil {
		// Provider error text can carry provider internals (and once
		// carried an API key in a URL), so clients get a fixed message and
		// the redacted detail stays in the server log.
		slog.Default().Warn("vector search: embed query failed", "err", embedding.Redact(err.Error()))
		return nil, fmt.Errorf("%w: vector search unavailable: embedding provider error", types.ErrCapability)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("%w: vector search: embedder returned no vector", types.ErrCapability)
	}
	scope.Embedding = vecs[0]
	scope.Filter = adapter.VectorFilter{CollectionID: req.Filters.CollectionID}
	filter := memoryFilter(req.Filters)
	// Hits are cells; over-fetch so that k distinct memories survive the
	// collapse to one (best) cell per memory below. Vector stores hold no
	// memory metadata, so agent, time and tag filters are checked here,
	// and the store is asked again with a doubled limit until k memories
	// pass or it has no more cells (S-4): a filter never leaves fewer than
	// k results while more matches exist.
	limit := min(k*cellsPerMemoryOverfetch, maxVectorCandidates)
	// An exact store returns fewer rows than the limit only when it has
	// no more. An ANN index may return a short page while more rows
	// exist, so for it only a doubled limit that returns no more rows
	// means exhausted (#4359 QA).
	ann := s.Vector.Capabilities().SupportsANN
	prev := -1
	for {
		scope.K = limit
		res, err := s.Vector.Query(ctx, scope)
		if err != nil {
			return nil, err
		}
		out := make([]api.RecallHit, 0, k)
		seen := make(map[string]bool, k)
		for _, v := range res { // best first, so the first cell seen per memory is its best
			if seen[v.Key.MemoryID] {
				continue
			}
			seen[v.Key.MemoryID] = true
			out = append(out, api.RecallHit{
				MemoryID: v.Key.MemoryID, WorkspaceID: v.Key.WorkspaceID, CellID: v.Key.CellID,
				Score: v.Score, VectorScore: v.Score, Via: "seed",
			})
		}
		if filter.HasMemoryFields() {
			if out, err = s.keepMatching(ctx, out, req.Filters); err != nil {
				return nil, err
			}
		}
		exhausted := len(res) < limit && (!ann || len(res) <= prev)
		prev = len(res)
		if len(out) >= k || exhausted || limit >= maxFilteredVectorCandidates {
			if len(out) > k {
				out = out[:k]
			}
			return out, nil
		}
		limit = min(limit*2, maxFilteredVectorCandidates)
	}
}

// maxFilteredVectorCandidates caps how many cells a filtered vector
// search reads while refilling.
const maxFilteredVectorCandidates = 20000

// memoryFilter maps recall filters onto the store-level filter.
func memoryFilter(f api.RecallFilters) adapter.MemoryFilter {
	return adapter.MemoryFilter{
		CollectionID: f.CollectionID, Tags: f.Tags, TsAfter: f.TsAfter, TsBefore: f.TsBefore,
		AgentID: f.AgentID, AgentIDIn: f.AgentIDIn, AgentIDNotIn: f.AgentIDNotIn,
	}
}

// keepMatching drops hits whose memories fail the filters, in order,
// with one batched metadata load.
func (s *Service) keepMatching(ctx context.Context, hits []api.RecallHit, f api.RecallFilters) ([]api.RecallHit, error) {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.MemoryID
	}
	mems, err := s.Metadata.GetMemories(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]types.Memory, len(mems))
	for _, m := range mems {
		byID[m.ID] = m
	}
	out := hits[:0]
	for _, h := range hits {
		if m, ok := byID[h.MemoryID]; ok && passesFilter(m, f) {
			out = append(out, h)
		}
	}
	return out, nil
}

// Result size limits (#6166). MaxK bounds k and per_peer_k on search
// and recall, and memory_ids in lookup mode; the MCP tool schemas
// advertise the same bound.
const (
	MaxK = 100
	// maxResponseContentBytes bounds the content and cell text one
	// search or recall response carries, across all its hits.
	maxResponseContentBytes = 8 << 20
	// maxCellsPerHit bounds include_cells for one hit.
	maxCellsPerHit = 256
)

// applyResponseBudget keeps the content and cell text hits carry under
// maxResponseContentBytes in total. Matched cells are counted first, in
// rank order, since each is its hit's answer: one that would cross the
// budget is cut (MatchedTextTruncated, #6352). Then, in rank order, a
// hit whose content or cells would cross what is left has its content
// cut (ContentTruncated) and its remaining cells omitted
// (CellsTruncated); every later hit keeps its excerpt and matched cell
// only. textIsContent counts Text too, for lookup mode, where Text is
// the memory's whole content.
func applyResponseBudget(hits []api.RecallHit, textIsContent bool) {
	left := maxResponseContentBytes
	for i := range hits {
		h := &hits[i]
		h.MatchedText, h.MatchedTextTruncated = truncateUTF8(h.MatchedText, left)
		left -= len(h.MatchedText)
	}
	for i := range hits {
		h := &hits[i]
		if textIsContent {
			var cut bool
			h.Text, cut = truncateUTF8(h.Text, left)
			h.ContentTruncated = h.ContentTruncated || cut
			left -= len(h.Text)
		}
		if h.Content != "" {
			var cut bool
			h.Content, cut = truncateUTF8(h.Content, left)
			h.ContentTruncated = h.ContentTruncated || cut
			left -= len(h.Content)
		}
		for j, c := range h.Cells {
			if len(c.Text) > left {
				h.Cells, h.CellsTruncated = h.Cells[:j], true
				break
			}
			left -= len(c.Text)
		}
	}
}

// hydrateOpts says what hydrate fills in beyond the always-present data.
type hydrateOpts struct {
	query           string
	filters         api.RecallFilters
	includeContent  bool
	maxContentBytes int
	includeCells    bool
}

// hydrate fills ranked candidate hits with their memories' data, in rank
// order, dropping memories that are gone or fail the filters. It makes
// two store calls whatever k is: one GetMemories and one
// GetCellsForMemories. Each hit gets its matched cell: the vector side's
// best cell, or for keyword hits the cell with the most query terms.
func (s *Service) hydrate(ctx context.Context, hits []api.RecallHit, o hydrateOpts) ([]api.RecallHit, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.MemoryID
	}
	mems, err := s.Metadata.GetMemories(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load search hits: %w", err)
	}
	byID := make(map[string]*types.Memory, len(mems))
	for i := range mems {
		byID[mems[i].ID] = &mems[i]
	}
	cells, err := s.Metadata.GetCellsForMemories(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load search hit cells: %w", err)
	}
	maxBytes := federation.EffectiveMaxContentBytes(o.maxContentBytes)
	terms := adapter.KeywordTerms(o.query)

	out := hits[:0]
	for _, h := range hits {
		m, ok := byID[h.MemoryID]
		if !ok || !passesFilter(*m, o.filters) {
			continue
		}
		h.WorkspaceID = m.WorkspaceID
		h.Tags = m.Tags
		h.Watermark = m.HeadWatermark
		h.WrittenByAgentID = m.WrittenByAgentID
		created := m.CreatedAt
		h.CreatedAt = &created
		h.Text = excerpt(m.Content, terms, 240)
		if c := matchedCell(cells[m.ID], h.CellID, terms); c != nil {
			h.CellID, h.MatchedText = c.CellID, c.Text
			seq := c.Seq
			h.CellIndex = &seq
		}
		if o.includeContent {
			h.Content, h.ContentTruncated = truncateUTF8(m.Content, maxBytes)
		}
		if o.includeCells {
			h.Cells = cells[m.ID]
			if len(h.Cells) > maxCellsPerHit {
				h.Cells, h.CellsTruncated = h.Cells[:maxCellsPerHit], true
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// matchedCell picks the hit's cell: cellID when the vector side named
// one, otherwise the cell containing the most query terms (the first cell
// when none do). nil when the memory has no cells.
func matchedCell(cells []types.Cell, cellID string, terms []string) *types.Cell {
	if len(cells) == 0 {
		return nil
	}
	if cellID != "" {
		for i := range cells {
			if cells[i].CellID == cellID {
				return &cells[i]
			}
		}
	}
	best, bestN := 0, -1
	for i, c := range cells {
		text := strings.ToLower(c.Text)
		n := 0
		for _, t := range terms {
			n += strings.Count(text, t)
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	return &cells[best]
}

// truncateUTF8 cuts s to at most max bytes on a rune boundary and reports
// whether it cut anything.
func truncateUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
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

// excerpt returns up to max bytes of content around the earliest
// occurrence of any term, or its start when no term occurs.
func excerpt(content string, terms []string, max int) string {
	if max <= 0 {
		max = 200
	}
	lc := strings.ToLower(content)
	idx := -1
	for _, t := range terms {
		if i := strings.Index(lc, t); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	start := 0
	if idx > max/2 {
		start = idx - max/2
	}
	for start > 0 && !utf8.RuneStart(content[start]) {
		start--
	}
	end, _ := truncateUTF8(content[start:], max)
	return end
}

// Vector hits are cells; a query asks for this many cells per wanted
// memory, capped, so that collapsing to one cell per memory still leaves k.
const (
	cellsPerMemoryOverfetch = 4
	maxVectorCandidates     = 1000
)

// Hybrid fuses candidate pools larger than k: with only k per side, the
// two lists barely overlap at small k, fusion degenerates into single
// votes, and top-k is no longer a prefix of top-(k+n).
const (
	minHybridPool = 50
	maxHybridPool = 400
)

// hybridPool is the number of candidates each side contributes for a
// final top k.
func hybridPool(k int) int {
	return min(max(4*k, minHybridPool), maxHybridPool)
}

// hybridSearch runs keyword and vector search over scope, each returning a
// candidate pool of hybridPool(k) memories, fuses the two full rankings by
// weighted reciprocal rank fusion (rank.Fuse) and only then keeps the top
// k, so top-k is a prefix of top-(k+n). BM25 and cosine scores never mix. req.Weights scale each source's votes (default
// equal). A failing source is logged and reported in degraded instead of
// failing the search; both failing is an error.
func (s *Service) hybridSearch(ctx context.Context, scope adapter.VectorQuery, req api.RecallRequest, k int) (hits []api.RecallHit, scanned int, degraded []string, err error) {
	pool := hybridPool(k)
	kw, kwErr := s.keywordSearch(ctx, scope, req, pool)
	vec, vecErr := s.vectorSearch(ctx, scope, req, pool)
	if kwErr != nil && vecErr != nil {
		return nil, 0, nil, fmt.Errorf("hybrid search: both sources failed: %w", errors.Join(kwErr, vecErr))
	}
	if kwErr != nil {
		slog.Default().Warn("hybrid search degraded: keyword side failed", "err", kwErr)
		degraded = append(degraded, api.DegradedKeyword)
	}
	if vecErr != nil {
		slog.Default().Warn("hybrid search degraded: vector side failed", "err", vecErr)
		degraded = append(degraded, api.DegradedVector)
	}
	wv, wk := req.Weights.Vector, req.Weights.Keyword
	if wv == 0 && wk == 0 {
		wv, wk = 1, 1
	}
	hits = rank.Fuse([]rank.List{{Hits: vec, Weight: wv}, {Hits: kw, Weight: wk}},
		func(h api.RecallHit) string { return h.MemoryID }, k)
	return hits, len(kw) + len(vec), degraded, nil
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
					Text:             excerpt(m.Content, nil, 240),
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
	case errors.Is(err, types.ErrFederationRevoked), errors.Is(err, types.ErrAgentDeactivated):
		return 403
	case errors.Is(err, types.ErrFederationAuth):
		return 401
	case errors.Is(err, types.ErrFederationLoop):
		return 409
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
	case errors.Is(err, types.ErrFederationRevoked):
		return "federation_peer_revoked"
	case errors.Is(err, types.ErrAgentDeactivated):
		return "agent_deactivated"
	case errors.Is(err, types.ErrFederationAuth):
		return "federation_unauthorized"
	case errors.Is(err, types.ErrFederationLoop):
		return "federation_loop"
	default:
		return "internal_error"
	}
}

// validateRecallIDs rejects a recall or search filter, or a lookup
// memory_ids entry, that is not a UUID, naming the field. Empty values
// are "not set".
func validateRecallIDs(f api.RecallFilters, memoryIDs []string) error {
	// Each tag key becomes a subquery and each value a bound parameter;
	// keep a filter far below SQLite's parameter limit.
	if len(f.Tags) > maxFilterTagKeys || len(f.AgentIDIn) > maxFilterValues || len(f.AgentIDNotIn) > maxFilterValues {
		return fmt.Errorf("%w: filters allow at most %d tag keys and %d agents per list", types.ErrInvalidInput, maxFilterTagKeys, maxFilterValues)
	}
	for k, vs := range f.Tags {
		if len(vs) > maxFilterValues {
			return fmt.Errorf("%w: filters.tags[%q] has %d values, at most %d", types.ErrInvalidInput, k, len(vs), maxFilterValues)
		}
	}
	for _, c := range []struct {
		field string
		ids   []string
	}{
		{"collection_id", []string{f.CollectionID}},
		{"agent_id", []string{f.AgentID}},
		{"agent_id_in", f.AgentIDIn},
		{"agent_id_not_in", f.AgentIDNotIn},
		{"memory_ids", memoryIDs},
	} {
		for _, id := range c.ids {
			if id == "" {
				continue
			}
			if _, err := types.ParseID(c.field, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// FetchMemory returns a memory with its cells by ID alone, from any
// workspace on this node, stamped with this node's node_id and name.
// Deleted and unknown memories are types.ErrNotFound.
func (s *Service) FetchMemory(ctx context.Context, memoryID string) (*api.MemoryEnvelope, error) {
	if _, err := types.ParseID("memory_id", memoryID); err != nil {
		return nil, err
	}
	m, err := s.Metadata.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	cells, err := s.Metadata.GetCells(ctx, m.WorkspaceID, memoryID)
	if err != nil {
		return nil, err
	}
	return &api.MemoryEnvelope{Memory: m, Cells: cells, NodeID: s.NodeID, NodeName: s.NodeName}, nil
}

// FetchMemoryFrom returns a memory from the node that holds it: this
// node when nodeID is empty or this node's ID, otherwise the peer with
// that node ID (F-6). A node that is not a usable peer is
// federation.ErrUnknownNode; a peer failure is returned for
// federation.PeerErrorCode to classify.
func (s *Service) FetchMemoryFrom(ctx context.Context, nodeID, memoryID string) (*api.MemoryEnvelope, error) {
	if nodeID == "" || nodeID == s.NodeID {
		return s.FetchMemory(ctx, memoryID)
	}
	for _, f := range [...][2]string{{"node", nodeID}, {"memory_id", memoryID}} {
		if _, err := types.ParseID(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	if s.Federation == nil {
		return nil, fmt.Errorf("%w: %s (federation is not enabled)", federation.ErrUnknownNode, nodeID)
	}
	env, err := s.Federation.Fetch(ctx, nodeID, memoryID)
	if !errors.Is(err, federation.ErrUnknownNode) {
		s.AppendLedger(ctx, federation.FetchEntry("outbound", nodeID, memoryID, "", federation.OutcomeOf(err)))
	}
	return env, err
}
