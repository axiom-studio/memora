package service

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// countingMetadata wraps the real store and counts the loads hydrate is
// meant to batch.
type countingMetadata struct {
	adapter.MetadataStore
	getMemory, getMemories, getCells, getCellsFor atomic.Int64
}

func (c *countingMetadata) GetMemory(ctx context.Context, id string) (*types.Memory, error) {
	c.getMemory.Add(1)
	return c.MetadataStore.GetMemory(ctx, id)
}

func (c *countingMetadata) GetMemories(ctx context.Context, ids []string) ([]types.Memory, error) {
	c.getMemories.Add(1)
	return c.MetadataStore.GetMemories(ctx, ids)
}

func (c *countingMetadata) GetCells(ctx context.Context, ws, id string) ([]types.Cell, error) {
	c.getCells.Add(1)
	return c.MetadataStore.GetCells(ctx, ws, id)
}

func (c *countingMetadata) GetCellsForMemories(ctx context.Context, ids []string) (map[string][]types.Cell, error) {
	c.getCellsFor.Add(1)
	return c.MetadataStore.GetCellsForMemories(ctx, ids)
}

// TestSearch_StoreCallsDoNotGrowWithK: hydrating 1 or 20 hits costs the
// same two batched loads and no per-hit GetMemory/GetCells.
func TestSearch_StoreCallsDoNotGrowWithK(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	for i := 0; i < 20; i++ {
		imprint(t, s, ws, "counted phrase number")
	}
	counter := &countingMetadata{MetadataStore: s.Metadata}
	s.Metadata = counter

	for _, k := range []int{1, 20} {
		counter.getMemory.Store(0)
		counter.getMemories.Store(0)
		counter.getCells.Store(0)
		counter.getCellsFor.Store(0)
		r, err := s.Search(context.Background(), api.SearchRequest{Query: "counted phrase", K: k})
		if err != nil || len(r.Results) != k {
			t.Fatalf("k=%d: %d results, %v", k, len(r.Results), err)
		}
		if counter.getMemory.Load() != 0 || counter.getCells.Load() != 0 ||
			counter.getMemories.Load() != 1 || counter.getCellsFor.Load() != 1 {
			t.Fatalf("k=%d: GetMemory=%d GetCells=%d GetMemories=%d GetCellsForMemories=%d; want 0, 0, 1, 1",
				k, counter.getMemory.Load(), counter.getCells.Load(), counter.getMemories.Load(), counter.getCellsFor.Load())
		}
	}
}

// TestSearch_HitsCarryMatchedCellAndData: the matched cell's full text
// and position come back, for vector hits (the cell searched for) and for
// keyword hits (the cell holding the term), with created_at.
func TestSearch_HitsCarryMatchedCellAndData(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	id := imprint(t, s, ws, strings.Repeat("filler words here. ", 200)+"\n\nthe obsidian marker sits in this paragraph only")
	cells, err := s.Metadata.GetCells(context.Background(), ws, id)
	if err != nil || len(cells) < 2 {
		t.Fatalf("want a multi-cell memory, got %d cells (%v)", len(cells), err)
	}

	r, err := s.Search(context.Background(), api.SearchRequest{Query: "obsidian", Mode: api.RecallModeKeyword})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("keyword search: %+v, %v", r, err)
	}
	h := r.Results[0]
	if !strings.Contains(h.MatchedText, "obsidian marker") || h.CellIndex == nil || h.CellID == "" || h.CreatedAt == nil || h.CreatedAt.IsZero() {
		t.Fatalf("keyword hit = %+v, want the obsidian cell with its position and created_at", h)
	}
	if want := cells[*h.CellIndex]; want.CellID != h.CellID || want.Text != h.MatchedText {
		t.Fatalf("matched cell %s@%d does not match the stored cell %s", h.CellID, *h.CellIndex, want.CellID)
	}
	if !strings.Contains(h.Text, "obsidian") || len(h.Text) > 240 {
		t.Fatalf("display excerpt %q should centre on the term within 240 bytes", h.Text)
	}

	last := cells[len(cells)-1]
	r, err = s.Search(context.Background(), api.SearchRequest{Query: last.Text, Mode: api.RecallModeVector, K: 1})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("vector search: %+v, %v", r, err)
	}
	if r.Results[0].CellID != last.CellID || r.Results[0].MatchedText != last.Text || *r.Results[0].CellIndex != last.Seq {
		t.Fatalf("vector hit cell %s, want %s with its text", r.Results[0].CellID, last.CellID)
	}
}

func TestSearch_IncludeContentAndCells(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	content := "héllo " + strings.Repeat("ü", 100) + " tail"
	id := imprint(t, s, ws, content)

	r, err := s.Search(context.Background(), api.SearchRequest{Query: "héllo", Mode: api.RecallModeKeyword})
	if err != nil || len(r.Results) != 1 || r.Results[0].Content != "" || r.Results[0].Cells != nil {
		t.Fatalf("without include flags: %+v, %v; want no content or cells", r.Results, err)
	}

	r, err = s.Search(context.Background(), api.SearchRequest{Query: "héllo", Mode: api.RecallModeKeyword, IncludeContent: true, IncludeCells: true})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("include: %+v, %v", r, err)
	}
	h := r.Results[0]
	cells, _ := s.Metadata.GetCells(context.Background(), ws, id)
	if h.Content != content || h.ContentTruncated || len(h.Cells) != len(cells) {
		t.Fatalf("include_content/cells: content=%q truncated=%v cells=%d, want full content and %d cells", h.Content, h.ContentTruncated, len(h.Cells), len(cells))
	}

	r, err = s.Search(context.Background(), api.SearchRequest{Query: "héllo", Mode: api.RecallModeKeyword, IncludeContent: true, MaxContentBytes: 10})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("capped: %+v, %v", r, err)
	}
	h = r.Results[0]
	if !h.ContentTruncated || len(h.Content) > 10 || !utf8.ValidString(h.Content) || !strings.HasPrefix(content, h.Content) {
		t.Fatalf("capped content %q (truncated=%v): want a valid UTF-8 prefix of at most 10 bytes", h.Content, h.ContentTruncated)
	}
}

func TestTruncateUTF8AndExcerpt(t *testing.T) {
	if got, cut := truncateUTF8("abc", 5); got != "abc" || cut {
		t.Errorf("short string: %q %v", got, cut)
	}
	if got, cut := truncateUTF8("aüb", 2); got != "a" || !cut { // ü is 2 bytes; never split it
		t.Errorf("rune boundary: %q %v", got, cut)
	}
	text := strings.Repeat("x", 500) + "needle" + strings.Repeat("y", 500)
	if ex := excerpt(text, []string{"absent", "needle"}, 100); !strings.Contains(ex, "needle") || len(ex) > 100 {
		t.Errorf("excerpt = %q, want the needle within 100 bytes", ex)
	}
	if ex := excerpt("start of text", []string{"absent"}, 5); ex != "start" {
		t.Errorf("excerpt without a match = %q, want the start", ex)
	}
}
