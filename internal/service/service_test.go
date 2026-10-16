package service

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

const testAgent = "agent_opaque_service_test"

// newTestService wires the OSS-default sqlite metadata + sqlite-vec vector
// stores and the noop embedder; no test doubles.
func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "memora.db"), Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })

	graph := &storesqlite.GraphStore{}
	if err := graph.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { _ = graph.Close() })

	emb, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatalf("open embedding: %v", err)
	}
	return &Service{Metadata: meta, Vector: vec, Graph: graph, Embedder: emb}
}

func newTestWorkspace(t *testing.T, s *Service) string {
	t.Helper()
	ws := &types.Workspace{Name: "service-test"}
	if err := s.Metadata.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return ws.ID
}

func storedRecallReady(t *testing.T, s *Service, memoryID string) bool {
	t.Helper()
	m, err := s.Metadata.GetMemory(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("GetMemory(%s): %v", memoryID, err)
	}
	return m.RecallReady
}

// Issue #4379: the response said recall_ready=true but the stored row
// stayed false forever, because only the (never-started) async pool
// flipped it.
func TestImprint_PersistsRecallReady(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)

	resp, err := s.Imprint(context.Background(), ws, testAgent, api.ImprintRequest{Content: "first paragraph\n\nsecond paragraph"})
	if err != nil {
		t.Fatalf("Imprint: %v", err)
	}
	if !resp.RecallReady {
		t.Fatal("ImprintResponse.RecallReady = false, want true after a successful inline embed")
	}
	if !storedRecallReady(t, s, resp.MemoryID) {
		t.Fatal("stored recall_ready = false after imprint; want true (response and row disagree)")
	}
}

func TestUpdate_LeavesRecallReadyTrue(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	ctx := context.Background()

	imp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: "original"})
	if err != nil {
		t.Fatalf("Imprint: %v", err)
	}
	if _, err := s.Update(ctx, ws, imp.MemoryID, testAgent, imp.Watermark, api.UpdateRequest{Content: "rewritten\n\nwith a second cell", ExpectedWatermark: imp.Watermark}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !storedRecallReady(t, s, imp.MemoryID) {
		t.Fatal("stored recall_ready = false after update re-embedded every cell")
	}
}

