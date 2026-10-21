// Package compliance ships the public test suite that adapter authors
// run against their MetadataStore / VectorStore / LedgerStore
// implementations to ensure they satisfy the Memora contract.
//
// Usage from a third-party adapter package:
//
//	import "github.com/axiom-studio/memora/pkg/adapter/compliance"
//
//	func TestMyAdapter(t *testing.T) {
//	    compliance.MetadataStoreSuite(t, func(t *testing.T) adapter.MetadataStore {
//	        return openMyStore(t)
//	    })
//	}
//
// v0.1 ships the minimum-viable suite: MetadataStore CRUD + CAS +
// flip_recall_ready. The full LedgerStore / VectorStore / GraphStore
// suites land in v0.5 (#297 §18.5, §19.8).
package compliance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// MetadataFactory builds a fresh MetadataStore for one test. The
// returned store must be Open()-ed and ready to use.
type MetadataFactory func(t *testing.T) adapter.MetadataStore

// MetadataStoreSuite runs the v0.1 MetadataStore compliance suite.
func MetadataStoreSuite(t *testing.T, factory MetadataFactory) {
	t.Helper()
	t.Run("workspace_roundtrip", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "compliance-ws"}
		if err := s.CreateWorkspace(ctx, ws); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		got, err := s.GetWorkspace(ctx, ws.ID)
		if err != nil {
			t.Fatalf("GetWorkspace: %v", err)
		}
		if got.Name != ws.Name {
			t.Fatalf("name roundtrip: %q vs %q", got.Name, ws.Name)
		}
	})

	t.Run("memory_imprint_and_get", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "imprint-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "compliance content",
			WrittenByAgentID: "1d8c60fe-2085-4353-9bb2-6dcf9b61eb27",
		}
		wmk, err := s.ImprintMemory(ctx, mem)
		if err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		if wmk == "" {
			t.Fatal("imprint returned empty watermark")
		}
		got, err := s.GetMemory(ctx, mem.ID)
		if err != nil {
			t.Fatalf("GetMemory: %v", err)
		}
		if got.Content != mem.Content {
			t.Fatalf("content roundtrip: %q", got.Content)
		}
	})

	t.Run("update_cas", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "cas-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "v1",
			WrittenByAgentID: "1d8c60fe-2085-4353-9bb2-6dcf9b61eb27",
		}
		wmk, _ := s.ImprintMemory(ctx, mem)
		mem.Content = "v2"
		mem.LastModifiedByAgentID = "1d8c60fe-2085-4353-9bb2-6dcf9b61eb27"
		newWmk, err := s.UpdateMemory(ctx, mem.WorkspaceID, mem.ID, wmk, mem)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if newWmk == wmk {
			t.Fatal("update didn't bump watermark")
		}
		if _, err := s.UpdateMemory(ctx, mem.WorkspaceID, mem.ID, wmk, mem); err == nil {
			t.Fatal("expected ErrCAS on stale watermark")
		}
	})

	t.Run("delete_cells_from", func(t *testing.T) {
		DeleteCellsFromContract(t, factory(t))
	})

	t.Run("resolve_identity_key", func(t *testing.T) {
		ResolveIdentityKeyContract(t, factory(t))
	})

	t.Run("flip_recall_ready_if_all_embedded", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		ws := &types.Workspace{Name: "recall-ready-ws"}
		_ = s.CreateWorkspace(ctx, ws)
		mem := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "recall content",
			WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966",
		}
		if _, err := s.ImprintMemory(ctx, mem); err != nil {
			t.Fatalf("imprint: %v", err)
		}
		cells := []types.Cell{
			{Seq: 0, Text: "a", TextMD5: types.MD5Hex("a"), WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966"},
			{Seq: 1, Text: "b", TextMD5: types.MD5Hex("b"), WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966"},
		}
		if err := s.UpsertCells(ctx, mem.ID, cells); err != nil {
			t.Fatalf("upsert cells: %v", err)
		}
		flipped, err := s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (none embedded): %v", err)
		}
		if flipped {
			t.Fatal("flipped with zero cells embedded")
		}
		stored, _ := s.GetCells(ctx, mem.WorkspaceID, mem.ID)
		if err := s.UpdateCellVectorKey(ctx, stored[0].CellID, stored[0].CellID, "test:model"); err != nil {
			t.Fatalf("update cell 0: %v", err)
		}
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (1 of 2): %v", err)
		}
		if flipped {
			t.Fatal("flipped with one cell still missing vector_key")
		}
		if err := s.UpdateCellVectorKey(ctx, stored[1].CellID, stored[1].CellID, "test:model"); err != nil {
			t.Fatalf("update cell 1: %v", err)
		}
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (all embedded): %v", err)
		}
		if !flipped {
			t.Fatal("expected flip after all cells embedded")
		}
		got, err := s.GetMemory(ctx, mem.ID)
		if err != nil {
			t.Fatalf("GetMemory: %v", err)
		}
		if !got.RecallReady {
			t.Fatal("Memory.RecallReady not persisted after flip")
		}
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, mem.ID)
		if err != nil {
			t.Fatalf("flip (idempotent): %v", err)
		}
		if flipped {
			t.Fatal("second flip mutated an already-ready memory")
		}
		empty := &types.Memory{
			WorkspaceID:      ws.ID,
			Content:          "empty",
			WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966",
		}
		_, _ = s.ImprintMemory(ctx, empty)
		flipped, err = s.FlipRecallReadyIfAllEmbedded(ctx, empty.ID)
		if err != nil {
			t.Fatalf("flip (zero cells): %v", err)
		}
		if flipped {
			t.Fatal("flipped a memory with zero cells")
		}
	})
}

