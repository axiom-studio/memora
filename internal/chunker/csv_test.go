package chunker

import (
	"strings"
	"testing"
)

func TestCSVChunker_BasicOneRowPerCell(t *testing.T) {
	c, err := Get("csv")
	if err != nil {
		t.Fatal(err)
	}
	body := "name,age\nAlice,30\nBob,25\nCarol,28\n"
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells (1 per data row), got %d", len(cells))
	}
	for _, c := range cells {
		if !strings.Contains(c.Text, "name,age") {
			t.Errorf("cell %d missing header: %q", c.Seq, c.Text)
		}
	}
	if !strings.Contains(cells[0].Text, "Alice") {
		t.Errorf("cell 0 missing Alice: %q", cells[0].Text)
	}
	if !strings.Contains(cells[2].Text, "Carol") {
		t.Errorf("cell 2 missing Carol: %q", cells[2].Text)
	}
}

func TestCSVChunker_MultipleRowsPerCell(t *testing.T) {
	ck, _ := Get("csv")
	ck.(Configurable).Configure(map[string]string{"rows_per_cell": "2"})
	body := "a,b\n1,2\n3,4\n5,6\n7,8\n9,10\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells (5 rows / 2 per cell, last cell has 1), got %d", len(cells))
	}
	if !strings.Contains(cells[0].Text, "1,2") || !strings.Contains(cells[0].Text, "3,4") {
		t.Errorf("cell 0 should have rows 1-2: %q", cells[0].Text)
	}
	if !strings.Contains(cells[2].Text, "9,10") {
		t.Errorf("cell 2 should have row 5: %q", cells[2].Text)
	}
}

func TestCSVChunker_NoHeader(t *testing.T) {
	ck, _ := Get("csv")
	ck.(Configurable).Configure(map[string]string{"has_header": "false"})
	body := "Alice,30\nBob,25\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(cells))
	}
	if strings.Contains(cells[1].Text, "Alice") {
		t.Error("cell 1 should not contain Alice when has_header=false")
	}
}

func TestCSVChunker_CustomDelimiter(t *testing.T) {
	ck, _ := Get("csv")
	ck.(Configurable).Configure(map[string]string{"delimiter": "\t"})
	body := "name\tage\nAlice\t30\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell, got %d", len(cells))
	}
	if !strings.Contains(cells[0].Text, "name\tage") {
		t.Errorf("cell should use tab delimiter: %q", cells[0].Text)
	}
}

func TestCSVChunker_EmptyContent(t *testing.T) {
	ck, _ := Get("csv")
	cells, err := ck.Chunk(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if cells != nil {
		t.Fatalf("expected nil for empty, got %d cells", len(cells))
	}
}

func TestCSVChunker_HeaderOnly(t *testing.T) {
	ck, _ := Get("csv")
	cells, err := ck.Chunk(ctx, "name,age\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell for header-only, got %d", len(cells))
	}
	if !strings.Contains(cells[0].Text, "name,age") {
		t.Errorf("header-only cell: %q", cells[0].Text)
	}
}

func TestCSVChunker_RowsPerCellExceedsRowCount(t *testing.T) {
	ck, _ := Get("csv")
	ck.(Configurable).Configure(map[string]string{"rows_per_cell": "100"})
	body := "h1,h2\na,b\nc,d\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell when rows_per_cell > row count, got %d", len(cells))
	}
}

func TestCSVChunker_RaggedRows(t *testing.T) {
	ck, _ := Get("csv")
	body := "a,b,c\n1,2\n3,4,5,6\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells for ragged CSV, got %d", len(cells))
	}
}

func TestCSVChunker_QuotedFields(t *testing.T) {
	ck, _ := Get("csv")
	body := "name,bio\nAlice,\"She said, \"\"hello\"\"\"\nBob,\"line1\nline2\"\n"
	cells, err := ck.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(cells))
	}
	if !strings.Contains(cells[0].Text, "hello") {
		t.Errorf("cell 0 missing quoted content: %q", cells[0].Text)
	}
}

func TestCSVChunker_BadConfig(t *testing.T) {
	ck, _ := Get("csv")
	c := ck.(Configurable)
	if err := c.Configure(map[string]string{"rows_per_cell": "0"}); err == nil {
		t.Error("expected error for rows_per_cell=0")
	}
	if err := c.Configure(map[string]string{"rows_per_cell": "abc"}); err == nil {
		t.Error("expected error for rows_per_cell=abc")
	}
	if err := c.Configure(map[string]string{"has_header": "maybe"}); err == nil {
		t.Error("expected error for has_header=maybe")
	}
	if err := c.Configure(map[string]string{"delimiter": "ab"}); err == nil {
		t.Error("expected error for multi-char delimiter")
	}
}

func TestCSVChunker_MD5Determinism(t *testing.T) {
	ck, _ := Get("csv")
	body := "x,y\n1,2\n3,4\n"
	a, _ := ck.Chunk(ctx, body)

	ck2, _ := Get("csv")
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

func TestCSVChunker_SeqNumbers(t *testing.T) {
	ck, _ := Get("csv")
	body := "h\na\nb\nc\nd\ne\n"
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
