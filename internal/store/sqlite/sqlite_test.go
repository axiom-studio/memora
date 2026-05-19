package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func openStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	s := &Store{}
	ctx := context.Background()
	if err := s.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, ctx
}

func TestImprintAndGet(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "test"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	m := &types.Memory{
		WorkspaceID:      ws.ID,
		Content:          "hello world",
		WrittenByAgentID: "agent_opaque_test",
	}
	wmk, err := s.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if wmk == "" {
		t.Fatal("watermark empty")
	}
	got, err := s.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" {
		t.Fatalf("content roundtrip: %q", got.Content)
	}
	if got.HeadWatermark != wmk {
		t.Fatalf("watermark mismatch: %s vs %s", got.HeadWatermark, wmk)
	}
}

func TestUpdateMemory_CAS(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "test"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "v1", WrittenByAgentID: "agent_opaque_x"}
	wmk, err := s.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	// Happy path.
	m.Content = "v2"
	m.LastModifiedByAgentID = "agent_opaque_x"
	newWmk, err := s.UpdateMemory(ctx, m.ID, wmk, m)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if newWmk == wmk {
		t.Fatal("watermark didn't bump")
	}
	// CAS conflict.
	_, err = s.UpdateMemory(ctx, m.ID, wmk, m)
	if err != types.ErrCAS {
		t.Fatalf("expected ErrCAS, got %v", err)
	}
}

func TestPatchMemory_AppliesOps(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "patch"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "alpha beta gamma", WrittenByAgentID: "agent_opaque_p"}
	wmk, _ := s.ImprintMemory(ctx, m)

	ops := []api.PatchOp{{OldString: "beta", NewString: "BETA"}}
	newWmk, _, content, err := s.PatchMemory(ctx, m.ID, wmk, ops, "agent_opaque_p")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if content != "alpha BETA gamma" {
		t.Fatalf("content: %q", content)
	}
	if newWmk == wmk {
		t.Fatal("watermark didn't bump")
	}
}

func TestPatchMemory_AmbiguousAnchorRejected(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "patch2"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "x x", WrittenByAgentID: "agent_opaque_p"}
	wmk, _ := s.ImprintMemory(ctx, m)
	_, _, _, err := s.PatchMemory(ctx, m.ID, wmk, []api.PatchOp{{OldString: "x", NewString: "y"}}, "agent_opaque_p")
	if err == nil {
		t.Fatal("expected ErrPatchAnchor on ambiguous")
	}
}

func TestGraphLink_AndNeighbors(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "graph"}
	_ = s.CreateWorkspace(ctx, ws)

	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_g"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_g"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)

	e := types.Edge{
		WorkspaceID:      ws.ID,
		SourceMemoryID:   a.ID,
		TargetMemoryID:   b.ID,
		EdgeType:         types.EdgeTypeReferences,
		CreatedByAgentID: "agent_opaque_g",
	}
	if _, err := s.GraphLink(ctx, e); err != nil {
		t.Fatalf("link: %v", err)
	}
	edges, headers, err := s.GraphNeighbors(ctx, ws.ID, a.ID, adapter.NeighborsOpts{Direction: "out"})
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetMemoryID != b.ID {
		t.Fatalf("expected one out-edge to B, got %+v", edges)
	}
	if len(headers) != 1 || headers[0].MemoryID != b.ID {
		t.Fatalf("expected B header, got %+v", headers)
	}
}

func TestGraphLink_UniqueLiveTriple(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "uniq"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_u"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_u"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	e := types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_u"}
	if _, err := s.GraphLink(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GraphLink(ctx, e); err == nil {
		t.Fatal("expected uniqueness violation on duplicate live triple")
	}
}

func TestGraphTraverse_DepthCap(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "depth"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_d"}
	_, _ = s.ImprintMemory(ctx, a)
	_, err := s.GraphTraverse(ctx, ws.ID, a.ID, adapter.TraverseOpts{Depth: 5})
	if err == nil {
		t.Fatal("expected ErrDepthExceeded on depth > 3")
	}
}

func TestGraphCascadeForget(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "cascade"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_c"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_c"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_c"})
	n, err := s.GraphCascadeForget(ctx, a.ID, "agent_opaque_c")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cascaded edge, got %d", n)
	}
}

func TestMigrationIdempotence(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "memora.db")
	ctx := context.Background()

	s1 := &Store{}
	if err := s1.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("first open: %v", err)
	}
	ws := &types.Workspace{Name: "idempotent"}
	if err := s1.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := &Store{}
	if err := s2.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("second open (re-migrate): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("data lost after re-open: %v", err)
	}
	if got.Name != "idempotent" {
		t.Errorf("name = %q, want idempotent", got.Name)
	}
}

