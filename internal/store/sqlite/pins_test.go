package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func TestPins_CreateListDelete_RoundTrip(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "pins"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	p := &types.Pin{WorkspaceID: ws.ID, Query: "q3 goals", Mode: "vector", K: 7, Watermark: "wm_1", Label: "q3", CreatedBy: "agent_a"}
	if err := s.CreatePin(ctx, p); err != nil {
		t.Fatalf("CreatePin: %v", err)
	}

	pins, err := s.ListPins(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("ListPins = %d pins, want 1", len(pins))
	}
	got := pins[0]
	if got.PinID != p.PinID || got.Query != "q3 goals" || got.Mode != "vector" || got.K != 7 ||
		got.Watermark != "wm_1" || got.Label != "q3" || got.CreatedBy != "agent_a" {
		t.Fatalf("ListPins round trip = %+v, want fields of %+v", got, p)
	}

	if err := s.DeletePin(ctx, p.PinID); err != nil {
		t.Fatalf("DeletePin: %v", err)
	}
	if pins, _ := s.ListPins(ctx, ws.ID); len(pins) != 0 {
		t.Fatalf("pins after delete = %+v, want none", pins)
	}
	if err := s.DeletePin(ctx, p.PinID); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("second DeletePin err = %v, want ErrNotFound", err)
	}
}

// TestPinsRebuildMigration_RepairsStaleTable reproduces a database migrated
// before 0005 — memora_pins still carrying 0001's query_spec schema — and
// checks that reopening rebuilds the table and keeps existing rows.
func TestPinsRebuildMigration_RepairsStaleTable(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "memora.db")
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("open: %v", err)
	}
	ws := &types.Workspace{Name: "legacy"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE memora_pins`,
		`CREATE TABLE memora_pins (pin_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, query_spec TEXT NOT NULL, watermark TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE INDEX idx_pins_workspace ON memora_pins(workspace_id)`,
		`DELETE FROM memora_schema_migrations WHERE version = '0005_pins_rebuild.sql'`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed stale schema (%s): %v", stmt, err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memora_pins VALUES ('pin_legacy', ?, 'legacy query', 'wm_old', '2026-05-01T00:00:00Z'),
		                               ('pin_orphan', 'ws_gone', 'orphan query', 'wm_old', '2026-05-01T00:00:00Z')`, ws.ID); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = &Store{}
	if err := s.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("reopen (applies 0005): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	pins, err := s.ListPins(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListPins after rebuild: %v", err)
	}
	if len(pins) != 1 || pins[0].PinID != "pin_legacy" || pins[0].Query != "legacy query" ||
		pins[0].Mode != "hybrid" || pins[0].K != 10 || pins[0].Watermark != "wm_old" {
		t.Fatalf("migrated pins = %+v, want pin_legacy with query copied and mode/k defaults", pins)
	}
	if err := s.CreatePin(ctx, &types.Pin{WorkspaceID: ws.ID, Query: "new", Mode: "hybrid", K: 10, Watermark: "wm_2"}); err != nil {
		t.Fatalf("CreatePin after rebuild: %v", err)
	}
	var orphans int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memora_pins WHERE pin_id = 'pin_orphan'`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("orphan pin rows = %d (err %v), want 0: rows for deleted workspaces cannot satisfy the FK", orphans, err)
	}
}
