package ui

import (
	"context"
	"path/filepath"
	"testing"

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
	emb, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatalf("open embedding: %v", err)
	}
	svc := &service.Service{Metadata: meta, Vector: vec, Graph: graph, Embedder: emb}
	return &ServiceDataSource{
		Metadata: meta, Vector: vec, Graph: graph,
		RecallFunc: svc.Recall, ImprintFunc: svc.Imprint, UpdateFunc: svc.Update,
		PatchFunc: svc.Patch, AppendFunc: svc.Append, ForgetFunc: svc.Forget,
	}, svc
}

// TestServiceDataSource_ConsoleWritesSucceed drives every console write
// through the real data source. Before ConsoleAgentID the console wrote
// as "ui-admin", which agent_id validation rejects, so each of these
// failed (#4347 QA).
func TestServiceDataSource_ConsoleWritesSucceed(t *testing.T) {
	ds, svc := realDataSource(t)
	ctx := context.Background()
	ws := &types.Workspace{Name: "console"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	a, err := ds.ImprintMemory(ctx, ws.ID, api.ImprintRequest{Content: "first"})
	if err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	b, err := ds.ImprintMemory(ctx, ws.ID, api.ImprintRequest{Content: "second"})
	if err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	if _, err := ds.UpdateMemory(ctx, ws.ID, a.ID, api.UpdateRequest{Content: "first, edited", ExpectedWatermark: a.Watermark}); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if _, err := ds.AppendMemory(ctx, ws.ID, b.ID, api.AppendRequest{Content: " more", ExpectedWatermark: b.Watermark}); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	edgeID, err := ds.LinkEdge(ctx, ws.ID, a.ID, b.ID, string(types.EdgeTypeReferences))
	if err != nil {
		t.Fatalf("LinkEdge: %v", err)
	}
	if err := ds.UnlinkEdge(ctx, ws.ID, edgeID); err != nil {
		t.Fatalf("UnlinkEdge: %v", err)
	}

	m, err := svc.Metadata.GetMemory(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m.WrittenByAgentID != types.ConsoleAgentID {
		t.Fatalf("written_by_agent_id = %q, want ConsoleAgentID %q", m.WrittenByAgentID, types.ConsoleAgentID)
	}
	if _, err := ds.ForgetMemory(ctx, ws.ID, b.ID); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
}