func TestMigrationSplit_NewObjects(t *testing.T) {
	s, ctx := openStore(t)

	// memora_workspace_meta table exists and accepts inserts.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memora_workspace_meta(workspace_id, hierarchy_labels, extra_json, updated_at)
		 VALUES ('ws_test', '{}', NULL, datetime('now'))`)
	if err != nil {
		// Need a workspace to satisfy FK.
		ws := &types.Workspace{Name: "meta_test"}
		if err2 := s.CreateWorkspace(ctx, ws); err2 != nil {
			t.Fatal(err2)
		}
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO memora_workspace_meta(workspace_id, hierarchy_labels, extra_json, updated_at)
			 VALUES (?, '{}', NULL, datetime('now'))`, ws.ID)
		if err != nil {
			t.Fatalf("workspace_meta insert: %v", err)
		}
	}

	// memora_edges_view exists and returns zero rows.
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memora_edges_view").Scan(&n); err != nil {
		t.Fatalf("edges_view query: %v", err)
	}
	if n != 0 {
		t.Fatalf("edges_view should return 0 rows, got %d", n)
	}

	// Three migration files were applied.
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM memora_schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	_ = rows.Close()
	if len(versions) != 3 {
		t.Fatalf("expected 3 migrations, got %v", versions)
	}
	if versions[0] != "0001_init.sql" || versions[1] != "0002_agents.sql" || versions[2] != "0003_edges.sql" {
		t.Fatalf("unexpected migration versions: %v", versions)
	}
}

func TestCapabilities(t *testing.T) {
	s, _ := openStore(t)
	caps := s.Capabilities()
	if !caps.SupportsCAS {
		t.Error("SupportsCAS should be true")
	}
	if !caps.SupportsTransactions {
		t.Error("SupportsTransactions should be true")
	}
	if caps.MaxGraphDepth != 3 {
		t.Errorf("MaxGraphDepth = %d, want 3", caps.MaxGraphDepth)
	}
	if caps.MaxNeighborsK != 200 {
		t.Errorf("MaxNeighborsK = %d, want 200", caps.MaxNeighborsK)
	}
	if caps.MaxLinkBatchSize != 1000 {
		t.Errorf("MaxLinkBatchSize = %d, want 1000", caps.MaxLinkBatchSize)
	}
}

func TestTenantIsolation_GetAgent(t *testing.T) {
	s, ctx := openStore(t)
	w1 := &types.Workspace{Name: "w1"}
	w2 := &types.Workspace{Name: "w2"}
	_ = s.CreateWorkspace(ctx, w1)
	_ = s.CreateWorkspace(ctx, w2)

	a := &types.Agent{AgentID: "agent_iso_1", WorkspaceID: w1.ID, IdentityProvider: "opaque"}
	_ = s.RegisterAgent(ctx, a)

	if _, err := s.GetAgent(ctx, w1.ID, "agent_iso_1"); err != nil {
		t.Fatalf("same-workspace GetAgent failed: %v", err)
	}
	if _, err := s.GetAgent(ctx, w2.ID, "agent_iso_1"); err != types.ErrNotFound {
		t.Fatalf("cross-workspace GetAgent: expected ErrNotFound, got %v", err)
	}
}

func TestTenantIsolation_DeactivateAgent(t *testing.T) {
	s, ctx := openStore(t)
	w1 := &types.Workspace{Name: "w1"}
	w2 := &types.Workspace{Name: "w2"}
	_ = s.CreateWorkspace(ctx, w1)
	_ = s.CreateWorkspace(ctx, w2)

	a := &types.Agent{AgentID: "agent_iso_2", WorkspaceID: w1.ID, IdentityProvider: "opaque"}
	_ = s.RegisterAgent(ctx, a)

	if err := s.DeactivateAgent(ctx, w2.ID, "agent_iso_2"); err != types.ErrNotFound {
		t.Fatalf("cross-workspace DeactivateAgent: expected ErrNotFound, got %v", err)
	}
	if err := s.DeactivateAgent(ctx, w1.ID, "agent_iso_2"); err != nil {
		t.Fatalf("same-workspace DeactivateAgent failed: %v", err)
	}
}

