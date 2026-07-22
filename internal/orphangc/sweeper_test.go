package orphangc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

type mockLedger struct {
	entries []api.LedgerEntry
}

func (m *mockLedger) AppendLedger(_ context.Context, e api.LedgerEntry) string {
	m.entries = append(m.entries, e)
	return "led_test"
}

type mockMetadata struct {
	adapter.MetadataStore
	workspaces []types.Workspace
	memories   map[string]*types.Memory // keyed by memory ID
	cells      map[string][]types.Cell  // keyed by memory ID
}

func (m *mockMetadata) ListWorkspaces(_ context.Context, _ int) ([]types.Workspace, error) {
	return m.workspaces, nil
}

func (m *mockMetadata) ListWorkspacesPaged(_ context.Context, cursor string, limit int) ([]types.Workspace, string, error) {
	if limit <= 0 {
		limit = 1000
	}
	var start int
	if cursor != "" {
		for i, ws := range m.workspaces {
			if ws.ID == cursor {
				start = i + 1
				break
			}
		}
	}
	if start >= len(m.workspaces) {
		return nil, "", nil
	}
	end := start + limit
	if end > len(m.workspaces) {
		end = len(m.workspaces)
	}
	page := m.workspaces[start:end]
	nextCursor := ""
	if end < len(m.workspaces) {
		nextCursor = page[len(page)-1].ID
	}
	return page, nextCursor, nil
}

func (m *mockMetadata) GetMemory(_ context.Context, id string) (*types.Memory, error) {
	if mem, ok := m.memories[id]; ok {
		return mem, nil
	}
	return nil, types.ErrNotFound
}

func (m *mockMetadata) GetCells(_ context.Context, memoryID string) ([]types.Cell, error) {
	if m.cells != nil {
		return m.cells[memoryID], nil
	}
	return nil, nil
}

type mockContent struct {
	adapter.ContentStore
	keys        map[string][]string  // workspace → memory IDs
	createdAt   map[string]time.Time // "ws:mem" → created_at
	deleted     map[string]bool      // "ws:mem" → deleted
	cellKeys    map[string][]string  // "ws:mem" → cell IDs
	cellDeleted map[string]bool      // "ws:mem:cell" → deleted
}

func (m *mockContent) ListMemoryIDs(_ context.Context, workspaceID string) ([]string, error) {
	return m.keys[workspaceID], nil
}

func (m *mockContent) ListMemoryIDsOlderThan(_ context.Context, workspaceID string, cutoff time.Time) ([]string, error) {
	var ids []string
	for _, memID := range m.keys[workspaceID] {
		key := workspaceID + ":" + memID
		if ca, ok := m.createdAt[key]; ok {
			if ca.Before(cutoff) {
				ids = append(ids, memID)
			}
		} else {
			ids = append(ids, memID)
		}
	}
	return ids, nil
}

func (m *mockContent) DeleteAllForMemory(_ context.Context, workspaceID, memoryID string) error {
	if m.deleted == nil {
		m.deleted = map[string]bool{}
	}
	m.deleted[workspaceID+":"+memoryID] = true
	return nil
}

func (m *mockContent) ListCellIDs(_ context.Context, workspaceID, memoryID string) ([]string, error) {
	if m.cellKeys != nil {
		return m.cellKeys[workspaceID+":"+memoryID], nil
	}
	return nil, nil
}

func (m *mockContent) DeleteCellContent(_ context.Context, workspaceID, memoryID, cellID string) error {
	if m.cellDeleted == nil {
		m.cellDeleted = map[string]bool{}
	}
	m.cellDeleted[workspaceID+":"+memoryID+":"+cellID] = true
	return nil
}

