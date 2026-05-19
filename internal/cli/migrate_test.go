package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
