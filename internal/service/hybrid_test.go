package service

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
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
