package autolink

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// --- mock metadata ---

type mockMetadata struct {
	adapter.MetadataStore
	ws *types.Workspace
}

func (m *mockMetadata) GetWorkspace(_ context.Context, _ string) (*types.Workspace, error) {
	if m.ws == nil {
		return nil, types.ErrNotFound
	}
	cp := *m.ws
	return &cp, nil
}

// --- mock vector ---

type mockVector struct {
	adapter.VectorStore
	hits []adapter.VectorHit
	err  error
}

func (m *mockVector) Query(_ context.Context, _ adapter.VectorQuery) ([]adapter.VectorHit, error) {
	return m.hits, m.err
}

// --- mock graph ---

type mockGraph struct {
	adapter.GraphStore
	batchResults  []adapter.LinkResult
	batchErr      error
	neighborEdges []types.Edge
	neighborErr   error
	linked        []types.Edge
}

func (m *mockGraph) LinkBatch(_ context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	m.linked = edges
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	if m.batchResults != nil {
		return m.batchResults, nil
	}
	results := make([]adapter.LinkResult, len(edges))
	for i, e := range edges {
		results[i] = adapter.LinkResult{Index: i, Status: "ok", EdgeID: e.EdgeID}
	}
	return results, nil
}

func (m *mockGraph) Neighbors(_ context.Context, _, _ string, _ adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
	return m.neighborEdges, nil, m.neighborErr
}

// --- mock ledger ---

type mockLedger struct {
	adapter.LedgerStore
	entries []api.LedgerEntry
}

func (m *mockLedger) Append(_ context.Context, e api.LedgerEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

// --- helpers ---

func testDeps(ws *types.Workspace, vec adapter.VectorStore, graph adapter.GraphStore, ledger adapter.LedgerStore) Deps {
	return Deps{
		Metadata: &mockMetadata{ws: ws},
		Vector:   vec,
		Graph:    graph,
		Ledger:   ledger,
		Logger:   slog.Default(),
	}
}

func enabledWorkspace() *types.Workspace {
	return &types.Workspace{
		ID:                      "ws_test",
		AutoLinkEnabled:         true,
		AutoLinkThreshold:       0.7,
		AutoLinkMaxEdges:        10,
		AutoLinkMaxIncomingPerDay: 100,
	}
}

func testMemory() *types.Memory {
	return &types.Memory{ID: "mem_new", WorkspaceID: "ws_test"}
}

func testCells() []types.Cell {
	return []types.Cell{{CellID: "cell_1", MemoryID: "mem_new"}}
}

func testEmbeddings() [][]float32 {
	return [][]float32{{0.1, 0.2, 0.3}}
}

// --- tests ---

func TestAutoLink_DisabledWorkspace(t *testing.T) {
	ws := enabledWorkspace()
	ws.AutoLinkEnabled = false
	edges, err := AutoLink(context.Background(), testDeps(ws, &mockVector{}, &mockGraph{}, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
}

func TestAutoLink_HappyPath(t *testing.T) {
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_a", CellID: "cell_a"}, Score: 0.9},
		{Key: adapter.VectorKey{MemoryID: "mem_b", CellID: "cell_b"}, Score: 0.8},
	}}
	graph := &mockGraph{}
	ledger := &mockLedger{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, ledger),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	for _, e := range edges {
		if e.EdgeType != types.EdgeTypeVectorNeighbor {
			t.Errorf("wrong edge type: %s", e.EdgeType)
		}
		if e.SourceMemoryID != "mem_new" {
			t.Errorf("wrong source: %s", e.SourceMemoryID)
		}
		props := e.PropertiesJSON
		if props["auto_source"] != "vector_neighbor" {
			t.Error("missing auto_source in properties")
		}
		if props["embedding_model"] != "model-1" {
			t.Error("wrong embedding_model in properties")
		}
		if props["cosine_score"] == nil {
			t.Error("missing cosine_score in properties")
		}
		pair, ok := props["via_cell_pair"].([]string)
		if !ok || len(pair) != 2 {
			t.Error("via_cell_pair should be a 2-element string slice")
		}
	}
	if len(ledger.entries) != 2 {
		t.Fatalf("expected 2 ledger entries, got %d", len(ledger.entries))
	}
	for _, le := range ledger.entries {
		if le.Op != "graph_link" {
			t.Errorf("expected op graph_link, got %s", le.Op)
		}
		if le.Metadata["trigger"] != "auto_link" {
			t.Error("ledger entry missing trigger: auto_link")
		}
	}
}

func TestAutoLink_SelfExclusion(t *testing.T) {
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_new", CellID: "cell_self"}, Score: 0.99},
		{Key: adapter.VectorKey{MemoryID: "mem_other", CellID: "cell_o"}, Score: 0.8},
	}}
	graph := &mockGraph{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (self excluded), got %d", len(edges))
	}
	if edges[0].TargetMemoryID != "mem_other" {
		t.Errorf("expected target mem_other, got %s", edges[0].TargetMemoryID)
	}
}

