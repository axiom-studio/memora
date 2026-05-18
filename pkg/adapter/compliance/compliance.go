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
