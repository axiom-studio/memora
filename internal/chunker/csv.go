package chunker

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

// CSVChunker splits CSV content into cells of N data rows each,
// optionally prefixing the header row to every cell.
type CSVChunker struct {
	RowsPerCell int
	HasHeader   bool
	Delimiter   rune
}

func (c *CSVChunker) Name() string { return "csv" }

func (c *CSVChunker) defaults() {
	if c.RowsPerCell <= 0 {
		c.RowsPerCell = 1
	}
	if c.Delimiter == 0 {
		c.Delimiter = ','
	}
}

func (c *CSVChunker) Configure(opts map[string]string) error {
	for k, v := range opts {
		switch k {
		case "rows_per_cell":
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
				return fmt.Errorf("rows_per_cell must be an integer >= 1, got %q", v)
			}
			c.RowsPerCell = n
		case "has_header":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				c.HasHeader = true
			case "false", "0", "no":
				c.HasHeader = false
			default:
				return fmt.Errorf("has_header must be a boolean, got %q", v)
			}
		case "delimiter":
			runes := []rune(v)
			if len(runes) != 1 {
				return fmt.Errorf("delimiter must be a single character, got %q", v)
			}
			c.Delimiter = runes[0]
		}
	}
	return nil
}

func (c *CSVChunker) Chunk(_ context.Context, content string) ([]types.Cell, error) {
	c.defaults()
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	r := csv.NewReader(strings.NewReader(content))
	r.Comma = c.Delimiter
	r.FieldsPerRecord = -1 // allow ragged rows
	r.LazyQuotes = true

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv parse: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	var header []string
	dataStart := 0
	if c.HasHeader {
		header = records[0]
		dataStart = 1
	}

	data := records[dataStart:]
	if len(data) == 0 {
		if c.HasHeader {
			return []types.Cell{mkCell(0, formatRows([][]string{header}, c.Delimiter))}, nil
		}
		return nil, nil
	}

	var cells []types.Cell
	for i := 0; i < len(data); i += c.RowsPerCell {
		end := i + c.RowsPerCell
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		var rows [][]string
		if c.HasHeader {
			rows = append(rows, header)
		}
		rows = append(rows, chunk...)
		cells = append(cells, mkCell(len(cells), formatRows(rows, c.Delimiter)))
	}
	return cells, nil
}

func formatRows(rows [][]string, delim rune) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = delim
	for _, row := range rows {
		w.Write(row)
	}
	w.Flush()
	return strings.TrimRight(b.String(), "\n")
}

func init() {
	Register("csv", func() Chunker {
		return &CSVChunker{RowsPerCell: 1, HasHeader: true, Delimiter: ','}
	})
}