func TestTenantIsolation_UpsertTag(t *testing.T) {
	s, ctx := openStore(t)
	w1 := &types.Workspace{Name: "w1"}
	w2 := &types.Workspace{Name: "w2"}
	_ = s.CreateWorkspace(ctx, w1)
	_ = s.CreateWorkspace(ctx, w2)

	m := &types.Memory{WorkspaceID: w1.ID, Content: "tagged", WrittenByAgentID: "agent_opaque_t"}
	_, _ = s.ImprintMemory(ctx, m)

	if err := s.UpsertTag(ctx, w1.ID, m.ID, "env", "prod"); err != nil {
		t.Fatalf("same-workspace UpsertTag failed: %v", err)
	}
	if err := s.UpsertTag(ctx, w2.ID, m.ID, "env", "prod"); err != types.ErrNotFound {
		t.Fatalf("cross-workspace UpsertTag: expected ErrNotFound, got %v", err)
	}
}

func TestTenantIsolation_GetWatermarkHistory(t *testing.T) {
	s, ctx := openStore(t)
	w1 := &types.Workspace{Name: "w1"}
	w2 := &types.Workspace{Name: "w2"}
	_ = s.CreateWorkspace(ctx, w1)
	_ = s.CreateWorkspace(ctx, w2)

	m := &types.Memory{WorkspaceID: w1.ID, Content: "wmk", WrittenByAgentID: "agent_opaque_w"}
	_, _ = s.ImprintMemory(ctx, m)

	hist, err := s.GetWatermarkHistory(ctx, w1.ID, m.ID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("same-workspace history: %v", err)
	}
	if len(hist) == 0 {
		t.Fatal("expected at least one watermark entry")
	}

	hist2, err := s.GetWatermarkHistory(ctx, w2.ID, m.ID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("cross-workspace history error: %v", err)
	}
	if len(hist2) != 0 {
		t.Fatalf("cross-workspace history should be empty, got %d entries", len(hist2))
	}
}

func TestGetMemory_NotFound(t *testing.T) {
	s, ctx := openStore(t)
	_, err := s.GetMemory(ctx, "mem_does_not_exist")
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteWorkspace_NonEmpty(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "nonempty"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "keep me", WrittenByAgentID: "agent_opaque_ne"}
	_, _ = s.ImprintMemory(ctx, m)

	err := s.DeleteWorkspace(ctx, ws.ID)
	if !errors.Is(err, types.ErrNotEmpty) {
		t.Fatalf("expected ErrNotEmpty, got %v", err)
	}

	_ = s.ForgetMemory(ctx, m.ID)
	if err := s.DeleteWorkspace(ctx, ws.ID); err != nil {
		t.Fatalf("delete after forget: %v", err)
	}
}

func TestDeleteCollection_NonEmpty(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "coll_nonempty"}
	_ = s.CreateWorkspace(ctx, ws)
	c := &types.Collection{WorkspaceID: ws.ID, Name: "test_coll"}
	_ = s.CreateCollection(ctx, c)
	m := &types.Memory{WorkspaceID: ws.ID, CollectionID: c.ID, Content: "in coll", WrittenByAgentID: "agent_opaque_cn"}
	_, _ = s.ImprintMemory(ctx, m)

	err := s.DeleteCollection(ctx, c.ID)
	if !errors.Is(err, types.ErrNotEmpty) {
		t.Fatalf("expected ErrNotEmpty, got %v", err)
	}

	_ = s.ForgetMemory(ctx, m.ID)
	if err := s.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatalf("delete after forget: %v", err)
	}
}

func TestUpdateMemory_ConcurrentWriters(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "race"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "v0", WrittenByAgentID: "agent_opaque_r"}
	wmk, _ := s.ImprintMemory(ctx, m)

	var winnerCount, loserCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m2 := &types.Memory{
				WorkspaceID:           ws.ID,
				Content:               fmt.Sprintf("v%d", i+1),
				LastModifiedByAgentID: "agent_opaque_r",
			}
			_, err := s.UpdateMemory(ctx, m.ID, wmk, m2)
			switch {
			case err == nil:
				winnerCount.Add(1)
			case errors.Is(err, types.ErrCAS):
				loserCount.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if winnerCount.Load() != 1 {
		t.Errorf("expected exactly one writer to succeed, got %d", winnerCount.Load())
	}
	if loserCount.Load() != 4 {
		t.Errorf("expected 4 CAS losers, got %d", loserCount.Load())
	}
}

func TestUpsertCells_Idempotent(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "cells"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "x", WrittenByAgentID: "agent_opaque_c"}
	_, _ = s.ImprintMemory(ctx, m)

	cells := []types.Cell{
		{Seq: 0, Text: "first", TextMD5: types.MD5Hex("first"), WrittenByAgentID: "agent_opaque_c"},
		{Seq: 1, Text: "second", TextMD5: types.MD5Hex("second"), WrittenByAgentID: "agent_opaque_c"},
	}
	if err := s.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCells(ctx, m.ID, cells); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCells(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 cells after idempotent re-apply, got %d", len(got))
	}
}