func TestSweeper_NilStoresSkip(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	sw := New(nil, nil, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sw.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	sw.Stop()

	if len(led.entries) != 0 {
		t.Fatalf("expected no ledger entries with nil stores, got %d", len(led.entries))
	}
}

func TestSweeper_ReclaimsOrphans(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories: map[string]*types.Memory{
			"mem_live": {ID: "mem_live"},
		},
	}
	content := &mockContent{
		keys: map[string][]string{
			"ws1": {"mem_live", "mem_orphan_1", "mem_orphan_2"},
		},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sw.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()
	sw.Stop()

	if sw.LastDeleted() < 2 {
		t.Fatalf("LastDeleted = %d, want >= 2", sw.LastDeleted())
	}
	if !content.deleted["ws1:mem_orphan_1"] || !content.deleted["ws1:mem_orphan_2"] {
		t.Fatalf("expected both orphans deleted, got %v", content.deleted)
	}
	if content.deleted["ws1:mem_live"] {
		t.Fatal("live memory should not be deleted")
	}

	if len(led.entries) == 0 {
		t.Fatal("expected at least one ledger entry")
	}
	found := false
	for _, e := range led.entries {
		if e.Op == "orphan_gc" {
			if scanned, ok := e.Metadata["scanned"].(int); ok && scanned >= 3 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected orphan_gc ledger entry with scanned >= 3")
	}
}

func TestSweeper_EmitsLedgerEntry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories:   map[string]*types.Memory{},
	}
	content := &mockContent{
		keys: map[string][]string{"ws1": {}},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sw.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	sw.Stop()

	if len(led.entries) == 0 {
		t.Fatal("expected at least one ledger entry from the sweeper")
	}
	if led.entries[0].Op != "orphan_gc" {
		t.Fatalf("op = %q, want %q", led.entries[0].Op, "orphan_gc")
	}
}

func TestSweeper_Counters(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories:   map[string]*types.Memory{"mem1": {ID: "mem1"}},
	}
	content := &mockContent{
		keys: map[string][]string{"ws1": {"mem1"}},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sw.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	sw.Stop()

	if sw.TotalDeleted() != 0 {
		t.Fatalf("TotalDeleted = %d, want 0 (no orphans)", sw.TotalDeleted())
	}
	if sw.LastDeleted() != 0 {
		t.Fatalf("LastDeleted = %d, want 0", sw.LastDeleted())
	}
}

func TestSweeper_DefaultConfig(t *testing.T) {
	sw := New(nil, nil, nil, slog.Default(), Config{})
	if sw.cfg.Interval != 5*time.Minute {
		t.Fatalf("default interval = %v, want 5m", sw.cfg.Interval)
	}
	if sw.cfg.MinAge != 1*time.Hour {
		t.Fatalf("default min_age = %v, want 1h", sw.cfg.MinAge)
	}
}

func TestSweeper_MinAgeProtectsYoungOrphans(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	now := time.Now()
	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories:   map[string]*types.Memory{},
	}
	content := &mockContent{
		keys: map[string][]string{"ws1": {"mem_young"}},
		createdAt: map[string]time.Time{
			"ws1:mem_young": now.Add(-1 * time.Minute),
		},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   1 * time.Hour,
	})
	sw.now = func() time.Time { return now }

	// Run one sweep directly.
	ctx := context.Background()
	sw.sweep(ctx)

	if content.deleted["ws1:mem_young"] {
		t.Fatal("young orphan should NOT be deleted (MinAge=1h, age=1m)")
	}
	if sw.LastDeleted() != 0 {
		t.Fatalf("LastDeleted = %d, want 0", sw.LastDeleted())
	}
}

func TestSweeper_MinAgeAllowsOldOrphans(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	now := time.Now()
	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories:   map[string]*types.Memory{},
	}
	content := &mockContent{
		keys: map[string][]string{"ws1": {"mem_old"}},
		createdAt: map[string]time.Time{
			"ws1:mem_old": now.Add(-2 * time.Hour),
		},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   1 * time.Hour,
	})
	sw.now = func() time.Time { return now }

	ctx := context.Background()
	sw.sweep(ctx)

	if !content.deleted["ws1:mem_old"] {
		t.Fatal("old orphan should be deleted (MinAge=1h, age=2h)")
	}
	if sw.LastDeleted() != 1 {
		t.Fatalf("LastDeleted = %d, want 1", sw.LastDeleted())
	}
}

func TestSweeper_PaginatesWorkspaces(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	workspaces := make([]types.Workspace, 1001)
	keys := make(map[string][]string)
	for i := range workspaces {
		wsID := fmt.Sprintf("ws_%04d", i)
		workspaces[i] = types.Workspace{ID: wsID}
		keys[wsID] = []string{fmt.Sprintf("orphan_%04d", i)}
	}

	meta := &mockMetadata{
		workspaces: workspaces,
		memories:   map[string]*types.Memory{},
	}
	content := &mockContent{
		keys: keys,
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx := context.Background()
	sw.sweep(ctx)

	if sw.LastDeleted() != 1001 {
		t.Fatalf("LastDeleted = %d, want 1001 (all workspaces must be swept)", sw.LastDeleted())
	}
	// Verify last workspace's orphan was reclaimed.
	lastKey := "ws_1000:orphan_1000"
	if !content.deleted[lastKey] {
		t.Fatalf("workspace #1001 orphan not deleted — pagination truncated")
	}
}

func TestSweeper_CellLevelOrphanReclaimed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	led := &mockLedger{}

	meta := &mockMetadata{
		workspaces: []types.Workspace{{ID: "ws1"}},
		memories:   map[string]*types.Memory{"mem1": {ID: "mem1"}},
		cells: map[string][]types.Cell{
			"mem1": {{CellID: "cell_live", MemoryID: "mem1"}},
		},
	}
	content := &mockContent{
		keys:     map[string][]string{"ws1": {"mem1"}},
		cellKeys: map[string][]string{"ws1:mem1": {"cell_live", "cell_orphan"}},
	}

	sw := New(meta, content, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx := context.Background()
	sw.sweep(ctx)

	if content.cellDeleted["ws1:mem1:cell_live"] {
		t.Fatal("live cell should NOT be deleted")
	}
	if !content.cellDeleted["ws1:mem1:cell_orphan"] {
		t.Fatal("orphan cell should be deleted")
	}
	if sw.LastDeleted() != 1 {
		t.Fatalf("LastDeleted = %d, want 1 (one orphan cell)", sw.LastDeleted())
	}
}
