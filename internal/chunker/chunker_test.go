package chunker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

var ctx = context.Background()

func TestDefaultChunker_SmallContent_SingleCell(t *testing.T) {
	c, _ := Get("default")
	cells, err := c.Chunk(ctx, "hello world")
	if err != nil {
		t.Fatal(err)
	}
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
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 2 {
		t.Fatalf("expected multiple cells for large body, got %d", len(cells))
	}
}

func TestNoChunkChunker_OneCell(t *testing.T) {
	c, _ := Get("no-chunk")
	body := strings.Repeat("paragraph\n\n", 100)
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("no-chunk should produce 1 cell, got %d", len(cells))
	}
}

func TestMarkdownChunker_PreservesCodeFences(t *testing.T) {
	c, _ := Get("markdown")
	body := "Intro paragraph.\n\n```go\nfunc main() {}\n```\n\nOutro paragraph."
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
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
	a, _ := c.Chunk(ctx, body)
	b, _ := c.Chunk(ctx, body)
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

func TestMarkdownChunker_SplitsOnHeadings(t *testing.T) {
	c, _ := Get("markdown")
	body := "# Chapter 1\n\nSome text in chapter one.\n\n# Chapter 2\n\nSome text in chapter two."
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 2 {
		t.Fatalf("expected at least 2 cells (one per heading), got %d", len(cells))
	}
	ch1Found, ch2Found := false, false
	for _, c := range cells {
		if strings.Contains(c.Text, "Chapter 1") && strings.Contains(c.Text, "chapter one") {
			ch1Found = true
		}
		if strings.Contains(c.Text, "Chapter 2") && strings.Contains(c.Text, "chapter two") {
			ch2Found = true
		}
	}
	if !ch1Found || !ch2Found {
		t.Errorf("headings not split into separate cells: ch1=%v ch2=%v", ch1Found, ch2Found)
	}
	for _, c := range cells {
		if strings.Contains(c.Text, "Chapter 1") && strings.Contains(c.Text, "Chapter 2") {
			t.Error("both chapters ended up in the same cell — heading split did not fire")
		}
	}
}

func TestGoChunker_SplitsOnDeclarations(t *testing.T) {
	c, _ := Get("code-go")
	src := `package main

import "fmt"

func Hello() {
	fmt.Println("hello")
}

func World() {
	fmt.Println("world")
}
`
	cells, err := c.Chunk(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 3 {
		t.Fatalf("expected >=3 cells (header, Hello, World), got %d", len(cells))
	}
	helloFound, worldFound := false, false
	for _, c := range cells {
		if strings.Contains(c.Text, "func Hello()") {
			helloFound = true
			if strings.Contains(c.Text, "func World()") {
				t.Error("Hello and World ended up in the same cell")
			}
		}
		if strings.Contains(c.Text, "func World()") {
			worldFound = true
		}
	}
	if !helloFound || !worldFound {
		t.Errorf("missing declarations: hello=%v world=%v", helloFound, worldFound)
	}
}

func TestGoChunker_FallsBackOnNonGo(t *testing.T) {
	c, _ := Get("code-go")
	content := "This is plain English text, not Go code."
	cells, err := c.Chunk(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell for non-Go fallback, got %d", len(cells))
	}
	if cells[0].Text != content {
		t.Errorf("text mismatch: %q", cells[0].Text)
	}
}

func TestDefaultChunker_50KB_MultipleCells(t *testing.T) {
	c, _ := Get("default")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "## Section %d\n\nLorem ipsum dolor sit amet, consectetur adipiscing elit. "+
			"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. "+
			"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris.\n\n", i)
	}
	body := b.String()
	if len(body) < 50000 {
		t.Fatalf("fixture too small: %d bytes, want >=50KB", len(body))
	}
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 10 {
		t.Fatalf("expected many cells for 50KB body, got %d", len(cells))
	}
	for i, c := range cells {
		if strings.TrimSpace(c.Text) == "" {
			t.Errorf("cell %d is empty", i)
		}
		if c.TextMD5 == "" {
			t.Errorf("cell %d has no MD5", i)
		}
		if c.Seq != i {
			t.Errorf("cell %d has seq=%d", i, c.Seq)
		}
	}
}