func TestGetWatermarkHistory_SinceFilter(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "wmkh"}
	_ = s.CreateWorkspace(ctx, ws)
	m := &types.Memory{WorkspaceID: ws.ID, Content: "v1", WrittenByAgentID: "agent_opaque_w"}
	_, _ = s.ImprintMemory(ctx, m)

	future := time.Now().UTC().Add(time.Hour)
	hist, err := s.GetWatermarkHistory(ctx, ws.ID, m.ID, future)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("expected zero history rows since the future, got %d", len(hist))
	}
	epoch := time.Unix(0, 0).UTC()
	hist, _ = s.GetWatermarkHistory(ctx, ws.ID, m.ID, epoch)
	if len(hist) == 0 {
		t.Fatal("expected at least one history row")
	}
	if hist[0].Op != "imprint" {
		t.Fatalf("first history row op = %s, want imprint", hist[0].Op)
	}
}

func TestRegisterAgent_ConcurrentUpserts(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "agents_race"}
	_ = s.CreateWorkspace(ctx, ws)

	const id = "agent_opaque_race"
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := &types.Agent{
				AgentID:          id,
				WorkspaceID:      ws.ID,
				DisplayName:      fmt.Sprintf("writer-%d", i),
				IdentityProvider: "opaque",
			}
			if err := s.RegisterAgent(ctx, a); err != nil {
				t.Errorf("register %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.GetAgent(ctx, ws.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.DisplayName, "writer-") {
		t.Fatalf("display_name not preserved across race: %s", got.DisplayName)
	}
	all, _ := s.ListAgents(ctx, ws.ID, 0)
	n := 0
	for _, a := range all {
		if a.AgentID == id {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected 1 row for %s after concurrent upserts, got %d", id, n)
	}
}

func TestGraphLink_AllEdgeTypes(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "alltypes"}
	_ = s.CreateWorkspace(ctx, ws)

	for _, et := range types.StoredEdgeTypes {
		a := &types.Memory{WorkspaceID: ws.ID, Content: "A-" + string(et), WrittenByAgentID: "agent_opaque_t"}
		b := &types.Memory{WorkspaceID: ws.ID, Content: "B-" + string(et), WrittenByAgentID: "agent_opaque_t"}
		_, _ = s.ImprintMemory(ctx, a)
		_, _ = s.ImprintMemory(ctx, b)
		e := types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: et, CreatedByAgentID: "agent_opaque_t"}
		got, err := s.GraphLink(ctx, e)
		if err != nil {
			t.Errorf("link %s: %v", et, err)
			continue
		}
		if got.EdgeType != et {
			t.Errorf("roundtrip edge_type %s -> %s", et, got.EdgeType)
		}
	}
}

