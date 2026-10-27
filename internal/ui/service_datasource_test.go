package ui

import (
	"context"
	"path/filepath"
	"testing"

	ledgersqlite "github.com/axiom-studio/memora/internal/ledger/sqlite"
	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// realDataSource wires a ServiceDataSource to a real Service on SQLite,
// exactly as the server does, so console writes go through the same
// validation as every other client.
func realDataSource(t *testing.T) (*ServiceDataSource, *service.Service) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "memora.db")
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: dsn, Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	graph := &storesqlite.GraphStore{}
	if err := graph.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: dsn}); err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	led := &ledgersqlite.Store{}
	if err := led.Open(ctx, adapter.LedgerConfig{DSN: filepath.Join(filepath.Dir(dsn), "ledger.db")}); err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = led.Close() })
	emb, err := embedding.Open("hash:default")
	if err != nil {
		t.Fatalf("open embedding: %v", err)
	}
	svc := &service.Service{Metadata: meta, Vector: vec, Graph: graph, Ledger: led, Embedder: emb}
	return &ServiceDataSource{
		Metadata: meta, Vector: vec, Graph: graph, Ledger: led,
		RecallFunc: svc.Recall, ImprintFunc: svc.Imprint, UpdateFunc: svc.Update,
		PatchFunc: svc.Patch, AppendFunc: svc.Append, ForgetFunc: svc.Forget,
		UnlinkFunc: svc.GraphUnlink,
	}, svc
}

// TestServiceDataSource_ConsoleWritesSucceed drives every console write
// through the real data source and checks who each one is attributed
// to. Before ConsoleAgentID the console wrote as "ui-admin", which
// agent_id validation rejects (#4347 QA); with the service now checking
// the agent on every write (#4378), any console call site that sends a
// non-UUID agent fails here.
func TestServiceDataSource_ConsoleWritesSucceed(t *testing.T) {
	ds, svc := realDataSource(t)
	ctx := context.Background()
	ws := &types.Workspace{Name: "console"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	lastModifiedBy := func(id string) string {
		t.Helper()
		m, err := svc.Metadata.GetMemory(ctx, id)
		if err != nil {
			t.Fatalf("GetMemory: %v", err)
		}
		return m.LastModifiedByAgentID
	}
	ledgerAgent := func(op, target string) string {
		t.Helper()
		entries, _, err := svc.Ledger.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws.ID, Op: []string{op}})
		if err != nil {
			t.Fatalf("ledger: %v", err)
		}
		for _, e := range entries {
			if e.Target == target {
				return e.AgentID
			}
		}
		t.Fatalf("no %s ledger entry for %s", op, target)
		return ""
	}

	a, err := ds.ImprintMemory(ctx, ws.ID, api.ImprintRequest{Content: "first"})
	if err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	b, err := ds.ImprintMemory(ctx, ws.ID, api.ImprintRequest{Content: "second"})
	if err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	m, err := svc.Metadata.GetMemory(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m.WrittenByAgentID != types.ConsoleAgentID {
		t.Fatalf("imprint: written_by_agent_id = %q, want ConsoleAgentID", m.WrittenByAgentID)
	}

	up, err := ds.UpdateMemory(ctx, ws.ID, a.ID, api.UpdateRequest{Content: "first, edited", ExpectedWatermark: a.Watermark})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if got := lastModifiedBy(a.ID); got != types.ConsoleAgentID {
		t.Fatalf("update: last_modified_by = %q, want ConsoleAgentID", got)
	}
	if _, err := ds.PatchMemory(ctx, ws.ID, a.ID, api.PatchRequest{
		Patch: []api.PatchOp{{OldString: "edited", NewString: "patched"}}, ExpectedWatermark: up.Watermark,
	}); err != nil {
		t.Fatalf("PatchMemory: %v", err)
	}
	if got := lastModifiedBy(a.ID); got != types.ConsoleAgentID {
		t.Fatalf("patch: last_modified_by = %q, want ConsoleAgentID", got)
	}
	if _, err := ds.AppendMemory(ctx, ws.ID, b.ID, api.AppendRequest{Content: " more", ExpectedWatermark: b.Watermark}); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	if got := lastModifiedBy(b.ID); got != types.ConsoleAgentID {
		t.Fatalf("append: last_modified_by = %q, want ConsoleAgentID", got)
	}

	edgeID, err := ds.LinkEdge(ctx, ws.ID, a.ID, b.ID, string(types.EdgeTypeReferences))
	if err != nil {
		t.Fatalf("LinkEdge: %v", err)
	}
	edges, _, err := svc.Graph.Neighbors(ctx, ws.ID, a.ID, adapter.NeighborsOpts{Direction: api.GraphDirection("out")})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	var linkedBy string
	for _, e := range edges {
		if e.EdgeID == edgeID {
			linkedBy = e.CreatedByAgentID
		}
	}
	if linkedBy != types.ConsoleAgentID {
		t.Fatalf("link: created_by = %q, want ConsoleAgentID", linkedBy)
	}
	if err := ds.UnlinkEdge(ctx, ws.ID, edgeID); err != nil {
		t.Fatalf("UnlinkEdge: %v", err)
	}
	if got := ledgerAgent("unlink", edgeID); got != types.ConsoleAgentID {
		t.Fatalf("unlink: ledger agent_id = %q, want ConsoleAgentID", got)
	}
	if _, err := ds.ForgetMemory(ctx, ws.ID, b.ID); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
	if got := ledgerAgent("forget", b.ID); got != types.ConsoleAgentID {
		t.Fatalf("forget: ledger agent_id = %q, want ConsoleAgentID", got)
	}
}
