package service

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestVectorSearch_OneHitPerMemoryWithItsBestCell: a memory whose cells
// all match yields one hit, carrying the cell the vector store ranked
// first for that memory, not one hit per cell or the last cell seen.
func TestVectorSearch_OneHitPerMemoryWithItsBestCell(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	para := func(word string) string { return strings.Repeat(word+" ", 600) }
	id := imprint(t, s, ws, para("alpha")+"\n\n"+para("bravo")+"\n\n"+para("charlie"))
	query := para("bravo")

	emb, err := s.Embedder.Embed(context.Background(), []string{query})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Vector.Query(context.Background(), adapter.VectorQuery{WorkspaceID: ws, Embedding: emb[0], K: 50})
	if err != nil || len(raw) < 2 {
		t.Fatalf("want several matching cells, got %d (%v)", len(raw), err)
	}

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: query, Mode: api.RecallModeVector, K: 5})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Results) != 1 || r.Results[0].MemoryID != id || r.Results[0].CellID != raw[0].Key.CellID {
		t.Fatalf("vector results = %d hits (first cell %s), want one hit for %s carrying the top cell %s",
			len(r.Results), r.Results[0].CellID, id, raw[0].Key.CellID)
	}
}

// TestHybrid_VectorDown_ReportsDegradedAndKeepsKeywordHits uses a real,
// closed vector store.
func TestHybrid_VectorDown_ReportsDegradedAndKeepsKeywordHits(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, "lighthouse keeper diary")
	if err := s.Vector.Close(); err != nil {
		t.Fatal(err)
	}

	for name, run := range map[string]func() (*api.RecallResponse, error){
		"recall": func() (*api.RecallResponse, error) {
			return s.Recall(context.Background(), ws, api.RecallRequest{Query: "lighthouse keeper"})
		},
		"search": func() (*api.RecallResponse, error) {
			return s.Search(context.Background(), api.SearchRequest{Query: "lighthouse keeper"})
		},
	} {
		r, err := run()
		if err != nil {
			t.Fatalf("%s: hybrid with the vector side down should degrade, got %v", name, err)
		}
		if len(r.Results) != 1 || r.Results[0].MemoryID != id || len(r.Degraded) != 1 || r.Degraded[0] != api.DegradedVector {
			t.Fatalf("%s: results=%+v degraded=%v, want %s and [%s]", name, r.Results, r.Degraded, id, api.DegradedVector)
		}
	}
	if _, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "lighthouse", Mode: api.RecallModeVector}); err == nil {
		t.Fatal("vector mode with the vector store down must fail, not return empty results")
	}
	s.Embedder = nil
	if _, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "lighthouse", Mode: api.RecallModeVector}); !errors.Is(err, types.ErrCapability) {
		t.Fatalf("vector mode with no embedder: err = %v, want ErrCapability", err)
	}
}

// TestHybrid_KeywordDown_ReportsDegradedAndKeepsVectorHits drops the
// real FTS index; both sides down is an error.
func TestHybrid_KeywordDown_ReportsDegradedAndKeepsVectorHits(t *testing.T) {
	dir := t.TempDir()
	s := newTestServiceIn(t, dir)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, "lighthouse keeper diary")

	db, err := sql.Open("sqlite", filepath.Join(dir, "memora.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE memora_memories_fts`); err != nil {
		t.Fatalf("drop fts: %v", err)
	}
	_ = db.Close()

	r, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "lighthouse keeper diary"})
	if err != nil {
		t.Fatalf("hybrid with the keyword side down should degrade, got %v", err)
	}
	if len(r.Results) != 1 || r.Results[0].MemoryID != id || len(r.Degraded) != 1 || r.Degraded[0] != api.DegradedKeyword {
		t.Fatalf("results=%+v degraded=%v, want %s and [%s]", r.Results, r.Degraded, id, api.DegradedKeyword)
	}

	if err := s.Vector.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recall(context.Background(), ws, api.RecallRequest{Query: "lighthouse"}); err == nil {
		t.Fatal("hybrid with both sides down must fail")
	}
}

// TestHybrid_TopKIsPrefixStableAndBothSidesWin (#4356 QA): hybrid fuses
// candidate pools larger than k, so top-k is a prefix of top-20, and a
// memory ranked by both sides outranks one that only the vector side
// ranks (the noop embedder ranks every memory), even at k=1..3.
func TestHybrid_TopKIsPrefixStableAndBothSidesWin(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	keywordMatches := map[string]bool{}
	for _, c := range []string{
		"NVDA dropped on export controls", "Chip exports and NVDA guidance cut", "NVDA",
		"exports exports exports of grain", "Quarterly NVDA exports review meeting notes",
		"NVDA supplier exports", "export data for NVDA", "NVDA exports outlook", "exports up, NVDA flat",
		"NVDA and AMD exports", "another NVDA note",
	} {
		keywordMatches[imprint(t, s, ws, c)] = true
	}
	for _, c := range []string{"Lunch menu for Tuesday", "Parking rota", "Team offsite photos"} {
		imprint(t, s, ws, c)
	}

	ids := func(k int) []string {
		t.Helper()
		r, err := s.Search(context.Background(), api.SearchRequest{Query: "NVDA exports", K: k})
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		out := make([]string, len(r.Results))
		for i, h := range r.Results {
			out[i] = h.MemoryID
		}
		return out
	}
	full := ids(20)
	for k := 1; k <= 5; k++ {
		top := ids(k)
		for i := range top {
			if top[i] != full[i] {
				t.Fatalf("k=%d: position %d is %s, but top-20 has %s there (not prefix-stable)", k, i, top[i], full[i])
			}
		}
	}
	for i, id := range full[:len(keywordMatches)] {
		if !keywordMatches[id] {
			t.Fatalf("position %d is a vector-only memory, ranked above a memory both sides rank", i)
		}
	}
}

// TestNoopEmbedder_VectorUnavailable (S-6): the noop embedder's vectors
// are noise, so vector mode refuses with ErrCapability and hybrid falls
// back to keyword, reporting vector_unavailable.
func TestNoopEmbedder_VectorUnavailable(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, "noop keeps keyword search alive")
	noop, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatal(err)
	}
	s.Embedder = noop

	_, err = s.Search(context.Background(), api.SearchRequest{Query: "keyword search", Mode: api.RecallModeVector})
	if !errors.Is(err, types.ErrCapability) || !strings.Contains(err.Error(), "noop:default") {
		t.Fatalf("vector mode with noop: err = %v, want ErrCapability naming the model", err)
	}
	r, err := s.Search(context.Background(), api.SearchRequest{Query: "keyword search"})
	if err != nil {
		t.Fatalf("hybrid with noop: %v", err)
	}
	if len(r.Results) != 1 || r.Results[0].MemoryID != id || len(r.Degraded) != 1 || r.Degraded[0] != api.DegradedVector {
		t.Fatalf("hybrid with noop: results=%+v degraded=%v; want the keyword hit and [vector_unavailable]", r.Results, r.Degraded)
	}
}