// ---- pure helpers -------------------------------------------------------

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestKeywordScore(t *testing.T) {
	cases := []struct {
		name, content, q string
		want             float64
	}{
		{"empty content", "", "go", 0},
		{"empty query", "go", "", 0},
		{"no match", "rust", "go", 0},
		{"one match, short doc", "go", "go", 1 / (1 + 2.0/1000)},
		{"two matches", "go go", "go", 2 / (1 + 5.0/1000)},
		{"length dampens", "go" + string(make([]byte, 998)), "go", 1 / (1 + 1000.0/1000)},
	}
	for _, c := range cases {
		if got := keywordScore(c.content, c.q); !approx(got, c.want) {
			t.Errorf("%s: keywordScore = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPow(t *testing.T) {
	cases := []struct {
		base float64
		exp  int
		want float64
	}{{0.7, 0, 1}, {0.7, 1, 0.7}, {0.7, 2, 0.49}, {2, 10, 1024}, {0.7, -1, 1}}
	for _, c := range cases {
		if got := pow(c.base, c.exp); !approx(got, c.want) {
			t.Errorf("pow(%v, %d) = %v, want %v", c.base, c.exp, got, c.want)
		}
	}
}

func TestSortHits_DescendingByScore(t *testing.T) {
	h := []api.RecallHit{{MemoryID: "a", Score: 0.1}, {MemoryID: "b", Score: 0.9}, {MemoryID: "c", Score: 0.5}}
	sortHits(h)
	for i := 1; i < len(h); i++ {
		if h[i].Score > h[i-1].Score {
			t.Fatalf("not descending at %d: %+v", i, h)
		}
	}
	if h[0].MemoryID != "b" {
		t.Fatalf("top hit = %s, want b", h[0].MemoryID)
	}
	sortHits(nil) // must not panic
}

func scoresByID(h []api.RecallHit) map[string]float64 {
	m := make(map[string]float64, len(h))
	for _, x := range h {
		m[x.MemoryID] = x.Score
	}
	return m
}

// hybridMerge accumulates through a map, so tie order is random; these
// tests assert score values per memory, never relative order among ties.
func TestHybridMerge_DefaultWeightsCombineSources(t *testing.T) {
	vec := []api.RecallHit{{MemoryID: "both", Score: 1.0}, {MemoryID: "vec_only", Score: 0.5}}
	kw := []api.RecallHit{{MemoryID: "both", Score: 2.0, KeywordScore: 2.0}, {MemoryID: "kw_only", Score: 1.0, KeywordScore: 1.0}}

	out := hybridMerge(kw, vec, api.RecallWeights{}, 10)
	got := scoresByID(out)
	want := map[string]float64{"both": 0.7*1.0 + 0.3*2.0, "vec_only": 0.7 * 0.5, "kw_only": 0.3 * 1.0}
	if len(got) != len(want) {
		t.Fatalf("merged %d hits, want %d: %+v", len(got), len(want), out)
	}
	for id, w := range want {
		if !approx(got[id], w) {
			t.Errorf("%s score = %v, want %v", id, got[id], w)
		}
	}
	for _, h := range out {
		if h.MemoryID == "both" && h.KeywordScore != 2.0 {
			t.Errorf("both.KeywordScore = %v, want 2.0 carried from the keyword hit", h.KeywordScore)
		}
	}
}

func TestHybridMerge_ExplicitWeightsAndTruncation(t *testing.T) {
	vec := []api.RecallHit{{MemoryID: "a", Score: 1}, {MemoryID: "b", Score: 0.2}}
	kw := []api.RecallHit{{MemoryID: "c", Score: 1}}

	out := hybridMerge(kw, vec, api.RecallWeights{Vector: 1, Keyword: 0}, 2)
	if len(out) != 2 {
		t.Fatalf("len = %d, want truncation to k=2", len(out))
	}
	got := scoresByID(out)
	if !approx(got["a"], 1) || !approx(got["b"], 0.2) {
		t.Fatalf("scores = %v, want a=1 b=0.2 and keyword-weight-0 hit c dropped below them", got)
	}
}

func TestHybridMerge_EmptyInputs(t *testing.T) {
	if out := hybridMerge(nil, nil, api.RecallWeights{}, 5); len(out) != 0 {
		t.Fatalf("hybridMerge(nil, nil) = %+v, want empty", out)
	}
}

func TestPassesFilter(t *testing.T) {
	now := time.Now()
	m := types.Memory{CollectionID: "c1", WrittenByAgentID: "agent_a", CreatedAt: now, Tags: map[string]string{"team": "core"}}
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name string
		f    api.RecallFilters
		want bool
	}{
		{"no filters", api.RecallFilters{}, true},
		{"collection match", api.RecallFilters{CollectionID: "c1"}, true},
		{"collection mismatch", api.RecallFilters{CollectionID: "c2"}, false},
		{"agent match", api.RecallFilters{AgentID: "agent_a"}, true},
		{"agent mismatch", api.RecallFilters{AgentID: "agent_b"}, false},
		{"agent in", api.RecallFilters{AgentIDIn: []string{"agent_b", "agent_a"}}, true},
		{"agent not in list", api.RecallFilters{AgentIDIn: []string{"agent_b"}}, false},
		{"agent excluded", api.RecallFilters{AgentIDNotIn: []string{"agent_a"}}, false},
		{"after window", api.RecallFilters{TsAfter: &before}, true},
		{"before cutoff fails", api.RecallFilters{TsAfter: &after}, false},
		{"tsbefore fails", api.RecallFilters{TsBefore: &before}, false},
		{"tag any-of match", api.RecallFilters{Tags: map[string][]string{"team": {"x", "core"}}}, true},
		{"tag mismatch", api.RecallFilters{Tags: map[string][]string{"team": {"x"}}}, false},
		{"missing tag", api.RecallFilters{Tags: map[string][]string{"owner": {"core"}}}, false},
	}
	for _, c := range cases {
		if got := passesFilter(m, c.f); got != c.want {
			t.Errorf("%s: passesFilter = %v, want %v", c.name, got, c.want)
		}
	}
}

// ---- Recall end to end (real sqlite + sqlite-vec + noop embedder) -------

func imprint(t *testing.T, s *Service, ws, content string) string {
	t.Helper()
	off := false
	resp, err := s.Imprint(context.Background(), ws, testAgent, api.ImprintRequest{Content: content, AutoLink: &off})
	if err != nil {
		t.Fatalf("Imprint(%q): %v", content, err)
	}
	return resp.MemoryID
}

func recallIDs(r *api.RecallResponse) map[string]api.RecallHit {
	out := map[string]api.RecallHit{}
	for _, h := range r.Results {
		out[h.MemoryID] = h
	}
	return out
}

func TestRecall_KeywordMode_MatchesCaseInsensitiveSubstring(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	hit := imprint(t, s, ws, "Quarterly ROADMAP review")
	imprint(t, s, ws, "lunch menu")

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "roadmap", Mode: api.RecallModeKeyword})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	got := recallIDs(r)
	if len(got) != 1 || got[hit].Via != "seed" || got[hit].KeywordScore <= 0 {
		t.Fatalf("keyword results = %+v, want only %s as a scored seed", r.Results, hit)
	}
}

