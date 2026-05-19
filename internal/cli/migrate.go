package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// MigrateContentConfig holds the options for a content migration.
type MigrateContentConfig struct {
	WorkspaceID  string
	CollectionID string
	ResumeFrom   string // memory ID to resume from (lexicographic)
	DryRun       bool
	Verify       bool
	MaxRate      int // memories per second (0 = unlimited)
	Out          io.Writer
}

// MigrateContentResult is the final output of a migration pass.
type MigrateContentResult struct {
	Event       string `json:"event"`
	Total       int    `json:"total"`
	Migrated    int    `json:"migrated"`
	Skipped     int    `json:"skipped"`
	Errors      int    `json:"errors"`
	Mismatches  int    `json:"mismatches,omitempty"`
	ResumeToken string `json:"resume_token,omitempty"`
}

// MigrateContent copies memory and cell content from the MetadataStore
// legacy columns to the ContentStore. Idempotent — only missing entries
// are written. Returns the result summary.
func MigrateContent(ctx context.Context, primary adapter.MetadataStore, content adapter.ContentStore, cfg MigrateContentConfig) (*MigrateContentResult, error) {
	if content == nil {
		return nil, fmt.Errorf("content store is nil — configure a content driver first")
	}

	const pageSize = 1000
	var mems []types.Memory
	for {
		page, err := primary.ListMemories(ctx, cfg.WorkspaceID, cfg.CollectionID, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list memories: %w", err)
		}
		mems = append(mems, page...)
		if len(page) < pageSize {
			break
		}
	}

	if cfg.ResumeFrom != "" {
		filtered := make([]types.Memory, 0, len(mems))
		found := false
		for _, m := range mems {
			if m.ID == cfg.ResumeFrom {
				found = true
				continue
			}
			if found {
				filtered = append(filtered, m)
			}
		}
		if !found {
			return nil, fmt.Errorf("resume_from %q not found in workspace memories", cfg.ResumeFrom)
		}
		mems = filtered
	}

	result := &MigrateContentResult{Total: len(mems)}
	var rateLimiter <-chan time.Time
	if cfg.MaxRate > 0 {
		ticker := time.NewTicker(time.Second / time.Duration(cfg.MaxRate))
		defer ticker.Stop()
		rateLimiter = ticker.C
	}

	for i, m := range mems {
		if ctx.Err() != nil {
			result.Event = "interrupted"
			result.ResumeToken = m.ID
			return result, nil
		}

		if rateLimiter != nil {
			select {
			case <-rateLimiter:
			case <-ctx.Done():
				result.Event = "interrupted"
				result.ResumeToken = m.ID
				return result, nil
			}
		}

		if cfg.Verify {
			err := verifyMemoryContent(ctx, primary, content, m)
			if err != nil {
				result.Mismatches++
				emitProgress(cfg.Out, "mismatch", i+1, result, m.ID, err.Error())
			}
			result.Skipped++
			continue
		}

		if cfg.DryRun {
			result.Skipped++
			if (i+1)%100 == 0 {
				emitProgress(cfg.Out, "progress", i+1, result, m.ID, "")
			}
			continue
		}

		// Migrate memory content.
		if m.Content != "" {
			if err := content.PutMemoryContent(ctx, m.WorkspaceID, m.ID, m.ContentMD5, m.Content); err != nil {
				result.Errors++
				emitProgress(cfg.Out, "error", i+1, result, m.ID, err.Error())
				continue
			}
		}

		// Migrate cell content.
		cellFailed := false
		cells, err := primary.GetCells(ctx, m.ID)
		if err == nil {
			for _, cell := range cells {
				if cell.Text != "" {
					if cerr := content.PutCellContent(ctx, m.WorkspaceID, m.ID, cell.CellID, cell.TextMD5, cell.Text); cerr != nil {
						cellFailed = true
						result.Errors++
						emitProgress(cfg.Out, "cell_error", i+1, result, m.ID, fmt.Sprintf("cell %s: %v", cell.CellID, cerr))
					}
				}
			}
		}

		if cellFailed {
			continue
		}
		result.Migrated++
		if (i+1)%100 == 0 {
			emitProgress(cfg.Out, "progress", i+1, result, m.ID, "")
		}
	}

	result.Event = "complete"
	return result, nil
}

func verifyMemoryContent(ctx context.Context, primary adapter.MetadataStore, content adapter.ContentStore, m types.Memory) error {
	if m.Content != "" {
		got, err := content.GetMemoryContent(ctx, m.WorkspaceID, m.ID)
		if err != nil {
			return fmt.Errorf("memory not in content store: %w", err)
		}
		if got != m.Content {
			return fmt.Errorf("memory content mismatch: primary md5=%s, content store has different bytes", m.ContentMD5)
		}
	}
	cells, err := primary.GetCells(ctx, m.ID)
	if err != nil {
		return nil
	}
	for _, cell := range cells {
		if cell.Text == "" {
			continue
		}
		got, err := content.GetCellContent(ctx, m.WorkspaceID, m.ID, cell.CellID)
		if err != nil {
			return fmt.Errorf("cell %s not in content store: %w", cell.CellID, err)
		}
		if got != cell.Text {
			return fmt.Errorf("cell %s content mismatch: md5=%s", cell.CellID, cell.TextMD5)
		}
	}
	return nil
}

func emitProgress(w io.Writer, event string, scanned int, r *MigrateContentResult, memID, errMsg string) {
	if w == nil {
		return
	}
	p := map[string]any{
		"event":             event,
		"scanned":           scanned,
		"migrated":          r.Migrated,
		"skipped":           r.Skipped,
		"errors":            r.Errors,
		"current_memory_id": memID,
		"ts":                time.Now().UTC().Format(time.RFC3339),
	}
	if errMsg != "" {
		p["error"] = errMsg
	}
	b, _ := json.Marshal(p)
	fmt.Fprintln(w, string(b))
}
