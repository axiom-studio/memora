package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/adapter/compliance"
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

	ws := types.NewID()
	entry := api.LedgerEntry{
		WorkspaceID: ws,
		Op:          "memory.imprint",
		Target:      "mem_test",
		AgentID:     "434becf0-b125-41d5-8379-5a945dc8fb5e",
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
	if entries[0].Op != "memory.imprint" || entries[0].AgentID != "434becf0-b125-41d5-8379-5a945dc8fb5e" {
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

	ws := types.NewID()
	entries := []api.LedgerEntry{
		{WorkspaceID: ws, Op: "memory.imprint", AgentID: "8a8bb7cd-343a-42ad-99b7-d762030857a2", Timestamp: time.Now().UTC()},
		{WorkspaceID: ws, Op: "memory.update", AgentID: "8a8bb7cd-343a-42ad-99b7-d762030857a2", Timestamp: time.Now().UTC()},
		{WorkspaceID: ws, Op: "memory.forget", AgentID: "8a8bb7cd-343a-42ad-99b7-d762030857a2", Timestamp: time.Now().UTC()},
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

	ws := types.NewID()
	entry := api.LedgerEntry{
		WorkspaceID: ws,
		Op:          "memory.imprint",
		AgentID:     "f7f8a524-d304-44b3-a645-3b3827fcadeb",
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

	ws := types.NewID()
	entries := []api.LedgerEntry{
		{WorkspaceID: ws, Op: "memory.imprint", AgentID: "8a8bb7cd-343a-42ad-99b7-d762030857a2"},
		{WorkspaceID: ws, Op: "memory.update", AgentID: "693a9fdd-4c2f-4070-8968-fba0d07ff3c0"},
	}
	s.AppendBatch(ctx, entries)

	got, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, AgentID: "8a8bb7cd-343a-42ad-99b7-d762030857a2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentID != "8a8bb7cd-343a-42ad-99b7-d762030857a2" {
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

func TestStore_CursorPagination_TiedTimestamps(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	s := &Store{}
	if err := s.Open(context.Background(), adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	compliance.LedgerCursorContract(t, s)
}

// TestStore_RequestIDRoundTrip (#6156): request_id survives the postgres
// ledger's write and read paths.
func TestStore_RequestIDRoundTrip(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()
	s := &Store{}
	if err := s.Open(ctx, adapter.LedgerConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ws := types.NewID()
	if err := s.Append(ctx, api.LedgerEntry{WorkspaceID: ws, Op: "imprint", AgentID: types.NewID(), RequestID: "qa-trace-1"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws})
	if err != nil || len(got) != 1 || got[0].RequestID != "qa-trace-1" {
		t.Fatalf("entries = %+v (%v), want request_id qa-trace-1", got, err)
	}
}
