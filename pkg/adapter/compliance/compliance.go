// Package compliance ships the public test suite that adapter authors
// run against their PrimaryStore / VectorStore / LedgerStore
// implementations to ensure they satisfy the Memora contract.
//
// Usage from a third-party adapter package:
//
//   import "github.com/axiom-studio/memora/pkg/adapter/compliance"
//
//   func TestMyAdapter(t *testing.T) {
//       compliance.PrimaryStoreSuite(t, func() adapter.PrimaryStore {
//           return openMyStore(t)
//       })
//   }
//
// v0.1 ships the minimum-viable suite: PrimaryStore CRUD + CAS +
// Context Graph roundtrip. The full LedgerStore / VectorStore blocks +
// the depth-cap and forget-cascade tests land in v0.5 (#297 §18.5,
// §19.8).
package compliance

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// PrimaryFactory builds a fresh PrimaryStore for one test. The
// returned store must be Open()-ed and ready to use.
type PrimaryFactory func(t *testing.T) adapter.PrimaryStore

// PrimaryStoreSuite runs the v0.1 PrimaryStore compliance suite.
func PrimaryStoreSuite(t *testing.T, factory PrimaryFactory) {
	t.Helper()
	t.Run("workspace_roundtrip", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "compliance-ws"}
		if err := s.CreateWorkspace(ctx, ws); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		got, err := s.GetWorkspace(ctx, ws.ID)
		if err != nil {
			t.Fatalf("GetWorkspace: %v", err)
		}
		if got.Name != ws.Name {
			t.Fatalf("name roundtrip: %q vs %q", got.Name, ws.Name)
		}
	})

	t.Run("memory_imprint_and_get", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "imprint-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "compliance content",
			WrittenByAgentID: "agent_opaque_compliance",
		}
		wmk, err := s.ImprintMemory(ctx, mem)
		if err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		if wmk == "" {
			t.Fatal("imprint returned empty watermark")
		}
		got, err := s.GetMemory(ctx, mem.ID)
		if err != nil {
			t.Fatalf("GetMemory: %v", err)
		}
		if got.Content != mem.Content {
			t.Fatalf("content roundtrip: %q", got.Content)
		}
	})

	t.Run("update_cas", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "cas-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "v1",
			WrittenByAgentID: "agent_opaque_compliance",
		}
		wmk, _ := s.ImprintMemory(ctx, mem)
		// Happy update.
		mem.Content = "v2"
		mem.LastModifiedByAgentID = "agent_opaque_compliance"
		newWmk, err := s.UpdateMemory(ctx, mem.ID, wmk, mem)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if newWmk == wmk {
			t.Fatal("update didn't bump watermark")
		}
		// CAS conflict.
		if _, err := s.UpdateMemory(ctx, mem.ID, wmk, mem); err == nil {
			t.Fatal("expected ErrCAS on stale watermark")
		}
	})

	t.Run("graph_link_neighbors", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "graph-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_c"}
		b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_c"}
		_, _ = s.ImprintMemory(ctx, a)
		_, _ = s.ImprintMemory(ctx, b)
		_, err := s.GraphLink(ctx, types.Edge{
			WorkspaceID:      ws.ID,
			SourceMemoryID:   a.ID,
			TargetMemoryID:   b.ID,
			EdgeType:         types.EdgeTypeReferences,
			CreatedByAgentID: "agent_opaque_c",
		})
		if err != nil {
			t.Fatalf("GraphLink: %v", err)
		}
		edges, _, err := s.GraphNeighbors(ctx, ws.ID, a.ID, adapter.NeighborsOpts{Direction: "out"})
		if err != nil {
			t.Fatalf("GraphNeighbors: %v", err)
		}
		if len(edges) != 1 || edges[0].TargetMemoryID != b.ID {
			t.Fatalf("expected one out-edge to B, got %+v", edges)
		}
	})

	t.Run("flip_recall_ready_if_all_embedded", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "recall-ready-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "recall content",
			WrittenByAgentID: "agent_opaque_c",
		}
		if _, err := s.ImprintMemory(ctx, mem); err != nil {
			t.Fatalf("imprint: %v", err)
		}
		// Two cells, neither embedded yet.
		cells := []types.Cell{
			{Seq: 0, Text: "a", TextMD5: types.MD5Hex("a"), WrittenByAgentID: "agent_opaque_c"},
			{Seq: 1, Text: "b", TextMD5: types.MD5Hex("b"), WrittenByAgentID: "agent_opaque_c"},
		}
		if err := s.UpsertCells(ctx, mem.ID, cells); err != nil {
			t.Fatalf("upsert cells: %v", err)
		}
		// No cells have a vector_key → no flip.
		flipped, err := s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (none embedded): %v", err)
		}
		if flipped {
			t.Fatal("flipped with zero cells embedded")
		}
		// Embed cell 0 only → still no flip.
		stored, _ := s.GetCells(ctx, mem.ID)
		if err := s.UpdateCellVectorKey(ctx, stored[0].CellID, stored[0].CellID, "test:model"); err != nil {
			t.Fatalf("update cell 0: %v", err)
		}
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (1 of 2): %v", err)
		}
		if flipped {
			t.Fatal("flipped with one cell still missing vector_key")
		}
		// Embed cell 1 → flip should succeed once.
		if err := s.UpdateCellVectorKey(ctx, stored[1].CellID, stored[1].CellID, "test:model"); err != nil {
			t.Fatalf("update cell 1: %v", err)
		}
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (all embedded): %v", err)
		}
		if !flipped {
			t.Fatal("expected flip after all cells embedded")
		}
		// Memory row reflects the flip.
		got, err := s.GetMemory(ctx, mem.ID)
		if err != nil {
			t.Fatalf("GetMemory: %v", err)
		}
		if !got.RecallReady {
			t.Fatal("Memory.RecallReady not persisted after flip")
		}
		// Idempotent — second call is a no-op.
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (idempotent): %v", err)
		}
		if flipped {
			t.Fatal("second flip mutated an already-ready memory")
		}
		// Memory with zero cells must never flip.
		empty := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "empty",
			WrittenByAgentID: "agent_opaque_c",
		}
		_, _ = s.ImprintMemory(ctx, empty)
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, empty.ID)
		if err != nil {
			t.Fatalf("flip (zero cells): %v", err)
		}
		if flipped {
			t.Fatal("flipped a memory with zero cells")
		}
	})

	t.Run("graph_unique_live_triple", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "uniq-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_c"}
		b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_c"}
		_, _ = s.ImprintMemory(ctx, a)
		_, _ = s.ImprintMemory(ctx, b)
		e := types.Edge{
			WorkspaceID:      ws.ID,
			SourceMemoryID:   a.ID,
			TargetMemoryID:   b.ID,
			EdgeType:         types.EdgeTypeReferences,
			CreatedByAgentID: "agent_opaque_c",
		}
		if _, err := s.GraphLink(ctx, e); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GraphLink(ctx, e); err == nil {
			t.Fatal("expected uniqueness violation on duplicate live triple")
		}
	})
}
