package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"

	_ "github.com/axiom-studio/memora/internal/store/sqlite"
)

func openStores(t *testing.T) (adapter.MetadataStore, adapter.ContentStore, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	ctx := context.Background()

	primary, err := adapter.OpenMetadata(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	content, err := adapter.OpenContent(ctx, adapter.ContentConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open content: %v", err)
	}
	t.Cleanup(func() { _ = content.Close() })

	return primary, content, ctx
}

func TestMigrateContent_DryRun(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-dryrun"}
	if err := primary.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("create ws: %v", err)
	}
	for i := 0; i < 5; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "test content", WrittenByAgentID: "agent"}
		if _, err := primary.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("imprint: %v", err)
		}
	}

	var buf bytes.Buffer
	result, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		DryRun:      true,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.Event != "complete" {
		t.Fatalf("event = %q, want complete", result.Event)
	}
	if result.Total != 5 {
		t.Fatalf("total = %d, want 5", result.Total)
	}
	if result.Migrated != 0 {
		t.Fatalf("migrated = %d, want 0 (dry run)", result.Migrated)
	}
	if result.Skipped != 5 {
		t.Fatalf("skipped = %d, want 5", result.Skipped)
	}
}

func TestMigrateContent_CommitAndVerify(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-commit"}
	_ = primary.CreateWorkspace(ctx, ws)

	var memIDs []string
	for i := 0; i < 10; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "content " + string(rune('A'+i)), WrittenByAgentID: "agent"}
		_, _ = primary.ImprintMemory(ctx, m)
		memIDs = append(memIDs, m.ID)
	}

	// Commit migration.
	var buf bytes.Buffer
	result, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("migrate commit: %v", err)
	}
	if result.Migrated != 10 {
		t.Fatalf("migrated = %d, want 10", result.Migrated)
	}

	// Verify round-trip.
	for _, id := range memIDs {
		m, _ := primary.GetMemory(ctx, id)
		got, err := content.GetMemoryContent(ctx, ws.ID, id)
		if err != nil {
			t.Fatalf("get content for %s: %v", id, err)
		}
		if got != m.Content {
			t.Fatalf("content mismatch for %s", id)
		}
	}

	// Verify mode.
	result, err = MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		Verify:      true,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Mismatches != 0 {
		t.Fatalf("mismatches = %d, want 0", result.Mismatches)
	}
}

func TestMigrateContent_Idempotent(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-idempotent"}
	_ = primary.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "idempotent test", WrittenByAgentID: "agent"}
	_, _ = primary.ImprintMemory(ctx, m)

	// Run twice.
	for i := 0; i < 2; i++ {
		_, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
			WorkspaceID: ws.ID,
		})
		if err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	got, _ := content.GetMemoryContent(ctx, ws.ID, m.ID)
	if got != "idempotent test" {
		t.Fatalf("content = %q, want %q", got, "idempotent test")
	}
}

func TestMigrateContent_ResumeFrom(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-resume"}
	_ = primary.CreateWorkspace(ctx, ws)

	var memIDs []string
	for i := 0; i < 5; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "content", WrittenByAgentID: "agent"}
		_, _ = primary.ImprintMemory(ctx, m)
		memIDs = append(memIDs, m.ID)
	}

	// Migrate from the 3rd memory onwards.
	result, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		ResumeFrom:  memIDs[2],
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// Should migrate memories after the resume token.
	if result.Total < 1 {
		t.Fatalf("total = %d, want >= 1 (memories after resume token)", result.Total)
	}
}

func TestMigrateContent_ProgressNDJSON(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-progress"}
	_ = primary.CreateWorkspace(ctx, ws)
	for i := 0; i < 150; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "c", WrittenByAgentID: "agent"}
		_, _ = primary.ImprintMemory(ctx, m)
	}

	var buf bytes.Buffer
	_, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Should have at least one progress line.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected progress output")
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("invalid NDJSON: %v", err)
	}
	if first["event"] != "progress" {
		t.Fatalf("event = %q, want %q", first["event"], "progress")
	}
}