func TestRecall_VectorMode_RanksIdenticalTextFirst(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	exact := imprint(t, s, ws, "vector target text")
	imprint(t, s, ws, "something entirely different here")

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "vector target text", Mode: api.RecallModeVector, K: 2})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Results) == 0 || r.Results[0].MemoryID != exact || !approx(r.Results[0].VectorScore, 1) {
		t.Fatalf("vector results = %+v, want %s first with cosine 1 (noop embedder is deterministic)", r.Results, exact)
	}
}

func TestRecall_HybridIsDefaultMode(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, "hybrid needle")

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "hybrid needle"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	h, ok := recallIDs(r)[id]
	if !ok {
		t.Fatalf("hybrid results = %+v, want %s", r.Results, id)
	}
	// Found by both sources: 0.7*vector + 0.3*keyword.
	if want := 0.7*h.VectorScore + 0.3*h.KeywordScore; !approx(h.Score, want) || h.KeywordScore == 0 || h.VectorScore == 0 {
		t.Fatalf("hybrid hit = %+v, want score %v from both sources", h, want)
	}
}

func TestRecall_LookupMode_SkipsUnknownIDs(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, "lookup me")

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Mode: api.RecallModeLookup, MemoryIDs: []string{"mem_missing", id}})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Results) != 1 || r.Results[0].MemoryID != id || r.Results[0].Text != "lookup me" {
		t.Fatalf("lookup results = %+v, want only %s with full content", r.Results, id)
	}
}

func TestRecall_UnknownMode_ReturnsInvalidInput(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	if _, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "x", Mode: "semantic"}); !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRecall_DefaultKIsFive(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	for i := 0; i < 7; i++ {
		imprint(t, s, ws, "repeated keyword entry")
	}
	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "keyword", Mode: api.RecallModeKeyword})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Results) != 5 {
		t.Fatalf("len(results) = %d, want default k=5", len(r.Results))
	}
}

func TestRecall_GraphExpansion_AddsDecayedNeighbor(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	ctx := context.Background()
	seed := imprint(t, s, ws, "seed document about graphs")
	neighbor := imprint(t, s, ws, "unrelated neighbor")
	if _, err := s.GraphLink(ctx, types.Edge{
		WorkspaceID: ws, SourceMemoryID: seed, TargetMemoryID: neighbor,
		EdgeType: types.EdgeType("references"), CreatedByAgentID: testAgent,
	}); err != nil {
		t.Fatalf("GraphLink: %v", err)
	}

	r, err := s.Recall(ctx, ws, api.RecallRequest{
		Query: "graphs", Mode: api.RecallModeKeyword,
		GraphExpansion: &api.GraphExpansion{Depth: 1, MaxNeighbors: 5},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	got := recallIDs(r)
	sh, nh := got[seed], got[neighbor]
	if sh.Via != "seed" || nh.Via != "graph" || nh.GraphProvenance == nil || nh.GraphProvenance.FromMemoryID != seed {
		t.Fatalf("results = %+v, want seed plus neighbor reached via graph from the seed", r.Results)
	}
	// weight (default 0.2) * decay^layer * seed score.
	if want := 0.2 * pow(0.7, nh.GraphProvenance.Layer) * sh.Score; !approx(nh.Score, want) {
		t.Fatalf("neighbor score = %v, want %v (layer %d)", nh.Score, want, nh.GraphProvenance.Layer)
	}
	if r.GraphNodesExpanded != 1 {
		t.Fatalf("GraphNodesExpanded = %d, want 1", r.GraphNodesExpanded)
	}
}

func TestRecall_GraphExpansion_WithoutGraphStoreReturnsSeedsOnly(t *testing.T) {
	s := newTestService(t)
	s.Graph = nil
	ws := newTestWorkspace(t, s)
	imprint(t, s, ws, "seed only")

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{
		Query: "seed", Mode: api.RecallModeKeyword, GraphExpansion: &api.GraphExpansion{Depth: 1},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Results) != 1 || r.GraphNodesExpanded != 0 {
		t.Fatalf("results = %+v expanded=%d, want the seed alone", r.Results, r.GraphNodesExpanded)
	}
}
