package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func openStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	s := &Store{}
	ctx := context.Background()
	if err := s.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, ctx
}

func TestImprintAndGet(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "test"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	m := &types.Memory{
		WorkspaceID:      ws.ID,
		Content:          "hello world",
		WrittenByAgentID: "agent_opaque_test",
	}
	wmk, err := s.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if wmk == "" {
		t.Fatal("watermark empty")
	}
	got, err := s.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" {
		t.Fatalf("content roundtrip: %q", got.Content)
	}
	if got.HeadWatermark != wmk {
		t.Fatalf("watermark mismatch: %s vs %s", got.HeadWatermark, wmk)
	}
}

func TestUpdateMemory_CAS(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "test"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "v1", WrittenByAgentID: "agent_opaque_x"}
	wmk, err := s.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	// Happy path.
	m.Content = "v2"
	m.LastModifiedByAgentID = "agent_opaque_x"
	newWmk, err := s.UpdateMemory(ctx, m.ID, wmk, m)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if newWmk == wmk {
		t.Fatal("watermark didn't bump")
	}
	// CAS conflict.
	_, err = s.UpdateMemory(ctx, m.ID, wmk, m)
	if err != types.ErrCAS {
		t.Fatalf("expected ErrCAS, got %v", err)
	}
}

func TestPatchMemory_AppliesOps(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "patch"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "alpha beta gamma", WrittenByAgentID: "agent_opaque_p"}
	wmk, _ := s.ImprintMemory(ctx, m)

	ops := []api.PatchOp{{OldString: "beta", NewString: "BETA"}}
	newWmk, _, content, err := s.PatchMemory(ctx, m.ID, wmk, ops, "agent_opaque_p")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if content != "alpha BETA gamma" {
		t.Fatalf("content: %q", content)
	}
	if newWmk == wmk {
		t.Fatal("watermark didn't bump")
	}
}

func TestPatchMemory_AmbiguousAnchorRejected(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "patch2"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "x x", WrittenByAgentID: "agent_opaque_p"}
	wmk, _ := s.ImprintMemory(ctx, m)
	_, _, _, err := s.PatchMemory(ctx, m.ID, wmk, []api.PatchOp{{OldString: "x", NewString: "y"}}, "agent_opaque_p")
	if err == nil {
		t.Fatal("expected ErrPatchAnchor on ambiguous")
	}
}

func TestGraphLink_AndNeighbors(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "graph"}
	_ = s.CreateWorkspace(ctx, ws)

	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_g"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_g"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)

	e := types.Edge{
		WorkspaceID:      ws.ID,
		SourceMemoryID:   a.ID,
		TargetMemoryID:   b.ID,
		EdgeType:         types.EdgeTypeReferences,
		CreatedByAgentID: "agent_opaque_g",
	}
	if _, err := s.GraphLink(ctx, e); err != nil {
		t.Fatalf("link: %v", err)
	}
	edges, headers, err := s.GraphNeighbors(ctx, ws.ID, a.ID, adapter.NeighborsOpts{Direction: "out"})
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetMemoryID != b.ID {
		t.Fatalf("expected one out-edge to B, got %+v", edges)
	}
	if len(headers) != 1 || headers[0].MemoryID != b.ID {
		t.Fatalf("expected B header, got %+v", headers)
	}
}

func TestGraphLink_UniqueLiveTriple(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "uniq"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_u"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_u"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	e := types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_u"}
	if _, err := s.GraphLink(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GraphLink(ctx, e); err == nil {
		t.Fatal("expected uniqueness violation on duplicate live triple")
	}
}

func TestGraphTraverse_DepthCap(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "depth"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_d"}
	_, _ = s.ImprintMemory(ctx, a)
	_, err := s.GraphTraverse(ctx, ws.ID, a.ID, adapter.TraverseOpts{Depth: 5})
	if err == nil {
		t.Fatal("expected ErrDepthExceeded on depth > 3")
	}
}

func TestGraphCascadeForget(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "cascade"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_c"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_c"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_c"})
	n, err := s.GraphCascadeForget(ctx, a.ID, "agent_opaque_c")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cascaded edge, got %d", n)
	}
}

func TestMigrationIdempotence(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	ctx := context.Background()

	s1 := &Store{}
	if err := s1.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("first open: %v", err)
	}
	ws := &types.Workspace{Name: "idempotent"}
	if err := s1.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := &Store{}
	if err := s2.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("second open (re-migrate): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("data lost after re-open: %v", err)
	}
	if got.Name != "idempotent" {
		t.Errorf("name = %q, want idempotent", got.Name)
	}
}

func TestCapabilities(t *testing.T) {
	s, _ := openStore(t)
	caps := s.Capabilities()
	if !caps.SupportsCAS {
		t.Error("SupportsCAS should be true")
	}
	if !caps.SupportsTransactions {
		t.Error("SupportsTransactions should be true")
	}
	if caps.MaxGraphDepth != 3 {
		t.Errorf("MaxGraphDepth = %d, want 3", caps.MaxGraphDepth)
	}
	if caps.MaxNeighborsK != 200 {
		t.Errorf("MaxNeighborsK = %d, want 200", caps.MaxNeighborsK)
	}
	if caps.MaxLinkBatchSize != 1000 {
		t.Errorf("MaxLinkBatchSize = %d, want 1000", caps.MaxLinkBatchSize)
	}
}
