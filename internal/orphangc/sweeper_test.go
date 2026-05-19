package orphangc

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

type mockLedger struct {
	entries []api.LedgerEntry
}

func (m *mockLedger) AppendLedger(_ context.Context, e api.LedgerEntry) string {
	m.entries = append(m.entries, e)
	return "led_test"
}

func TestSweeper_EmitsLedgerEntry(t *testing.T) {
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

	sw := New(nil, nil, led, logger, Config{
		Interval: 50 * time.Millisecond,
		MinAge:   0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sw.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	sw.Stop()

	if sw.TotalDeleted() != 0 {
		t.Fatalf("TotalDeleted = %d, want 0 (no orphans in this test)", sw.TotalDeleted())
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