func TestDefaultChunker_OverlapPresent(t *testing.T) {
	c := &DefaultChunker{TargetTokens: 64, OverlapTokens: 16}
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "Paragraph number %d contains unique marker P%03d. ", i, i)
		b.WriteString("Extra padding text to fill up the cell boundary and ensure splitting occurs naturally.\n\n")
	}
	cells, err := c.Chunk(ctx, b.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 3 {
		t.Fatalf("need >=3 cells to test overlap, got %d", len(cells))
	}
	overlapFound := false
	overlapChars := 16 * 4 // OverlapTokens * 4 chars/token
	for i := 1; i < len(cells); i++ {
		prev := cells[i-1].Text
		cur := cells[i].Text
		if len(prev) < overlapChars {
			continue
		}
		tail := prev[len(prev)-overlapChars:]
		if strings.Contains(cur, tail) {
			overlapFound = true
			break
		}
	}
	if !overlapFound {
		t.Error("no overlap detected between consecutive cells")
	}
}

func TestMarkdownChunker_50KB_HeadingsAndFences(t *testing.T) {
	c, _ := Get("markdown")
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "# Section %d\n\n", i)
		fmt.Fprintf(&b, "Paragraph of text in section %d. This has enough content to be meaningful.\n\n", i)
		if i%5 == 0 {
			fmt.Fprintf(&b, "```go\nfunc Example%d() {}\n```\n\n", i)
		}
	}
	body := b.String()
	if len(body) < 15000 {
		t.Fatalf("fixture too small: %d bytes", len(body))
	}
	cells, err := c.Chunk(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 50 {
		t.Fatalf("expected many cells, got %d", len(cells))
	}
	fencedCells := 0
	for _, c := range cells {
		if strings.Contains(c.Text, "```go") {
			fencedCells++
		}
	}
	if fencedCells < 30 {
		t.Errorf("expected ~40 fenced cells, got %d", fencedCells)
	}
}

func TestSetTokenCounter(t *testing.T) {
	original := ApproxTokens("hello world")

	SetTokenCounter(func(s string) int { return len(s) })
	if got := ApproxTokens("hello"); got != 5 {
		t.Errorf("custom counter: want 5, got %d", got)
	}

	SetTokenCounter(func(s string) int { return len(s) / 4 })
	if got := ApproxTokens("hello world"); got != original {
		t.Errorf("restored counter: want %d, got %d", original, got)
	}
}

func TestApproxTokens_DefaultHeuristic(t *testing.T) {
	got := ApproxTokens("abcdefghijklmnop") // 16 chars
	if got != 4 {
		t.Errorf("want 4 tokens for 16 chars, got %d", got)
	}
}

type fakeConfigurableChunker struct {
	configured map[string]string
}

func (f *fakeConfigurableChunker) Name() string { return "test-configurable" }

func (f *fakeConfigurableChunker) Chunk(_ context.Context, content string) ([]types.Cell, error) {
	return []types.Cell{mkCell(0, content)}, nil
}

func (f *fakeConfigurableChunker) Configure(opts map[string]string) error {
	if v, ok := opts["bad"]; ok && v == "true" {
		return errors.New("invalid config: bad=true is not allowed")
	}
	f.configured = opts
	return nil
}

func init() {
	Register("test-configurable", func() Chunker { return &fakeConfigurableChunker{} })
}

func TestConfigurable_GoodConfig(t *testing.T) {
	ck, err := Get("test-configurable")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := ck.(Configurable)
	if !ok {
		t.Fatal("test-configurable does not implement Configurable")
	}
	if err := c.Configure(map[string]string{"rows_per_cell": "10"}); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}
	fc := ck.(*fakeConfigurableChunker)
	if fc.configured["rows_per_cell"] != "10" {
		t.Errorf("config not applied: %v", fc.configured)
	}
}

func TestConfigurable_BadConfig(t *testing.T) {
	ck, err := Get("test-configurable")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := ck.(Configurable)
	if !ok {
		t.Fatal("test-configurable does not implement Configurable")
	}
	if err := c.Configure(map[string]string{"bad": "true"}); err == nil {
		t.Fatal("expected Configure to fail with bad config")
	}
}

func TestNonConfigurable_IgnoresConfig(t *testing.T) {
	ck, err := Get("default")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ck.(Configurable); ok {
		t.Fatal("default chunker should NOT implement Configurable")
	}
}

func TestConfigurable_EmptyConfig(t *testing.T) {
	ck, err := Get("test-configurable")
	if err != nil {
		t.Fatal(err)
	}
	c := ck.(Configurable)
	if err := c.Configure(map[string]string{}); err != nil {
		t.Fatalf("empty config should not error: %v", err)
	}
}