func TestMigrateContent_ResumeFromTypoErrors(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-resume-typo"}
	_ = primary.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "x", WrittenByAgentID: "agent"}
	_, _ = primary.ImprintMemory(ctx, m)

	_, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		ResumeFrom:  "mem_nonexistent_typo",
	})
	if err == nil {
		t.Fatal("expected error for typo'd resume-from, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found' substring", err.Error())
	}
}

func TestMigrateContent_CellWriteFailureCounted(t *testing.T) {
	primary, _, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-cellfail"}
	_ = primary.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "cell test", WrittenByAgentID: "agent"}
	_, _ = primary.ImprintMemory(ctx, m)

	cells := []types.Cell{
		{CellID: "cell_001", MemoryID: m.ID, Text: "chunk one", TextMD5: "abc", WrittenByAgentID: "agent"},
		{CellID: "cell_002", MemoryID: m.ID, Text: "chunk two", TextMD5: "def", WrittenByAgentID: "agent"},
	}
	if err := primary.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatalf("upsert cells: %v", err)
	}

	failing := &failingCellContent{}
	var buf bytes.Buffer
	result, err := MigrateContent(ctx, primary, failing, MigrateContentConfig{
		WorkspaceID: ws.ID,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.Migrated != 0 {
		t.Fatalf("migrated = %d, want 0 (cell write failed)", result.Migrated)
	}
	if result.Errors == 0 {
		t.Fatal("expected errors > 0 for cell write failures")
	}
}

type failingCellContent struct {
	adapter.ContentStore
}

func (f *failingCellContent) PutMemoryContent(context.Context, string, string, string, string) error {
	return nil
}
func (f *failingCellContent) PutCellContent(context.Context, string, string, string, string, string) error {
	return fmt.Errorf("injected cell write failure")
}
func (f *failingCellContent) GetMemoryContent(context.Context, string, string) (string, error) {
	return "", types.ErrNotFound
}
func (f *failingCellContent) GetCellContent(context.Context, string, string, string) (string, error) {
	return "", types.ErrNotFound
}
func (f *failingCellContent) Capabilities() adapter.ContentCapabilities {
	return adapter.ContentCapabilities{}
}

func TestMigrateContent_PaginationExceedsOnePage(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-pagination"}
	_ = primary.CreateWorkspace(ctx, ws)

	total := 10
	for i := 0; i < total; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: fmt.Sprintf("content-%d", i), WrittenByAgentID: "agent"}
		_, _ = primary.ImprintMemory(ctx, m)
	}

	result, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		PageSize:    3,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.Total != total {
		t.Fatalf("total = %d, want %d (pagination should fetch all pages)", result.Total, total)
	}
	if result.Migrated != total {
		t.Fatalf("migrated = %d, want %d", result.Migrated, total)
	}
}

func TestMigrateContent_VerifyChecksCells(t *testing.T) {
	primary, content, ctx := openStores(t)

	ws := &types.Workspace{Name: "migrate-verify-cells"}
	_ = primary.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "verify cells", WrittenByAgentID: "agent"}
	_, _ = primary.ImprintMemory(ctx, m)

	// Migrate memory content only (not cells) by using PutMemoryContent directly.
	_ = content.PutMemoryContent(ctx, ws.ID, m.ID, m.ContentMD5, m.Content)

	// Verify should detect missing cell content if cells exist.
	var buf bytes.Buffer
	result, err := MigrateContent(ctx, primary, content, MigrateContentConfig{
		WorkspaceID: ws.ID,
		Verify:      true,
		Out:         &buf,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	cells, _ := primary.GetCells(ctx, m.ID)
	if len(cells) > 0 {
		if result.Mismatches == 0 {
			t.Fatal("expected mismatches > 0 when cell content is missing from content store")
		}
	}
}