// DeleteCellsFromContract checks MetadataStore.DeleteCellsFrom (#6037): it
// removes exactly the cells at seq >= fromSeq, reports their ids in seq
// order, deletes nothing past the end, and treats another workspace's
// Memory as not found without touching its cells.
func DeleteCellsFromContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	ws := &types.Workspace{Name: "delete-cells-" + types.NewID()}
	other := &types.Workspace{Name: "delete-cells-other-" + types.NewID()}
	for _, w := range []*types.Workspace{ws, other} {
		if err := s.CreateWorkspace(ctx, w); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
	}
	mem := &types.Memory{WorkspaceID: ws.ID, Content: "a b c", WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966"}
	if _, err := s.ImprintMemory(ctx, mem); err != nil {
		t.Fatalf("imprint: %v", err)
	}
	var cells []types.Cell
	for i, txt := range []string{"a", "b", "c"} {
		cells = append(cells, types.Cell{Seq: i, Text: txt, TextMD5: types.MD5Hex(txt), WrittenByAgentID: "339f2f5f-7486-4171-a049-62c4a9213966"})
	}
	if err := s.UpsertCells(ctx, mem.ID, cells); err != nil {
		t.Fatalf("upsert cells: %v", err)
	}
	stored, _ := s.GetCells(ctx, ws.ID, mem.ID)
	if len(stored) != 3 {
		t.Fatalf("stored %d cells, want 3", len(stored))
	}

	if ids, err := s.DeleteCellsFrom(ctx, other.ID, mem.ID, 0); !errors.Is(err, types.ErrNotFound) || len(ids) != 0 {
		t.Fatalf("foreign workspace: ids=%v err=%v, want none and ErrNotFound", ids, err)
	}
	if after, _ := s.GetCells(ctx, ws.ID, mem.ID); len(after) != 3 {
		t.Fatalf("foreign-workspace call deleted cells: %d left", len(after))
	}

	ids, err := s.DeleteCellsFrom(ctx, ws.ID, mem.ID, 1)
	if err != nil {
		t.Fatalf("DeleteCellsFrom: %v", err)
	}
	if len(ids) != 2 || ids[0] != stored[1].CellID || ids[1] != stored[2].CellID {
		t.Fatalf("deleted ids = %v, want [%s %s]", ids, stored[1].CellID, stored[2].CellID)
	}
	after, _ := s.GetCells(ctx, ws.ID, mem.ID)
	if len(after) != 1 || after[0].CellID != stored[0].CellID {
		t.Fatalf("cells after delete = %+v, want only seq 0", after)
	}

	if ids, err := s.DeleteCellsFrom(ctx, ws.ID, mem.ID, 5); err != nil || len(ids) != 0 {
		t.Fatalf("delete past the end: ids=%v err=%v, want none", ids, err)
	}
}

// ResolveIdentityKeyContract checks MetadataStore.ResolveIdentityKey: the
// first call binds the candidate ID, later calls return that binding and
// ignore their candidates, distinct keys stay distinct, and concurrent
// first calls for one key all agree on a single ID.
func ResolveIdentityKeyContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	key := "compliance|" + types.NewID()

	first := types.NewID()
	got, err := s.ResolveIdentityKey(ctx, key, first)
	if err != nil || got != first {
		t.Fatalf("first resolve = %q, %v; want the candidate %q", got, err, first)
	}
	if got, err := s.ResolveIdentityKey(ctx, key, types.NewID()); err != nil || got != first {
		t.Fatalf("second resolve = %q, %v; want the first binding %q", got, err, first)
	}
	if got, err := s.ResolveIdentityKey(ctx, "compliance|"+types.NewID(), types.NewID()); err != nil || got == first {
		t.Fatalf("a distinct key resolved to %q, %v; want its own ID", got, err)
	}

	raceKey := "compliance|" + types.NewID()
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = s.ResolveIdentityKey(ctx, raceKey, types.NewID())
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("concurrent resolve %d = %q, %v; want all %q", i, ids[i], errs[i], ids[0])
		}
	}
}

