package chunker

import (
	"strings"
	"testing"
)

func TestDefaultChunker_SmallContent_SingleCell(t *testing.T) {
	c, _ := Get("default")
	cells := c.Chunk("hello world")
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell, got %d", len(cells))
	}
	if cells[0].Text != "hello world" {
		t.Errorf("text roundtrip: %q", cells[0].Text)
	}
}

func TestDefaultChunker_LargeContent_MultipleCells(t *testing.T) {
	c, _ := Get("default")
	body := strings.Repeat("This is a paragraph of text. ", 200) // ~5800 chars
	cells := c.Chunk(body)
	if len(cells) < 2 {
		t.Fatalf("expected multiple cells for large body, got %d", len(cells))
	}
}

func TestNoChunkChunker_OneCell(t *testing.T) {
	c, _ := Get("no-chunk")
	body := strings.Repeat("paragraph\n\n", 100)
	cells := c.Chunk(body)
	if len(cells) != 1 {
		t.Fatalf("no-chunk should produce 1 cell, got %d", len(cells))
	}
}

func TestMarkdownChunker_PreservesCodeFences(t *testing.T) {
	c, _ := Get("markdown")
	body := "Intro paragraph.\n\n```go\nfunc main() {}\n```\n\nOutro paragraph."
	cells := c.Chunk(body)
	// There should be a cell containing the fenced block verbatim.
	found := false
	for _, c := range cells {
		if strings.Contains(c.Text, "func main()") && strings.Contains(c.Text, "```") {
			found = true
		}
	}
	if !found {
		t.Fatal("markdown chunker did not preserve fenced block as a unit")
	}
}

func TestDeterminism(t *testing.T) {
	c, _ := Get("default")
	body := "alpha beta gamma. delta epsilon zeta.\n\nhello world."
	a := c.Chunk(body)
	b := c.Chunk(body)
	if len(a) != len(b) {
		t.Fatal("chunk count differs")
	}
	for i := range a {
		if a[i].Text != b[i].Text {
			t.Fatalf("cell %d differs: %q vs %q", i, a[i].Text, b[i].Text)
		}
		if a[i].TextMD5 != b[i].TextMD5 {
			t.Fatalf("cell %d md5 differs", i)
		}
	}
}