func TestGraphLinkBatch_PerEdgeIsolation(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "batch"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_b"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_b"}
	c := &types.Memory{WorkspaceID: ws.ID, Content: "C", WrittenByAgentID: "agent_opaque_b"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	_, _ = s.ImprintMemory(ctx, c)

	edges := []types.Edge{
		{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_b"},
		{WorkspaceID: ws.ID, SourceMemoryID: "mem_does_not_exist", TargetMemoryID: c.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_b"},
		{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: c.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_b"},
	}
	results, err := s.GraphLinkBatch(ctx, edges)
	if err != nil {
		t.Fatalf("batch returned outer error: %v", err)
	}
	if results[0].Status != "ok" {
		t.Errorf("expected first edge ok, got %s (err=%v)", results[0].Status, results[0].Error)
	}
	if results[1].Status != "error" {
		t.Errorf("expected second edge error (bad source), got %s", results[1].Status)
	}
	if results[2].Status != "ok" {
		t.Errorf("expected third edge ok, got %s (err=%v)", results[2].Status, results[2].Error)
	}
}

func TestGraphLink_OutDegreeCap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow out-degree test")
	}
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "outdeg"}
	_ = s.CreateWorkspace(ctx, ws)
	src := &types.Memory{WorkspaceID: ws.ID, Content: "src", WrittenByAgentID: "agent_opaque_od"}
	_, _ = s.ImprintMemory(ctx, src)

	for i := 0; i < 1000; i++ {
		tgt := &types.Memory{WorkspaceID: ws.ID, Content: fmt.Sprintf("tgt-%d", i), WrittenByAgentID: "agent_opaque_od"}
		_, _ = s.ImprintMemory(ctx, tgt)
		_, err := s.GraphLink(ctx, types.Edge{
			WorkspaceID: ws.ID, SourceMemoryID: src.ID, TargetMemoryID: tgt.ID,
			EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_od",
		})
		if err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	overflow := &types.Memory{WorkspaceID: ws.ID, Content: "overflow", WrittenByAgentID: "agent_opaque_od"}
	_, _ = s.ImprintMemory(ctx, overflow)
	_, err := s.GraphLink(ctx, types.Edge{
		WorkspaceID: ws.ID, SourceMemoryID: src.ID, TargetMemoryID: overflow.ID,
		EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_od",
	})
	if !errors.Is(err, types.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on 1001st edge, got %v", err)
	}
}

func TestGraphTraverse_LayerOrdering(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "traverse"}
	_ = s.CreateWorkspace(ctx, ws)

	ids := map[string]string{}
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		m := &types.Memory{WorkspaceID: ws.ID, Content: name, WrittenByAgentID: "agent_opaque_t"}
		_, _ = s.ImprintMemory(ctx, m)
		ids[name] = m.ID
	}
	for _, link := range [][2]string{
		{"A", "B"}, {"A", "C"},
		{"B", "D"}, {"B", "E"},
		{"C", "E"}, {"C", "F"},
		{"D", "G"},
	} {
		_, _ = s.GraphLink(ctx, types.Edge{
			WorkspaceID: ws.ID, SourceMemoryID: ids[link[0]], TargetMemoryID: ids[link[1]],
			EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_t",
		})
	}

	res, err := s.GraphTraverse(ctx, ws.ID, ids["A"], adapter.TraverseOpts{Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(res.Layers))
	}
	if len(res.Layers[0]) != 2 {
		t.Errorf("layer 1: want 2 (B, C), got %d", len(res.Layers[0]))
	}
	if len(res.Layers[1]) != 3 {
		t.Errorf("layer 2: want 3 (D, E, F), got %d", len(res.Layers[1]))
	}
	if len(res.Layers[2]) != 1 {
		t.Errorf("layer 3: want 1 (G), got %d", len(res.Layers[2]))
	}
}

func TestGraphTraverse_CycleDoesNotLoop(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "cycle"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_c"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_c"}
	c := &types.Memory{WorkspaceID: ws.ID, Content: "C", WrittenByAgentID: "agent_opaque_c"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	_, _ = s.ImprintMemory(ctx, c)
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_c"})
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: b.ID, TargetMemoryID: c.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_c"})
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: c.ID, TargetMemoryID: a.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_c"})

	res, err := s.GraphTraverse(ctx, ws.ID, a.ID, adapter.TraverseOpts{Depth: 3, Direction: api.GraphDirOut})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.NodesVisited != 2 {
		t.Errorf("expected 2 visited nodes (B, C); A revisit dropped; got %d", res.Stats.NodesVisited)
	}
}

func TestGraphTraverse_FilterPrunes_DescendantsExplored(t *testing.T) {
	s, ctx := openStore(t)
	ws := &types.Workspace{Name: "filter"}
	_ = s.CreateWorkspace(ctx, ws)
	a := &types.Memory{WorkspaceID: ws.ID, Content: "A", WrittenByAgentID: "agent_opaque_alice"}
	b := &types.Memory{WorkspaceID: ws.ID, Content: "B", WrittenByAgentID: "agent_opaque_bob"}
	c := &types.Memory{WorkspaceID: ws.ID, Content: "C", WrittenByAgentID: "agent_opaque_alice"}
	_, _ = s.ImprintMemory(ctx, a)
	_, _ = s.ImprintMemory(ctx, b)
	_, _ = s.ImprintMemory(ctx, c)
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: a.ID, TargetMemoryID: b.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_alice"})
	_, _ = s.GraphLink(ctx, types.Edge{WorkspaceID: ws.ID, SourceMemoryID: b.ID, TargetMemoryID: c.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_alice"})

	res, _ := s.GraphTraverse(ctx, ws.ID, a.ID, adapter.TraverseOpts{
		Depth:     2,
		Direction: api.GraphDirOut,
		Filter:    map[string]any{"agent_id": "agent_opaque_alice"},
	})

	foundC := false
	for _, layer := range res.Layers {
		for _, hit := range layer {
			if hit.Memory.MemoryID == c.ID {
				foundC = true
			}
			if hit.Memory.MemoryID == b.ID {
				t.Errorf("B was pruned by filter but still appeared in layer %d", hit.Layer)
			}
		}
	}
	if !foundC {
		t.Errorf("descendant C of pruned B was not explored")
	}
}
