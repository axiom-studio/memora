package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func openLedger(t *testing.T) *Store {
	t.Helper()
	s := &Store{}
	dsn := filepath.Join(t.TempDir(), "ledger.db")
	if err := s.Open(context.Background(), adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openLedgerAt(t *testing.T, dsn string) *Store {
	t.Helper()
	s := &Store{}
	if err := s.Open(context.Background(), adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func TestAppend_DurableAcrossReopen(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "ledger.db")
	ctx := context.Background()

	s1 := openLedgerAt(t, dsn)
	err := s1.Append(ctx, api.LedgerEntry{
		LedgerID:    "lg_durable",
		WorkspaceID: "ws_a",
		Op:          "imprint",
		AgentID:     "agent_a",
		Timestamp:   time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2 := openLedgerAt(t, dsn)
	defer s2.Close()
	entries, _, err := s2.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry after reopen, got %d", len(entries))
	}
	if entries[0].LedgerID != "lg_durable" {
		t.Errorf("want lg_durable, got %s", entries[0].LedgerID)
	}
}

func TestAppendBatch_RollsBackOnError(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	err := s.AppendBatch(ctx, []api.LedgerEntry{
		{LedgerID: "lg_ok", WorkspaceID: "ws_a", Op: "imprint", AgentID: "agent_a", Timestamp: time.Now().UTC()},
		{LedgerID: "lg_ok", WorkspaceID: "ws_a", Op: "update", AgentID: "agent_a", Timestamp: time.Now().UTC()},
	})
	if err == nil {
		t.Fatal("expected PK violation error, got nil")
	}

	entries, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want 0 entries after rollback, got %d", len(entries))
	}
}

func TestQuery_FilterByOp(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	for i, op := range []string{"imprint", "imprint", "update", "update", "forget"} {
		_ = s.Append(ctx, api.LedgerEntry{
			LedgerID: fmt.Sprintf("lg_%d", i), WorkspaceID: "ws_a",
			Op: op, AgentID: "agent_a", Timestamp: time.Now().UTC(),
		})
	}

	entries, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a", Op: []string{"imprint"}})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 imprint entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Op != "imprint" {
			t.Errorf("want op=imprint, got %s", e.Op)
		}
	}
}

func TestQuery_FilterByTimeRange(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		_ = s.Append(ctx, api.LedgerEntry{
			LedgerID: fmt.Sprintf("lg_%d", i), WorkspaceID: "ws_a",
			Op: "imprint", AgentID: "agent_a", Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}

	since := base.Add(1 * time.Hour)
	until := base.Add(3 * time.Hour)
	entries, _, err := s.Query(ctx, adapter.LedgerQuery{
		WorkspaceID: "ws_a", Since: &since, Until: &until,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries in [1h,3h] range, got %d", len(entries))
	}
}

func TestQuery_CursorPagination(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		_ = s.Append(ctx, api.LedgerEntry{
			LedgerID: fmt.Sprintf("lg_%04d", i), WorkspaceID: "ws_a",
			Op: "imprint", AgentID: "agent_a", Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	page1, cursor1, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a", Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 want 2, got %d", len(page1))
	}
	if cursor1 == "" {
		t.Fatal("expected non-empty cursor after page1")
	}
	if page1[0].LedgerID != "lg_0004" {
		t.Errorf("page1[0] want lg_0004 (newest), got %s", page1[0].LedgerID)
	}
	if page1[1].LedgerID != "lg_0003" {
		t.Errorf("page1[1] want lg_0003, got %s", page1[1].LedgerID)
	}

	page2, cursor2, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a", Limit: 2, SinceLedgerID: cursor1})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 want 2, got %d", len(page2))
	}
	if page2[0].LedgerID != "lg_0002" {
		t.Errorf("page2[0] want lg_0002, got %s", page2[0].LedgerID)
	}
	if page2[1].LedgerID != "lg_0001" {
		t.Errorf("page2[1] want lg_0001, got %s", page2[1].LedgerID)
	}

	page3, cursor3, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a", Limit: 2, SinceLedgerID: cursor2})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 want 1 (last entry), got %d", len(page3))
	}
	if page3[0].LedgerID != "lg_0000" {
		t.Errorf("page3[0] want lg_0000 (oldest), got %s", page3[0].LedgerID)
	}
	if cursor3 != "" {
		t.Errorf("page3 should have empty cursor (last page), got %s", cursor3)
	}
}