func TestAutoLink_ThresholdFiltering(t *testing.T) {
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_a", CellID: "ca"}, Score: 0.9},
		{Key: adapter.VectorKey{MemoryID: "mem_b", CellID: "cb"}, Score: 0.5},
		{Key: adapter.VectorKey{MemoryID: "mem_c", CellID: "cc"}, Score: 0.69},
	}}
	graph := &mockGraph{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (only >=0.7), got %d", len(edges))
	}
	if edges[0].TargetMemoryID != "mem_a" {
		t.Errorf("expected target mem_a, got %s", edges[0].TargetMemoryID)
	}
}

func TestAutoLink_MaxEdgesCap(t *testing.T) {
	ws := enabledWorkspace()
	ws.AutoLinkMaxEdges = 2
	hits := make([]adapter.VectorHit, 5)
	for i := range hits {
		hits[i] = adapter.VectorHit{
			Key:   adapter.VectorKey{MemoryID: "mem_" + string(rune('a'+i)), CellID: "c"},
			Score: 0.9 - float64(i)*0.01,
		}
	}
	vec := &mockVector{hits: hits}
	graph := &mockGraph{}
	edges, err := AutoLink(context.Background(), testDeps(ws, vec, graph, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges (capped), got %d", len(edges))
	}
}

func TestAutoLink_MemoryLevelDedup(t *testing.T) {
	cells := []types.Cell{
		{CellID: "cell_1", MemoryID: "mem_new"},
		{CellID: "cell_2", MemoryID: "mem_new"},
	}
	embeddings := [][]float32{{0.1}, {0.2}}
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_target", CellID: "ct1"}, Score: 0.8},
		{Key: adapter.VectorKey{MemoryID: "mem_target", CellID: "ct2"}, Score: 0.9},
	}}
	graph := &mockGraph{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, &mockLedger{}),
		"ws_test", testMemory(), cells, embeddings, "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (deduped at memory level), got %d", len(edges))
	}
	score := edges[0].PropertiesJSON["cosine_score"].(float64)
	if score != 0.9 {
		t.Errorf("expected best score 0.9, got %f", score)
	}
}

func TestAutoLink_VectorQueryError_LedgerSkip(t *testing.T) {
	vec := &mockVector{err: errors.New("timeout")}
	ledger := &mockLedger{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, &mockGraph{}, ledger),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges on error, got %d", len(edges))
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("expected 1 ledger skip entry, got %d", len(ledger.entries))
	}
	if ledger.entries[0].Op != "auto_link_skipped" {
		t.Errorf("expected op auto_link_skipped, got %s", ledger.entries[0].Op)
	}
}

func TestAutoLink_GraphBatchError_NoEdges(t *testing.T) {
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_a", CellID: "ca"}, Score: 0.9},
	}}
	graph := &mockGraph{batchErr: errors.New("db down")}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges on graph error, got %d", len(edges))
	}
}

func TestAutoLink_PerTargetThrottle(t *testing.T) {
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_popular", CellID: "cp"}, Score: 0.95},
		{Key: adapter.VectorKey{MemoryID: "mem_fresh", CellID: "cf"}, Score: 0.85},
	}}
	ws := enabledWorkspace()
	ws.AutoLinkMaxIncomingPerDay = 2
	recentEdges := make([]types.Edge, 2)
	for i := range recentEdges {
		recentEdges[i] = types.Edge{
			EdgeType:  types.EdgeTypeVectorNeighbor,
			CreatedAt: time.Now().UTC().Add(-1 * time.Hour),
		}
	}
	callCount := 0
	graph := &mockGraph{}
	origNeighbors := graph.Neighbors
	_ = origNeighbors
	graph2 := &throttleGraph{
		mockGraph:     mockGraph{},
		recentByMemID: map[string][]types.Edge{"mem_popular": recentEdges},
	}
	ledger := &mockLedger{}
	edges, err := AutoLink(context.Background(), testDeps(ws, vec, graph2, ledger),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = callCount
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (popular throttled), got %d", len(edges))
	}
	if edges[0].TargetMemoryID != "mem_fresh" {
		t.Errorf("expected mem_fresh, got %s", edges[0].TargetMemoryID)
	}
}

type throttleGraph struct {
	mockGraph
	recentByMemID map[string][]types.Edge
}

func (g *throttleGraph) Neighbors(_ context.Context, _, memID string, _ adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
	return g.recentByMemID[memID], nil, nil
}

func (g *throttleGraph) LinkBatch(ctx context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	return g.mockGraph.LinkBatch(ctx, edges)
}

func TestAutoLink_NilEmbeddings_Skipped(t *testing.T) {
	embeddings := [][]float32{nil, {0.1, 0.2}}
	cells := []types.Cell{
		{CellID: "cell_1", MemoryID: "mem_new"},
		{CellID: "cell_2", MemoryID: "mem_new"},
	}
	vec := &mockVector{hits: []adapter.VectorHit{
		{Key: adapter.VectorKey{MemoryID: "mem_a", CellID: "ca"}, Score: 0.9},
	}}
	graph := &mockGraph{}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, graph, &mockLedger{}),
		"ws_test", testMemory(), cells, embeddings, "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (nil embedding skipped), got %d", len(edges))
	}
}

func TestAutoLink_EmptyHits_NoEdges(t *testing.T) {
	vec := &mockVector{hits: nil}
	edges, err := AutoLink(context.Background(), testDeps(enabledWorkspace(), vec, &mockGraph{}, &mockLedger{}),
		"ws_test", testMemory(), testCells(), testEmbeddings(), "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
}
