package chunker

import (
	"strings"
	"testing"
)

func TestJSONLChunker_BasicOneLinePerCell(t *testing.T) {
	c, err := Get("jsonl")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"a":1}` + "\n" + `{"a":2}` + "\n" + `{"a":3}` + "\n"
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d", len(cells))
	}
	if cells[0].Text != `{"a":1}` {
		t.Errorf("cell 0: %q", cells[0].Text)
	}
	if cells[2].Text != `{"a":3}` {
		t.Errorf("cell 2: %q", cells[2].Text)
	}
}

func TestJSONLChunker_MultipleLinesPerCell(t *testing.T) {
	ck, _ := Get("jsonl")
	ck.(Configurable).Configure(map[string]string{"lines_per_cell": "2"})
	body := `{"a":1}` + "\n" + `{"a":2}` + "\n" + `{"a":3}` + "\n" + `{"a":4}` + "\n" + `{"a":5}` + "\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells (5 lines / 2 per cell), got %d", len(cells))
	}
	if !strings.Contains(cells[0].Text, `{"a":1}`) || !strings.Contains(cells[0].Text, `{"a":2}`) {
		t.Errorf("cell 0 should have lines 1-2: %q", cells[0].Text)
	}
	if cells[2].Text != `{"a":5}` {
		t.Errorf("cell 2 should have line 5 only: %q", cells[2].Text)
	}
}

func TestJSONLChunker_EmptyContent(t *testing.T) {
	ck, _ := Get("jsonl")
	cells, err := ck.Chunk(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if cells != nil {
		t.Fatalf("expected nil for empty, got %d cells", len(cells))
	}
}

func TestJSONLChunker_WhitespaceOnly(t *testing.T) {
	ck, _ := Get("jsonl")
	cells, err := ck.Chunk(ctx, "  \n\n  \n")
	if err != nil {
		t.Fatal(err)
	}
	if cells != nil {
		t.Fatalf("expected nil for whitespace-only, got %d cells", len(cells))
	}
}

func TestJSONLChunker_TrailingNewlineAndBlankLines(t *testing.T) {
	ck, _ := Get("jsonl")
	body := `{"x":1}` + "\n\n" + `{"x":2}` + "\n\n\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells (blank lines skipped), got %d", len(cells))
	}
}

func TestJSONLChunker_LinesPerCellExceedsCount(t *testing.T) {
	ck, _ := Get("jsonl")
	ck.(Configurable).Configure(map[string]string{"lines_per_cell": "100"})
	body := `{"a":1}` + "\n" + `{"a":2}` + "\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell when lines_per_cell > count, got %d", len(cells))
	}
}

func TestJSONLChunker_ValidateTrue_InvalidJSON(t *testing.T) {
	ck, _ := Get("jsonl")
	body := `{"a":1}` + "\n" + `not json` + "\n" + `{"a":3}` + "\n"
	_, err := ck.Chunk(ctx, body)
	if err == nil {
		t.Fatal("expected error for invalid JSON with validate=true")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should mention line 2: %v", err)
	}
}

func TestJSONLChunker_ValidateFalse_InvalidJSON(t *testing.T) {
	ck, _ := Get("jsonl")
	ck.(Configurable).Configure(map[string]string{"validate": "false"})
	body := `{"a":1}` + "\n" + `not json` + "\n" + `{"a":3}` + "\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatalf("validate=false should not error: %v", err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d", len(cells))
	}
	if cells[1].Text != "not json" {
		t.Errorf("cell 1 should be verbatim: %q", cells[1].Text)
	}
}

func TestJSONLChunker_VerbatimRoundTrip(t *testing.T) {
	ck, _ := Get("jsonl")
	lines := []string{`{"k":"v1"}`, `{"k":"v2"}`, `{"k":"v3"}`}
	body := strings.Join(lines, "\n") + "\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	var rebuilt []string
	for _, c := range cells {
		rebuilt = append(rebuilt, strings.Split(c.Text, "\n")...)
	}
	if len(rebuilt) != len(lines) {
		t.Fatalf("round-trip line count: want %d, got %d", len(lines), len(rebuilt))
	}
	for i, l := range lines {
		if rebuilt[i] != l {
			t.Errorf("line %d: want %q, got %q", i, l, rebuilt[i])
		}
	}
}

func TestJSONLChunker_BadConfig(t *testing.T) {
	ck, _ := Get("jsonl")
	c := ck.(Configurable)
	if err := c.Configure(map[string]string{"lines_per_cell": "0"}); err == nil {
		t.Error("expected error for lines_per_cell=0")
	}
	if err := c.Configure(map[string]string{"lines_per_cell": "abc"}); err == nil {
		t.Error("expected error for lines_per_cell=abc")
	}
	if err := c.Configure(map[string]string{"validate": "maybe"}); err == nil {
		t.Error("expected error for validate=maybe")
	}
}

func TestJSONLChunker_MD5Determinism(t *testing.T) {
	body := `{"x":1}` + "\n" + `{"x":2}` + "\n"
	ck1, _ := Get("jsonl")
	a, _ := ck1.Chunk(ctx, body)
	ck2, _ := Get("jsonl")
	b, _ := ck2.Chunk(ctx, body)
	if len(a) != len(b) {
		t.Fatal("cell count differs")
	}
	for i := range a {
		if a[i].TextMD5 != b[i].TextMD5 {
			t.Fatalf("cell %d md5 differs", i)
		}
	}
}

func TestJSONLChunker_SeqNumbers(t *testing.T) {
	ck, _ := Get("jsonl")
	body := `{"a":1}` + "\n" + `{"a":2}` + "\n" + `{"a":3}` + "\n" + `{"a":4}` + "\n" + `{"a":5}` + "\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cells {
		if c.Seq != i {
			t.Errorf("cell %d has seq=%d", i, c.Seq)
		}
	}
}

func TestJSONLChunker_SingleLongLine(t *testing.T) {
	ck, _ := Get("jsonl")
	obj := `{"data":"` + strings.Repeat("x", 10000) + `"}`
	cells, err := ck.Chunk(ctx, obj+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell for single long line, got %d", len(cells))
	}
	if cells[0].Text != obj {
		t.Error("single long line not preserved verbatim")
	}
}
