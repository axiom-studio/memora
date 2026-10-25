package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestResultLimits_K (#6166): k above MaxK, and lookup memory_ids beyond
// it, are refused with ErrInvalidInput; MaxK itself is accepted.
func TestResultLimits_K(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	imprint(t, s, ws, "bounded search phrase")
	ctx := context.Background()

	if _, err := s.Search(ctx, api.SearchRequest{Query: "bounded", K: MaxK + 1}); !errors.Is(err, types.ErrInvalidInput) {
		t.Errorf("search k=%d: err = %v, want ErrInvalidInput", MaxK+1, err)
	}
	if _, err := s.Search(ctx, api.SearchRequest{Query: "bounded", K: 30000, Mode: api.RecallModeKeyword, IncludeContent: true}); !errors.Is(err, types.ErrInvalidInput) {
		t.Errorf("search k=30000: err = %v, want ErrInvalidInput", err)
	}
	if _, err := s.Search(ctx, api.SearchRequest{Query: "bounded", K: MaxK}); err != nil {
		t.Errorf("search k=%d: %v", MaxK, err)
	}
	if _, err := s.Recall(ctx, ws, api.RecallRequest{Query: "bounded", K: MaxK + 1}); !errors.Is(err, types.ErrInvalidInput) {
		t.Errorf("recall k=%d: err = %v, want ErrInvalidInput", MaxK+1, err)
	}
	ids := make([]string, MaxK+1)
	for i := range ids {
		ids[i] = types.NewID()
	}
	if _, err := s.Recall(ctx, ws, api.RecallRequest{Mode: api.RecallModeLookup, MemoryIDs: ids}); !errors.Is(err, types.ErrInvalidInput) {
		t.Errorf("lookup with %d ids: err = %v, want ErrInvalidInput", len(ids), err)
	}
	if _, err := s.Recall(ctx, ws, api.RecallRequest{Mode: api.RecallModeLookup, MemoryIDs: ids[:MaxK]}); err != nil {
		t.Errorf("lookup with %d ids: %v", MaxK, err)
	}
}

// TestResultLimits_ResponseBudget (#6166): one response carries at most
// maxResponseContentBytes of content and cell text, however many large
// memories match; hits past the budget are flagged, best ranks first.
func TestResultLimits_ResponseBudget(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	const n = 12
	body := strings.Repeat("ünïcode budget filler sentence. ", (1<<20)/34) // ~1 MiB, multi-byte runes
	for i := 0; i < n; i++ {
		imprint(t, s, ws, fmt.Sprintf("budgetterm memory %d\n\n%s", i, body))
	}
	ctx := context.Background()

	// budgetBound: the response budget, not the per-hit cell cap, is
	// what limits this request.
	check := func(name string, req api.SearchRequest, wantContent, wantCells, budgetBound bool) {
		t.Helper()
		r, err := s.Search(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Results) != n {
			t.Fatalf("%s: %d results, want %d", name, len(r.Results), n)
		}
		total, flagged := 0, 0
		for _, h := range r.Results {
			total += len(h.Content) + len(h.MatchedText)
			for _, c := range h.Cells {
				total += len(c.Text)
			}
			if !utf8.ValidString(h.Content) {
				t.Fatalf("%s: content cut inside a rune", name)
			}
			if (wantContent && h.ContentTruncated) || (wantCells && h.CellsTruncated) {
				flagged++
			}
		}
		if total > maxResponseContentBytes {
			t.Fatalf("%s: %d bytes of content and cells, budget %d", name, total, maxResponseContentBytes)
		}
		if flagged == 0 {
			t.Fatalf("%s: no hit flagged truncated", name)
		}
		if !budgetBound {
			return
		}
		if total < maxResponseContentBytes/2 {
			t.Fatalf("%s: total %d; want the budget used", name, total)
		}
		last := r.Results[n-1]
		if last.Content != "" || len(last.Cells) != 0 || last.MatchedText == "" {
			t.Fatalf("%s: last hit keeps %d content bytes, %d cells, matched %d; want only its matched cell", name, len(last.Content), len(last.Cells), len(last.MatchedText))
		}
	}
	check("content", api.SearchRequest{Query: "budgetterm", Mode: api.RecallModeKeyword, K: n, IncludeContent: true, MaxContentBytes: 1 << 20}, true, false, true)
	check("cells", api.SearchRequest{Query: "budgetterm", Mode: api.RecallModeKeyword, K: n, IncludeCells: true}, false, true, false)
	check("both", api.SearchRequest{Query: "budgetterm", Mode: api.RecallModeKeyword, K: n, IncludeContent: true, MaxContentBytes: 1 << 20, IncludeCells: true}, true, true, true)

	// Recall in one workspace is bounded the same way.
	r, err := s.Recall(ctx, ws, api.RecallRequest{Query: "budgetterm", Mode: api.RecallModeKeyword, K: n, IncludeContent: true, MaxContentBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, h := range r.Results {
		total += len(h.Content)
	}
	if total > maxResponseContentBytes {
		t.Fatalf("recall: %d content bytes, budget %d", total, maxResponseContentBytes)
	}
}

// TestResultLimits_ResponseBudget_LargeMatchedCells (#6352): matched
// cell text counts toward the response budget. Memories stored as one
// ~1 MiB cell each (no-chunk) cannot push a response past it; the
// best-ranked hits keep their whole matched cell and the hit that
// reaches the limit is cut on a rune boundary and flagged. A federated
// search, which budgets local hits twice, keeps every flag (QA rework).
func TestResultLimits_ResponseBudget_LargeMatchedCells(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	const n = 12
	body := strings.Repeat("ünïcode budget filler sentence. ", (1<<20)/34)
	off := false
	for i := 0; i < n; i++ {
		if _, err := s.Imprint(context.Background(), ws, testAgent, api.ImprintRequest{
			Content: fmt.Sprintf("bigcell memory %d\n\n%s", i, body), ChunkerID: "no-chunk", AutoLink: &off,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true}, types.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if s.Federation, err = federation.NewSearcher(reg); err != nil {
		t.Fatal(err)
	}
	for _, federated := range []bool{false, true} {
		r, err := s.Search(context.Background(), api.SearchRequest{Query: "bigcell", Mode: api.RecallModeKeyword, K: n, Federated: federated})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Results) != n {
			t.Fatalf("federated=%v: %d results, want %d", federated, len(r.Results), n)
		}
		total, cut := 0, 0
		for i, h := range r.Results {
			total += len(h.MatchedText)
			if !utf8.ValidString(h.MatchedText) {
				t.Fatalf("federated=%v: hit %d: matched text cut inside a rune", federated, i)
			}
			if h.MatchedTextTruncated {
				cut++
			} else if len(h.MatchedText) < len(body) {
				t.Fatalf("federated=%v: hit %d: %d matched bytes but not flagged truncated", federated, i, len(h.MatchedText))
			}
		}
		if total > maxResponseContentBytes {
			t.Fatalf("federated=%v: %d bytes of matched text, budget %d", federated, total, maxResponseContentBytes)
		}
		if cut == 0 || r.Results[0].MatchedTextTruncated {
			t.Fatalf("federated=%v: %d hits cut, top hit cut %v; want later hits cut, the top one whole", federated, cut, r.Results[0].MatchedTextTruncated)
		}
	}
}

// TestResultLimits_CellsPerHit: include_cells returns at most
// maxCellsPerHit cells of one memory and says so.
func TestResultLimits_CellsPerHit(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	var b strings.Builder
	b.WriteString("manycells\n\n")
	b.WriteString(strings.Repeat("a sentence of filler words for many cells. ", (1<<20)/44))
	id := imprint(t, s, ws, b.String())
	cells, err := s.Metadata.GetCells(context.Background(), ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) <= maxCellsPerHit {
		t.Fatalf("chunker made %d cells; the test needs more than %d", len(cells), maxCellsPerHit)
	}
	r, err := s.Search(context.Background(), api.SearchRequest{Query: "manycells", Mode: api.RecallModeKeyword, K: 1, IncludeCells: true})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("search: %v (%d results)", err, len(r.Results))
	}
	if h := r.Results[0]; len(h.Cells) != maxCellsPerHit || !h.CellsTruncated {
		t.Fatalf("%d of %d cells returned (truncated %v), want %d and the flag", len(h.Cells), len(cells), h.CellsTruncated, maxCellsPerHit)
	}
}
