package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// withVibeflowArchive adds a vibeflow_contexts table holding one archive
// row (source -> parent) and rebuilds the shim view, as Open does on a
// database that has the table.
func withVibeflowArchive(t *testing.T, s *Store, ctx context.Context, wsID, source, parent string) {
	t.Helper()
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE vibeflow_contexts (id TEXT PRIMARY KEY, project_id TEXT, parent_context_id TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create vibeflow_contexts: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vibeflow_contexts VALUES (?, ?, ?, '2026-10-21T00:00:00Z')`, source, wsID, parent); err != nil {
		t.Fatalf("insert archive row: %v", err)
	}
	if err := s.ensureLegacyShimView(ctx); err != nil {
		t.Fatalf("ensureLegacyShimView: %v", err)
	}
}

func syntheticEdgeID(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	var id string
	if err := s.db.QueryRowContext(ctx,
		`SELECT edge_id FROM memora_edges_view WHERE is_synthetic = 1`).Scan(&id); err != nil {
		t.Fatalf("read synthetic edge: %v", err)
	}
	return id
}

// TestLegacyShim_SyntheticEdgeIDIsPersistedUUID: the synthetic edge gets
// a UUIDv4 that survives rebuilding the view (a restart), and both graph
// stores refuse to unlink it or re-link parent_of over it, keyed on
// is_synthetic rather than an ID prefix.
func TestLegacyShim_SyntheticEdgeIDIsPersistedUUID(t *testing.T) {
	gs, s, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, s, ctx, "shim", 2)
	withVibeflowArchive(t, s, ctx, wsID, mems[0], mems[1])

	id := syntheticEdgeID(t, s, ctx)
	if _, err := types.ParseID("edge_id", id); err != nil {
		t.Fatalf("synthetic edge_id: %v", err)
	}
	if err := s.ensureLegacyShimView(ctx); err != nil {
		t.Fatalf("rebuild view: %v", err)
	}
	if again := syntheticEdgeID(t, s, ctx); again != id {
		t.Fatalf("synthetic edge_id changed across rebuild: %q then %q", id, again)
	}

	agent := types.NewID()
	if err := s.GraphUnlink(ctx, wsID, id, agent); !errors.Is(err, types.ErrSyntheticEdge) {
		t.Errorf("Store.GraphUnlink(synthetic) = %v, want ErrSyntheticEdge", err)
	}
	if err := gs.Unlink(ctx, wsID, id, agent); !errors.Is(err, types.ErrSyntheticEdge) {
		t.Errorf("GraphStore.Unlink(synthetic) = %v, want ErrSyntheticEdge", err)
	}
	if err := gs.Unlink(ctx, types.NewID(), id, agent); errors.Is(err, types.ErrSyntheticEdge) {
		t.Error("the synthetic guard matched an edge from another workspace")
	}

	parentOf := types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[0], TargetMemoryID: mems[1],
		EdgeType: types.EdgeTypeParentOf, CreatedByAgentID: agent}
	if _, err := s.GraphLink(ctx, parentOf); !errors.Is(err, types.ErrAlreadyLinked) {
		t.Errorf("Store.GraphLink(parent_of over synthetic) = %v, want ErrAlreadyLinked", err)
	}
	if _, err := gs.Link(ctx, parentOf); !errors.Is(err, types.ErrAlreadyLinked) {
		t.Errorf("GraphStore.Link(parent_of over synthetic) = %v, want ErrAlreadyLinked", err)
	}
}

// TestLegacyShim_NativeEdgesUnaffected: a native edge in the same
// workspace is not synthetic and unlinks normally.
func TestLegacyShim_NativeEdgesUnaffected(t *testing.T) {
	gs, s, ctx := openGraphAndMetadata(t)
	wsID, mems := createTestWorkspaceAndMemories(t, s, ctx, "shim-native", 3)
	withVibeflowArchive(t, s, ctx, wsID, mems[0], mems[1])

	agent := types.NewID()
	e, err := gs.Link(ctx, types.Edge{WorkspaceID: wsID, SourceMemoryID: mems[1], TargetMemoryID: mems[2],
		EdgeType: types.EdgeTypeReferences, CreatedByAgentID: agent})
	if err != nil {
		t.Fatalf("link native edge: %v", err)
	}
	if isSyntheticEdge(ctx, s.db, wsID, e.EdgeID) {
		t.Fatal("native edge reported as synthetic")
	}
	if err := gs.Unlink(ctx, wsID, e.EdgeID, agent); err != nil {
		t.Fatalf("unlink native edge: %v", err)
	}
}
