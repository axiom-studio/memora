package orphangc

import (
	"context"
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
}

func (m *mockMetadata) ListWorkspaces(_ context.Context, _ int) ([]types.Workspace, error) {
	return m.workspaces, nil
}

func (m *mockMetadata) GetMemory(_ context.Context, id string) (*types.Memory, error) {
	if mem, ok := m.memories[id]; ok {
		return mem, nil
	}
	return nil, types.ErrNotFound
}

type mockContent struct {
	adapter.ContentStore
	keys    map[string][]string // workspace → memory IDs
	deleted map[string]bool     // "ws:mem" → deleted
}

func (m *mockContent) ListMemoryIDs(_ context.Context, workspaceID string) ([]string, error) {
	return m.keys[workspaceID], nil
}

func (m *mockContent) DeleteAllForMemory(_ context.Context, workspaceID, memoryID string) error {
	if m.deleted == nil {
		m.deleted = map[string]bool{}
	}
	m.deleted[workspaceID+":"+memoryID] = true
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
