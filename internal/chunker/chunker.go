// Package chunker splits Memory content into Cells — the unit of
// embedding. v0.1 ships a token-approximated paragraph-aware default
// plus a markdown-aware variant; `no-chunk` produces one cell per memory.
package chunker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/axiom-studio/memora/pkg/types"
)

// Chunker splits a Memory's content into Cells. Cells carry sequence
// + text + text_md5; the caller fills cell_id and persists.
type Chunker interface {
	Name() string
	Chunk(ctx context.Context, content string) ([]types.Cell, error)
}

// Factory builds a Chunker.
type Factory func() Chunker

var (
	regMu  sync.RWMutex
	chunks = map[string]Factory{}
)

// Register adds a chunker under a stable name.
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := chunks[name]; dup {
		panic(fmt.Sprintf("chunker %q registered twice", name))
	}
	chunks[name] = f
}

// Get instantiates a chunker by name. Falls back to "default" if name is empty.
func Get(name string) (Chunker, error) {
	if name == "" {
		name = "default"
	}
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := chunks[name]
	if !ok {
		return nil, fmt.Errorf("unknown chunker %q (registered: %v)", name, names())
	}
	return f(), nil
}

func names() []string {
	out := make([]string, 0, len(chunks))
	for k := range chunks {
		out = append(out, k)
	}
	return out
}

// TokenCounter counts tokens in a string. Defaults to a 4-char heuristic;
// call SetTokenCounter to wire in tiktoken or another real tokenizer.
type TokenCounter func(string) int

var (
	tokenMu      sync.RWMutex
	tokenCounter TokenCounter = func(s string) int { return len(s) / 4 }
)

// SetTokenCounter replaces the global token-counting function.
func SetTokenCounter(tc TokenCounter) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	tokenCounter = tc
}

// ApproxTokens returns a token-count estimate. Uses the registered
// TokenCounter (default: 4-chars-per-token heuristic).
func ApproxTokens(s string) int {
	tokenMu.RLock()
	tc := tokenCounter
	tokenMu.RUnlock()
	return tc(s)
}

// ----- default chunker -----

// DefaultChunker splits on paragraph boundaries first, then on sentence
// boundaries when a paragraph exceeds the token target. ~512 token
// target with ~64 token overlap.
type DefaultChunker struct {
	TargetTokens  int
	OverlapTokens int
}

func (c *DefaultChunker) Name() string { return "default" }

func (c *DefaultChunker) Chunk(_ context.Context, content string) ([]types.Cell, error) {
	if c.TargetTokens <= 0 {
		c.TargetTokens = 512
	}
	if c.OverlapTokens < 0 {
		c.OverlapTokens = 64
	}
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	target := c.TargetTokens * 4 // approx chars
	overlap := c.OverlapTokens * 4

	paragraphs := strings.Split(content, "\n\n")
	var cells []types.Cell
	current := ""
	for _, p := range paragraphs {
		p = strings.TrimRight(p, "\n")
		if p == "" {
			continue
		}
		if len(current)+len(p)+2 <= target {
			if current == "" {
				current = p
			} else {
				current += "\n\n" + p
			}
			continue
		}
		if current != "" {
			cells = append(cells, mkCell(len(cells), current))
			tail := tailOverlap(current, overlap)
			current = tail
			if current != "" {
				current += "\n\n"
			}
		}
		// If the paragraph itself exceeds the target, split it on sentence boundaries.
		if len(p) > target {
			for _, chunk := range splitLong(p, target) {
				if current == "" {
					current = chunk
				} else if len(current)+len(chunk)+2 <= target {
					current += "\n\n" + chunk
				} else {
					cells = append(cells, mkCell(len(cells), current))
					current = tailOverlap(current, overlap)
					if current != "" {
						current += "\n\n"
					}
					current += chunk
				}
			}
		} else {
			current += p
		}
	}
	if strings.TrimSpace(current) != "" {
		cells = append(cells, mkCell(len(cells), current))
	}
	return cells, nil
}

func mkCell(seq int, text string) types.Cell {
	return types.Cell{
		Seq:     seq,
		Text:    text,
		TextMD5: types.MD5Hex(text),
	}
}

func tailOverlap(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func splitLong(p string, target int) []string {
	// Sentence-ish split on .!? followed by space.
	var out []string
	cur := strings.Builder{}
	for i := 0; i < len(p); i++ {
		cur.WriteByte(p[i])
		if (p[i] == '.' || p[i] == '!' || p[i] == '?') && i+1 < len(p) && p[i+1] == ' ' && cur.Len() >= target/2 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	// If still too long, force-split on target bytes.
	var final []string
	for _, s := range out {
		for len(s) > target {
			final = append(final, s[:target])
			s = s[target:]
		}
		if s != "" {
			final = append(final, s)
		}
	}
	return final
}

// ----- markdown-aware chunker -----

// MarkdownChunker respects code blocks (never splits a fenced block).
type MarkdownChunker struct {
	Default DefaultChunker
}

func (c *MarkdownChunker) Name() string { return "markdown" }

func (c *MarkdownChunker) Chunk(ctx context.Context, content string) ([]types.Cell, error) {
	parts := splitOnFences(content)
	var cells []types.Cell
	for _, p := range parts {
		if p.fenced {
			cells = append(cells, mkCell(len(cells), p.text))
			continue
		}
		sub, err := c.Default.Chunk(ctx, p.text)
		if err != nil {
			return nil, err
		}
		for i := range sub {
			sub[i].Seq = len(cells) + i
		}
		cells = append(cells, sub...)
	}
	return cells, nil
}

type mdPart struct {
	text   string
	fenced bool
}

func splitOnFences(content string) []mdPart {
	lines := strings.Split(content, "\n")
	var parts []mdPart
	var buf []string
	inFence := false
	flush := func(fenced bool) {
		if len(buf) == 0 {
			return
		}
		parts = append(parts, mdPart{text: strings.Join(buf, "\n"), fenced: fenced})
		buf = nil
	}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				flush(false)
				inFence = true
			} else {
				buf = append(buf, l)
				flush(true)
				inFence = false
				continue
			}
		}
		if !inFence && len(trimmed) > 0 && trimmed[0] == '#' && len(buf) > 0 {
			flush(false)
		}
		buf = append(buf, l)
	}
	flush(inFence)
	return parts
}

// ----- no-chunk -----

// NoChunkChunker emits one cell containing the entire memory.
type NoChunkChunker struct{}

func (c *NoChunkChunker) Name() string { return "no-chunk" }

func (c *NoChunkChunker) Chunk(_ context.Context, content string) ([]types.Cell, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	return []types.Cell{mkCell(0, content)}, nil
}

func init() {
	Register("default", func() Chunker { return &DefaultChunker{} })
	Register("markdown", func() Chunker { return &MarkdownChunker{Default: DefaultChunker{}} })
	Register("no-chunk", func() Chunker { return &NoChunkChunker{} })
}
