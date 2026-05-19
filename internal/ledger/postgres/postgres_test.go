package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func skipWithoutPostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MEMORA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MEMORA_POSTGRES_DSN not set; skipping Postgres ledger integration test")
	}
	return dsn
}

func TestStore_Capabilities(t *testing.T) {
	s := &Store{}
	caps := s.Capabilities()
	if !caps.SupportsAppend || !caps.SupportsBatchAppend || !caps.SupportsQuery || !caps.SupportsRedaction {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.EstimatedAppendQPS != 10000 {
		t.Fatalf("expected 10000 QPS, got %d", caps.EstimatedAppendQPS)
	}
}

func TestStore_AppendAndQuery(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ws := "ws_ledger_test_" + types.NewID("t")
	entry := api.LedgerEntry{
		WorkspaceID: ws,
		Op:          "memory.imprint",
		Target:      "mem_test",
		AgentID:     "agent_test",
		IP:          "127.0.0.1",
		Metadata:    map[string]any{"key": "value"},
	}
	if err := s.Append(ctx, entry); err != nil {
		t.Fatal(err)
	}

	entries, cursor, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Op != "memory.imprint" || entries[0].AgentID != "agent_test" {
		t.Fatalf("entry mismatch: %+v", entries[0])
	}
	if cursor != "" {
		t.Fatalf("expected empty cursor for 1 entry, got %q", cursor)
	}
}

func TestStore_AppendBatch(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ws := "ws_batch_test_" + types.NewID("t")
	entries := []api.LedgerEntry{
		{WorkspaceID: ws, Op: "memory.imprint", AgentID: "a1", Timestamp: time.Now().UTC()},
		{WorkspaceID: ws, Op: "memory.update", AgentID: "a1", Timestamp: time.Now().UTC()},
		{WorkspaceID: ws, Op: "memory.forget", AgentID: "a1", Timestamp: time.Now().UTC()},
	}
	if err := s.AppendBatch(ctx, entries); err != nil {
		t.Fatal(err)
	}

	got, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
}

func TestStore_Redact(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ws := "ws_redact_test_" + types.NewID("t")
	entry := api.LedgerEntry{
		WorkspaceID: ws,
		Op:          "memory.imprint",
		AgentID:     "agent_redact",
		IP:          "10.0.0.1",
		Metadata:    map[string]any{"secret": "data"},
	}
	if err := s.Append(ctx, entry); err != nil {
		t.Fatal(err)
	}

	entries, _, _ := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws})
	if len(entries) == 0 {
		t.Fatal("expected entry")
	}

	if err := s.Redact(ctx, entries[0].LedgerID, []string{"ip", "metadata"}); err != nil {
		t.Fatal(err)
	}

	got, _, _ := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws})
	if !got[0].Redacted {
		t.Fatal("expected redacted=true")
	}
	if got[0].IP != "[REDACTED]" {
		t.Fatalf("expected IP redacted, got %q", got[0].IP)
	}
}

func TestStore_QueryFilters(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ws := "ws_filter_test_" + types.NewID("t")
	entries := []api.LedgerEntry{
		{WorkspaceID: ws, Op: "memory.imprint", AgentID: "a1"},
		{WorkspaceID: ws, Op: "memory.update", AgentID: "a2"},
	}
	s.AppendBatch(ctx, entries)

	got, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, AgentID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentID != "a1" {
		t.Fatalf("agent filter failed: got %d entries", len(got))
	}

	got, _, err = s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, Op: []string{"memory.update"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Op != "memory.update" {
		t.Fatalf("op filter failed: got %d entries", len(got))
	}
}
