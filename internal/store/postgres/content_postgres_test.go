package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func skipWithoutPostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MEMORA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MEMORA_POSTGRES_DSN not set; skipping Postgres integration test")
	}
	return dsn
}

func TestContentStore_RoundTrip(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	cs := &ContentStore{}
	if err := cs.Open(ctx, adapter.ContentConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ws := "ws_test_content_" + types.NewID("t")
	mem := "mem_test_" + types.NewID("t")

	if err := cs.PutMemoryContent(ctx, ws, mem, "md5abc", "hello world"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetMemoryContent(ctx, ws, mem)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("expected 'hello world', got %q", got)
	}

	if err := cs.PutCellContent(ctx, ws, mem, "cell_1", "md5c1", "chunk one"); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutCellContent(ctx, ws, mem, "cell_2", "md5c2", "chunk two"); err != nil {
		t.Fatal(err)
	}
	batch, err := cs.GetCellContentBatch(ctx, ws, mem, []string{"cell_1", "cell_2", "cell_missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(batch))
	}

	ids, err := cs.ListMemoryIDs(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != mem {
		t.Fatalf("expected [%s], got %v", mem, ids)
	}

	if err := cs.DeleteAllForMemory(ctx, ws, mem); err != nil {
		t.Fatal(err)
	}
	_, err = cs.GetMemoryContent(ctx, ws, mem)
	if err != types.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMetadataStore_WorkspaceCRUD(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	w := &types.Workspace{
		Name:   "test-ws-" + types.NewID("t"),
		Region: "us-east",
	}
	if err := ms.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	if w.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := ms.GetWorkspace(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != w.Name || got.Region != "us-east" {
		t.Fatalf("workspace mismatch: %+v", got)
	}

	all, err := ms.ListWorkspaces(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ws := range all {
		if ws.ID == w.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("workspace not in list")
	}

	w.Name = "updated-name"
	if err := ms.UpdateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}

	if err := ms.DeleteWorkspace(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	_, err = ms.GetWorkspace(ctx, w.ID)
	if err != types.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMetadataStore_MemoryCRUD(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	w := &types.Workspace{Name: "mem-test-ws-" + types.NewID("t")}
	if err := ms.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	defer ms.DeleteWorkspace(ctx, w.ID)

	m := &types.Memory{
		WorkspaceID:      w.ID,
		Content:          "initial content",
		WrittenByAgentID: types.AgentLegacyVibeflowID,
		Tags:             map[string]string{"env": "test"},
	}
	wmk, err := ms.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if wmk == "" {
		t.Fatal("expected watermark")
	}

	got, err := ms.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "initial content" {
		t.Fatalf("content mismatch: %q", got.Content)
	}

	newWmk, md5, err := ms.AppendMemory(ctx, m.ID, wmk, " appended", types.AgentLegacyVibeflowID)
	if err != nil {
		t.Fatal(err)
	}
	if md5 == "" || newWmk == wmk {
		t.Fatal("expected new watermark and md5")
	}

	got, _ = ms.GetMemory(ctx, m.ID)
	if got.Content != "initial content appended" {
		t.Fatalf("append content mismatch: %q", got.Content)
	}

	if err := ms.ForgetMemory(ctx, w.ID, m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataStore_CASConflict(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	w := &types.Workspace{Name: "cas-test-" + types.NewID("t")}
	if err := ms.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}

	m := &types.Memory{
		WorkspaceID:      w.ID,
		Content:          "v1",
		WrittenByAgentID: types.AgentLegacyVibeflowID,
	}
	wmk, _ := ms.ImprintMemory(ctx, m)
	defer func() {
		ms.ForgetMemory(ctx, w.ID, m.ID)
		ms.DeleteWorkspace(ctx, w.ID)
	}()

	update := &types.Memory{
		Content:               "v2",
		LastModifiedByAgentID: types.AgentLegacyVibeflowID,
	}
	_, err := ms.UpdateMemory(ctx, m.ID, "wrong-watermark", update)
	if err != types.ErrCAS {
		t.Fatalf("expected ErrCAS, got %v", err)
	}

	_, err = ms.UpdateMemory(ctx, m.ID, wmk, update)
	if err != nil {
		t.Fatalf("expected success with correct watermark, got %v", err)
	}
}

func TestMetadataStore_AgentRegistry(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	w := &types.Workspace{Name: "agent-test-" + types.NewID("t")}
	if err := ms.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	defer ms.DeleteWorkspace(ctx, w.ID)

	agentID := "test-agent-" + types.NewID("t")
	a := &types.Agent{
		AgentID:          agentID,
		WorkspaceID:      w.ID,
		IdentityProvider: "opaque",
		DisplayName:      "Test Agent",
	}
	if err := ms.RegisterAgent(ctx, a); err != nil {
		t.Fatal(err)
	}

	got, err := ms.GetAgent(ctx, w.ID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Test Agent" || !got.Active {
		t.Fatalf("agent mismatch: %+v", got)
	}

	agents, err := ms.ListAgents(ctx, w.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ag := range agents {
		if ag.AgentID == agentID {
			found = true
		}
	}
	if !found {
		t.Fatal("agent not in list")
	}

	if err := ms.DeactivateAgent(ctx, w.ID, agentID); err != nil {
		t.Fatal(err)
	}
	got, _ = ms.GetAgent(ctx, w.ID, agentID)
	if got.Active {
		t.Fatal("expected agent to be deactivated")
	}
}

func TestMetadataStore_CellsAndRecallReady(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	w := &types.Workspace{Name: "cells-test-" + types.NewID("t")}
	if err := ms.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}

	m := &types.Memory{
		WorkspaceID:      w.ID,
		Content:          "cell content",
		WrittenByAgentID: types.AgentLegacyVibeflowID,
	}
	ms.ImprintMemory(ctx, m)
	defer func() {
		ms.ForgetMemory(ctx, w.ID, m.ID)
		ms.DeleteWorkspace(ctx, w.ID)
	}()

	cells := []types.Cell{
		{Seq: 0, Text: "chunk0", TextMD5: "md50", WrittenByAgentID: types.AgentLegacyVibeflowID, CreatedAt: time.Now().UTC()},
		{Seq: 1, Text: "chunk1", TextMD5: "md51", WrittenByAgentID: types.AgentLegacyVibeflowID, CreatedAt: time.Now().UTC()},
	}
	if err := ms.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatal(err)
	}

	got, err := ms.GetCells(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(got))
	}

	flipped, _ := ms.FlipRecallReadyIfAllEmbedded(ctx, m.ID)
	if flipped {
		t.Fatal("should not flip — cells have no vector_key")
	}

	for _, c := range got {
		ms.UpdateCellVectorKey(ctx, c.CellID, "vec_"+c.CellID, "model-v1")
	}

	flipped, _ = ms.FlipRecallReadyIfAllEmbedded(ctx, m.ID)
	if !flipped {
		t.Fatal("should have flipped recall_ready after all cells embedded")
	}
}

func TestMetadataStore_Capabilities(t *testing.T) {
	ms := &MetadataStore{}
	caps := ms.Capabilities()
	if !caps.SupportsCAS || !caps.SupportsTransactions || !caps.SupportsBatchUpsert || !caps.SupportsLogicalReplication {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.RecommendedMaxSizeGB != 10000 {
		t.Fatalf("expected 10000 GB, got %d", caps.RecommendedMaxSizeGB)
	}
}
