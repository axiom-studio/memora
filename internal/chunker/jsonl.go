package chunker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

// JSONLChunker splits JSON-lines (NDJSON) content into cells of N lines each.
type JSONLChunker struct {
	LinesPerCell int
	Validate     bool
}

func (c *JSONLChunker) Name() string { return "jsonl" }

func (c *JSONLChunker) defaults() {
	if c.LinesPerCell <= 0 {
		c.LinesPerCell = 1
	}
}

func (c *JSONLChunker) Configure(opts map[string]string) error {
	for k, v := range opts {
		switch k {
		case "lines_per_cell":
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
				return fmt.Errorf("lines_per_cell must be an integer >= 1, got %q", v)
			}
			c.LinesPerCell = n
		case "validate":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				c.Validate = true
			case "false", "0", "no":
				c.Validate = false
			default:
				return fmt.Errorf("validate must be a boolean, got %q", v)
			}
		}
	}
	return nil
}

func (c *JSONLChunker) Chunk(_ context.Context, content string) ([]types.Cell, error) {
	c.defaults()
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	raw := strings.Split(content, "\n")
	var lines []string
	for lineNo, l := range raw {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if c.Validate && !json.Valid([]byte(l)) {
			return nil, fmt.Errorf("invalid JSON on line %d: %s", lineNo+1, truncate(l, 80))
		}
		lines = append(lines, l)
	}

	if len(lines) == 0 {
		return nil, nil
	}

	var cells []types.Cell
	for i := 0; i < len(lines); i += c.LinesPerCell {
		end := i + c.LinesPerCell
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.Join(lines[i:end], "\n")
		cells = append(cells, mkCell(len(cells), text))
	}
	return cells, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func init() {
	Register("jsonl", func() Chunker {
		return &JSONLChunker{LinesPerCell: 1, Validate: true}
	})
}
