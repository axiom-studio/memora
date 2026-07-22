package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// openGraphAndMetadata returns a GraphStore and a MetadataStore backed
// by the same SQLite file. MetadataStore is needed to create memories
// (the graph endpoints must exist before linking).
func openGraphAndMetadata(t *testing.T) (*GraphStore, *Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	ctx := context.Background()

	primary := &Store{}
	if err := primary.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	gs := &GraphStore{}
	if err := gs.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: dsn}); err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { _ = gs.Close() })

	return gs, primary, ctx
}

func createTestWorkspaceAndMemories(t *testing.T, primary *Store, ctx context.Context, wsName string, n int) (string, []string) {
	t.Helper()
	ws := &types.Workspace{Name: wsName}
	if err := primary.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var ids []string
	for i := 0; i < n; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "content", WrittenByAgentID: "agent"}
		if _, err := primary.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("imprint: %v", err)
		}
		ids = append(ids, m.ID)
	}
	return ws.ID, ids
}

func TestGraphStore_LinkAndNeighbors(t *testing.T) {
	gs, primary, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, primary, ctx, "graph-link-test", 2)

	e, err := gs.Link(ctx, types.Edge{
		WorkspaceID:      wsID,
		SourceMemoryID:   mems[0],
		TargetMemoryID:   mems[1],
		EdgeType:         types.EdgeTypeReferences,
		CreatedByAgentID: "agent",
	})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if e.EdgeID == "" {
		t.Fatal("EdgeID should be populated")
	}

	edges, headers, err := gs.Neighbors(ctx, wsID, mems[0], adapter.NeighborsOpts{Direction: api.GraphDirOut})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if len(headers) != 1 || headers[0].MemoryID != mems[1] {
		t.Fatalf("expected neighbor %s, got %+v", mems[1], headers)
	}
}

func TestGraphStore_UnlinkAndCascade(t *testing.T) {
	gs, primary, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, primary, ctx, "graph-unlink-test", 3)

	e1, _ := gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[0], TargetMemoryID: mems[1], EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent"})
	_, _ = gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[1], TargetMemoryID: mems[2], EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent"})

	if err := gs.Unlink(ctx, e1.EdgeID, "agent"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}

	edges, _, _ := gs.Neighbors(ctx, wsID, mems[0], adapter.NeighborsOpts{Direction: api.GraphDirOut})
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges after unlink, got %d", len(edges))
	}

	n, err := gs.CascadeForget(ctx, mems[1], "agent")
	if err != nil {
		t.Fatalf("CascadeForget: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cascaded edge, got %d", n)
	}
}

func TestGraphStore_Traverse(t *testing.T) {
	gs, primary, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, primary, ctx, "graph-traverse-test", 3)

	_, _ = gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[0], TargetMemoryID: mems[1], EdgeType: types.EdgeTypeDerivedFrom, CreatedByAgentID: "agent"})
	_, _ = gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[1], TargetMemoryID: mems[2], EdgeType: types.EdgeTypeDerivedFrom, CreatedByAgentID: "agent"})

	tr, err := gs.Traverse(ctx, wsID, mems[0], adapter.TraverseOpts{Depth: 2, Direction: api.GraphDirOut})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if tr.Seed.MemoryID != mems[0] {
		t.Fatalf("seed = %s, want %s", tr.Seed.MemoryID, mems[0])
	}
	if len(tr.Layers) < 2 {
		t.Fatalf("expected 2 layers, got %d", len(tr.Layers))
	}
}

func TestGraphStore_DepthCap(t *testing.T) {
	gs, _, _ := openGraphAndMetadata(t)
	ctx := context.Background()
	_, err := gs.Traverse(ctx, "ws", "mem", adapter.TraverseOpts{Depth: 5})
	if err == nil {
		t.Fatal("expected depth cap error")
	}
}

func TestGraphStore_Stats(t *testing.T) {
	gs, primary, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, primary, ctx, "graph-stats-test", 2)

	_, _ = gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[0], TargetMemoryID: mems[1], EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent"})

	nodeCount, byType, err := gs.Stats(ctx, wsID)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if nodeCount != 2 {
		t.Fatalf("nodeCount = %d, want 2", nodeCount)
	}
	if byType["references"] != 1 {
		t.Fatalf("references count = %d, want 1", byType["references"])
	}
}

func TestGraphStore_Capabilities(t *testing.T) {
	gs := &GraphStore{}
	caps := gs.Capabilities()
	if caps.MaxDepth != 3 {
		t.Fatalf("MaxDepth = %d, want 3", caps.MaxDepth)
	}
	if caps.MaxLinkBatchSize != 1000 {
		t.Fatalf("MaxLinkBatchSize = %d, want 1000", caps.MaxLinkBatchSize)
	}
}
