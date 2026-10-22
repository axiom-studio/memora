package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestWrites_RejectNonUUIDAgent (#4378): every service write checks its
// agent, so an internal caller that is not behind the HTTP or MCP
// boundary cannot record "ui-admin" in last_modified_by, history, the
// graph or the ledger. Pins check their creator in the store.
func TestWrites_RejectNonUUIDAgent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := newTestServiceIn(t, dir)
	graph := &storesqlite.GraphStore{}
	if err := graph.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	s.Graph = graph
	ws := newTestWorkspace(t, s)
	a := imprint(t, s, ws, "first")
	b := imprint(t, s, ws, "second")
	edge, err := s.GraphLink(ctx, types.Edge{WorkspaceID: ws, SourceMemoryID: a, TargetMemoryID: b, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: testAgent})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Metadata.GetMemory(ctx, a)
	if err != nil {
		t.Fatal(err)
	}

	const bad = "ui-admin"
	writes := map[string]func() error{
		"imprint": func() error {
			_, err := s.Imprint(ctx, ws, bad, api.ImprintRequest{Content: "x"})
			return err
		},
		"update": func() error {
			_, err := s.Update(ctx, ws, a, bad, "", api.UpdateRequest{Content: "y", ExpectedWatermark: m.HeadWatermark})
			return err
		},
		"patch": func() error {
			_, err := s.Patch(ctx, ws, a, bad, "", api.PatchRequest{Patch: []api.PatchOp{{OldString: "first", NewString: "1st"}}, ExpectedWatermark: m.HeadWatermark})
			return err
		},
		"append": func() error {
			_, err := s.Append(ctx, ws, a, bad, "", api.AppendRequest{Content: " z", ExpectedWatermark: m.HeadWatermark})
			return err
		},
		"forget": func() error {
			_, err := s.Forget(ctx, ws, b, bad)
			return err
		},
		"unlink": func() error { return s.GraphUnlink(ctx, ws, edge.EdgeID, bad) },
		"cascade forget": func() error {
			_, err := s.GraphCascadeForget(ctx, ws, a, bad)
			return err
		},
		"link": func() error {
			_, err := s.GraphLink(ctx, types.Edge{WorkspaceID: ws, SourceMemoryID: b, TargetMemoryID: a, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: bad})
			return err
		},
		"pin create": func() error {
			return s.Metadata.CreatePin(ctx, &types.Pin{WorkspaceID: ws, Query: "q", CreatedBy: bad})
		},
	}
	for name, write := range writes {
		err := write()
		if !errors.Is(err, types.ErrInvalidInput) || !strings.Contains(err.Error(), "must be a UUID") {
			t.Errorf("%s with agent %q: err = %v, want ErrInvalidInput naming the agent", name, bad, err)
		}
	}

	// Nothing was written as "ui-admin".
	got, err := s.Metadata.GetMemory(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastModifiedByAgentID == bad || got.HeadWatermark != m.HeadWatermark {
		t.Fatalf("a refused write changed the memory: %+v", got)
	}
	if _, err := s.Metadata.GetMemory(ctx, b); err != nil {
		t.Fatalf("a refused forget removed the memory: %v", err)
	}
}
