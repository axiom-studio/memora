package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func openMetadataStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, ctx
}

func TestMetadataStore_WorkspaceRoundtrip(t *testing.T) {
	ms, ctx := openMetadataStore(t)

	ws := &types.Workspace{Name: "meta-test"}
	if err := ms.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if ws.ID == "" {
		t.Fatal("workspace ID should be populated")
	}

	got, err := ms.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.Name != "meta-test" {
		t.Fatalf("name = %q, want %q", got.Name, "meta-test")
	}

	list, _, err := ms.ListWorkspaces(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
}

func TestMetadataStore_MemoryRoundtrip(t *testing.T) {
	ms, ctx := openMetadataStore(t)

	ws := &types.Workspace{Name: "mem-test"}
	if err := ms.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	m := &types.Memory{
		WorkspaceID:      ws.ID,
		Content:          "hello world",
		WrittenByAgentID: "b33aed8f-3134-4967-83dc-39f9a7c95783",
	}
	wmk, err := ms.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	if wmk == "" {
		t.Fatal("watermark should be populated")
	}

	got, err := ms.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.ID != m.ID {
		t.Fatalf("ID = %q, want %q", got.ID, m.ID)
	}
}

func TestMetadataStore_CellRoundtrip(t *testing.T) {
	ms, ctx := openMetadataStore(t)

	ws := &types.Workspace{Name: "cell-test"}
	_ = ms.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "c", WrittenByAgentID: "b33aed8f-3134-4967-83dc-39f9a7c95783"}
	_, _ = ms.ImprintMemory(ctx, m)

	cells := []types.Cell{
		{CellID: types.NewID(), MemoryID: m.ID, Seq: 0, Text: "chunk text", TextMD5: "abc123", WrittenByAgentID: "b33aed8f-3134-4967-83dc-39f9a7c95783"},
	}
	if err := ms.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatalf("UpsertCells: %v", err)
	}

	got, err := ms.GetCells(ctx, m.WorkspaceID, m.ID)
	if err != nil {
		t.Fatalf("GetCells: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestMetadataStore_TagRoundtrip(t *testing.T) {
	ms, ctx := openMetadataStore(t)

	ws := &types.Workspace{Name: "tag-test"}
	_ = ms.CreateWorkspace(ctx, ws)
	m := &types.Memory{
		WorkspaceID:      ws.ID,
		Content:          "c",
		Tags:             map[string]string{"env": "prod"},
		WrittenByAgentID: "b33aed8f-3134-4967-83dc-39f9a7c95783",
	}
	_, _ = ms.ImprintMemory(ctx, m)

	got, err := ms.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("tag env = %q, want %q", got.Tags["env"], "prod")
	}

	if err := ms.UpsertTag(ctx, ws.ID, m.ID, "region", "us"); err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	if err := ms.DeleteTag(ctx, ws.ID, m.ID, "region"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
}

func TestMetadataStore_AgentRoundtrip(t *testing.T) {
	ms, ctx := openMetadataStore(t)

	ws := &types.Workspace{Name: "agent-test"}
	_ = ms.CreateWorkspace(ctx, ws)

	agentID := types.NewID()
	a := &types.Agent{
		AgentID:          agentID,
		WorkspaceID:      ws.ID,
		IdentityProvider: "opaque",
	}
	if err := ms.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	got, err := ms.GetAgent(ctx, ws.ID, agentID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.AgentID != agentID {
		t.Fatalf("AgentID = %q, want %q", got.AgentID, agentID)
	}

	list, err := ms.ListAgents(ctx, ws.ID, 0)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	found := false
	for _, ag := range list {
		if ag.AgentID == agentID {
			found = true
		}
	}
	if !found {
		t.Fatal("agent not found in ListAgents")
	}
}

func TestMetadataStore_Capabilities(t *testing.T) {
	s := &Store{}
	caps := s.Capabilities()
	if !caps.SupportsCAS {
		t.Fatal("SupportsCAS should be true")
	}
	if !caps.SupportsTransactions {
		t.Fatal("SupportsTransactions should be true")
	}
}

// TestOpen_NormalizesLegacyCreatedAt: rows written before the fixed-width
// layout, in the driver's zone-dependent form, are rewritten at Open, and
// listing then orders them by instant rather than by text.
func TestOpen_NormalizesLegacyCreatedAt(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "memora.db")
	ctx := context.Background()
	s := &Store{}
	if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	ws := &types.Workspace{Name: "legacy"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	// The instants are 10:00, 10:00:00.25 and 11:00:00.5 UTC, but as text
	// the second ("15:30… +0530") sorts newest.
	legacy := []string{
		"2026-01-02 10:00:00 +0000 UTC",
		"2026-01-02 15:30:00.25 +0530 IST",
		"2026-01-02 11:00:00.5 +0000 UTC",
	}
	agent := types.NewID()
	var ids []string
	for _, raw := range legacy {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "legacy", WrittenByAgentID: agent}
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE memora_memories SET created_at = ? WHERE id = ?`, raw, m.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE memora_workspaces SET created_at = ? WHERE id = ?`, legacy[1], ws.ID); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s = &Store{}
	if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var bad int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM memora_memories WHERE length(created_at) != 30) +
		(SELECT count(*) FROM memora_workspaces WHERE length(created_at) != 30)`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("%d rows still in a legacy created_at form", bad)
	}
	mems, _, err := s.ListMemories(ctx, ws.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ids[2], ids[1], ids[0]}
	var got []string
	for _, m := range mems {
		got = append(got, m.ID)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
