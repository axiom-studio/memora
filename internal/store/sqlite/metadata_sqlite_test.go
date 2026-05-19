package sqlite

import (
	"context"
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

	list, err := ms.ListWorkspaces(ctx, 100)
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
		WrittenByAgentID: "agent",
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
	m := &types.Memory{WorkspaceID: ws.ID, Content: "c", WrittenByAgentID: "agent"}
	_, _ = ms.ImprintMemory(ctx, m)

	cells := []types.Cell{
		{CellID: types.NewID(types.CellIDPrefix), MemoryID: m.ID, Seq: 0, Text: "chunk text", TextMD5: "abc123", WrittenByAgentID: "agent"},
	}
	if err := ms.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatalf("UpsertCells: %v", err)
	}

	got, err := ms.GetCells(ctx, m.ID)
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
		WrittenByAgentID: "agent",
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

	agentID := types.NewID(types.AgentIDPrefix)
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