func TestRedact_ReplacesButDoesNotDelete(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	_ = s.Append(ctx, api.LedgerEntry{
		LedgerID: "lg_redact", WorkspaceID: "ws_a", Op: "imprint", AgentID: "agent_a",
		Timestamp: time.Now().UTC(), Metadata: map[string]any{"pii": "alice@example.com"},
	})
	if err := s.Redact(ctx, "lg_redact", []string{"metadata"}); err != nil {
		t.Fatalf("redact: %v", err)
	}

	entries, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("row was deleted by redact, want preserved; got %d", len(entries))
	}
	if !entries[0].Redacted {
		t.Error("entry should be marked redacted")
	}
	if v, ok := entries[0].Metadata["_redacted"]; !ok || v != "[REDACTED]" {
		t.Errorf("metadata not replaced: %+v", entries[0].Metadata)
	}
}

func TestRedact_HonorsAllFieldNames(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	_ = s.Append(ctx, api.LedgerEntry{
		LedgerID: "lg_full", WorkspaceID: "ws_a", Op: "imprint", AgentID: "agent_a",
		Timestamp: time.Now().UTC(), IP: "1.2.3.4", UserAgent: "curl/7.0",
		APIKeyID: "key_123", UserID: "user_456", Target: "mem_abc",
		WatermarkBefore: "wmk_old", WatermarkAfter: "wmk_new", RequestID: "req_789",
		Metadata: map[string]any{"secret": "s3cr3t"},
	})

	fields := []string{"ip", "user_agent", "api_key_id", "user_id", "target",
		"watermark_before", "watermark_after", "request_id", "metadata"}
	if err := s.Redact(ctx, "lg_full", fields); err != nil {
		t.Fatalf("redact: %v", err)
	}

	entries, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1, got %d", len(entries))
	}
	e := entries[0]
	checks := map[string]string{
		"ip":               e.IP,
		"user_agent":       e.UserAgent,
		"api_key_id":       e.APIKeyID,
		"user_id":          e.UserID,
		"target":           e.Target,
		"watermark_before": e.WatermarkBefore,
		"watermark_after":  e.WatermarkAfter,
		"request_id":       e.RequestID,
	}
	for field, val := range checks {
		if val != "[REDACTED]" {
			t.Errorf("%s = %q, want [REDACTED]", field, val)
		}
	}
	if v, ok := e.Metadata["_redacted"]; !ok || v != "[REDACTED]" {
		t.Errorf("metadata not replaced: %+v", e.Metadata)
	}
}

func TestRedact_NotFound(t *testing.T) {
	s := openLedger(t)
	err := s.Redact(context.Background(), "lg_nonexistent", []string{"metadata"})
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

func TestQuery_TimestampRoundTrip(t *testing.T) {
	s := openLedger(t)
	ctx := context.Background()

	ts := time.Date(2026, 5, 18, 14, 30, 45, 123456789, time.UTC)
	_ = s.Append(ctx, api.LedgerEntry{
		LedgerID: "lg_ts", WorkspaceID: "ws_a", Op: "imprint",
		AgentID: "agent_a", Timestamp: ts,
	})

	entries, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: "ws_a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1, got %d", len(entries))
	}
	if !entries[0].Timestamp.Equal(ts) {
		t.Errorf("timestamp mismatch: want %v, got %v", ts, entries[0].Timestamp)
	}
}

func TestCapabilities(t *testing.T) {
	s := openLedger(t)
	caps := s.Capabilities()
	if !caps.SupportsAppend || !caps.SupportsBatchAppend || !caps.SupportsQuery || !caps.SupportsRedaction || !caps.DurableOnAppend {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
}
