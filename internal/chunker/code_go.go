package chunker

import (
	"context"
	"go/parser"
	"go/token"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

// GoChunker splits Go source into cells aligned to top-level declarations.
// Non-Go content falls back to DefaultChunker.
type GoChunker struct {
	Default DefaultChunker
}

func (c *GoChunker) Name() string { return "code-go" }

func (c *GoChunker) Chunk(ctx context.Context, content string) ([]types.Cell, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", content, parser.AllErrors)
	if err != nil || f == nil {
		return c.Default.Chunk(ctx, content)
	}

	lines := strings.Split(content, "\n")
	type span struct{ start, end int }
	var spans []span

	headerEnd := 0
	if len(f.Decls) > 0 {
		headerEnd = fset.Position(f.Decls[0].Pos()).Line - 1
	}
	if headerEnd > 0 {
		spans = append(spans, span{0, headerEnd - 1})
	}

	for _, decl := range f.Decls {
		start := fset.Position(decl.Pos()).Line - 1
		end := fset.Position(decl.End()).Line - 1
		if end >= len(lines) {
			end = len(lines) - 1
		}
		spans = append(spans, span{start, end})
	}

	var cells []types.Cell
	for _, s := range spans {
		text := strings.Join(lines[s.start:s.end+1], "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		cells = append(cells, mkCell(len(cells), text))
	}

	if len(cells) == 0 {
		return c.Default.Chunk(ctx, content)
	}
	return cells, nil
}

func init() {
	Register("code-go", func() Chunker { return &GoChunker{Default: DefaultChunker{}} })
}