// LedgerCursorContract checks LedgerStore.Query keyset paging: following
// nextCursor (passed back as SinceLedgerID) visits every entry exactly
// once, in the store's ts DESC, ledger_id DESC order, even when many
// entries share a timestamp and ledger IDs are random UUIDs.
func LedgerCursorContract(t *testing.T, s adapter.LedgerStore) {
	t.Helper()
	ctx := context.Background()
	ws := types.NewID()
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var entries []api.LedgerEntry
	for i := 0; i < 30; i++ {
		ts := base // most entries tie on ts; a few sit either side of it
		switch {
		case i%10 == 1:
			ts = base.Add(time.Second)
		case i%10 == 2:
			ts = base.Add(-time.Second)
		}
		entries = append(entries, api.LedgerEntry{
			LedgerID: types.NewID(), WorkspaceID: ws, Op: "imprint", AgentID: types.NewID(), Timestamp: ts,
		})
	}
	if err := s.AppendBatch(ctx, entries); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	all, _, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, Limit: 1000})
	if err != nil || len(all) != len(entries) {
		t.Fatalf("full query = %d entries, %v; want %d", len(all), err, len(entries))
	}
	for _, pageSize := range []int{1, 3, 7} {
		var got []string
		cursor := ""
		for page := 0; page <= len(entries); page++ {
			out, next, err := s.Query(ctx, adapter.LedgerQuery{WorkspaceID: ws, Limit: pageSize, SinceLedgerID: cursor})
			if err != nil {
				t.Fatalf("page size %d: Query: %v", pageSize, err)
			}
			for _, e := range out {
				got = append(got, e.LedgerID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(got) != len(all) {
			t.Fatalf("page size %d: paged %d entries, want %d", pageSize, len(got), len(all))
		}
		for i := range all {
			if got[i] != all[i].LedgerID {
				t.Fatalf("page size %d: entry %d = %s, want %s (skipped or repeated rows)", pageSize, i, got[i], all[i].LedgerID)
			}
		}
	}
}

// VectorScopeContract checks VectorQuery scoping on a 3-dimension store:
// WorkspaceID searches one workspace, WorkspaceIDs those workspaces,
// AllWorkspaces the whole node, and a query with no scope (or more than
// one) is invalid input rather than silently node-wide.
func VectorScopeContract(t *testing.T, s adapter.VectorStore) {
	t.Helper()
	ctx := context.Background()
	wsA, wsB, wsC := types.NewID(), types.NewID(), types.NewID()
	var puts []adapter.VectorPut
	for _, ws := range []string{wsA, wsB, wsC} {
		puts = append(puts, adapter.VectorPut{
			Key:       adapter.VectorKey{WorkspaceID: ws, MemoryID: types.NewID(), CellID: types.NewID()},
			Embedding: []float32{1, 0, 0},
		})
	}
	if err := s.PutVectorsBatch(ctx, puts); err != nil {
		t.Fatalf("PutVectorsBatch: %v", err)
	}
	workspacesOf := func(q adapter.VectorQuery) map[string]bool {
		t.Helper()
		q.Embedding, q.K = []float32{1, 0, 0}, 1000
		hits, err := s.Query(ctx, q)
		if err != nil {
			t.Fatalf("Query(%+v): %v", q, err)
		}
		got := map[string]bool{}
		for _, h := range hits {
			got[h.Key.WorkspaceID] = true
		}
		return got
	}

	if got := workspacesOf(adapter.VectorQuery{WorkspaceID: wsA}); len(got) != 1 || !got[wsA] {
		t.Errorf("WorkspaceID scope hit %v, want only %s", got, wsA)
	}
	if got := workspacesOf(adapter.VectorQuery{WorkspaceIDs: []string{wsA, wsB}}); len(got) != 2 || !got[wsA] || !got[wsB] {
		t.Errorf("WorkspaceIDs scope hit %v, want %s and %s", got, wsA, wsB)
	}
	if got := workspacesOf(adapter.VectorQuery{AllWorkspaces: true}); !got[wsA] || !got[wsB] || !got[wsC] {
		t.Errorf("AllWorkspaces hit %v, want all three workspaces", got)
	}
	for name, q := range map[string]adapter.VectorQuery{
		"no scope":   {},
		"two scopes": {WorkspaceID: wsA, AllWorkspaces: true},
	} {
		q.Embedding, q.K = []float32{1, 0, 0}, 10
		if _, err := s.Query(ctx, q); !errors.Is(err, types.ErrInvalidInput) {
			t.Errorf("%s: Query err = %v, want ErrInvalidInput", name, err)
		}
	}
}
