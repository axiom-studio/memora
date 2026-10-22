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
	"fmt"
	"sort"
	"strings"
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

	t.Run("agents_per_workspace", func(t *testing.T) {
		AgentsPerWorkspaceContract(t, factory(t))
	})

	t.Run("batch_load", func(t *testing.T) {
		BatchLoadContract(t, factory(t))
	})

	t.Run("keyword_search", func(t *testing.T) {
		KeywordSearchContract(t, factory(t))
	})

	t.Run("tag_edits", func(t *testing.T) {
		TagEditContract(t, factory(t))
	})
	t.Run("keyword_filter", func(t *testing.T) {
		KeywordFilterContract(t, factory(t))
	})

	t.Run("ensure_node_id", func(t *testing.T) {
		EnsureNodeIDContract(t, factory(t))
	})

	t.Run("list_chronological", func(t *testing.T) {
		ListChronologicalContract(t, factory(t))
	})

	t.Run("peer_store", func(t *testing.T) {
		PeerStoreContract(t, factory(t))
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

// VectorDeleteContract checks DeleteVectors on a 3-dimension store: a
// memory key removes every vector of that memory and only in its
// workspace, a cell key removes one vector, and keys naming neither, or
// a memory without its workspace, are invalid input. When the store is
// model-aware it also checks VectorMemories paging.
func VectorDeleteContract(t *testing.T, s adapter.VectorStore) {
	t.Helper()
	ctx := context.Background()
	wsA, wsB := types.NewID(), types.NewID()
	memA, memB := types.NewID(), types.NewID()
	put := func(ws, mem string) string {
		t.Helper()
		cell := types.NewID()
		if err := s.PutVector(ctx, adapter.VectorPut{Key: adapter.VectorKey{WorkspaceID: ws, MemoryID: mem, CellID: cell}, Embedding: []float32{1, 0, 0}}); err != nil {
			t.Fatalf("PutVector: %v", err)
		}
		return cell
	}
	put(wsA, memA)
	put(wsA, memA)
	keepB := put(wsA, memB)
	dropB := put(wsA, memB)
	put(wsB, memA) // the same memory ID in another workspace is not touched
	cells := func(ws string) map[string]string {
		t.Helper()
		hits, err := s.Query(ctx, adapter.VectorQuery{WorkspaceID: ws, Embedding: []float32{1, 0, 0}, K: 100})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		out := map[string]string{}
		for _, h := range hits {
			out[h.Key.CellID] = h.Key.MemoryID
		}
		return out
	}

	if err := s.DeleteVectors(ctx, []adapter.VectorKey{{WorkspaceID: wsA, MemoryID: memA}, {CellID: dropB}}); err != nil {
		t.Fatalf("DeleteVectors: %v", err)
	}
	if got := cells(wsA); len(got) != 1 || got[keepB] != memB {
		t.Errorf("workspace A after delete = %v, want only %s of memory B", got, keepB)
	}
	if got := cells(wsB); len(got) != 1 {
		t.Errorf("workspace B after delete = %v, want its vector of the same memory ID kept", got)
	}
	for name, k := range map[string]adapter.VectorKey{
		"empty key":            {},
		"memory, no workspace": {MemoryID: memB},
		"workspace only":       {WorkspaceID: wsA},
	} {
		if err := s.DeleteVectors(ctx, []adapter.VectorKey{k}); !errors.Is(err, types.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if got := cells(wsA); len(got) != 1 {
		t.Errorf("a refused delete changed workspace A: %v", got)
	}

	mav, ok := s.(adapter.ModelAwareVectorStore)
	if !ok {
		return
	}
	wsC := types.NewID()
	var want []string
	for i := 0; i < 5; i++ {
		m := types.NewID()
		put(wsC, m)
		put(wsC, m)
		want = append(want, m)
	}
	sort.Strings(want)
	var got []string
	for after := ""; ; {
		page, err := mav.VectorMemories(ctx, wsC, after, 2)
		if err != nil {
			t.Fatalf("VectorMemories: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		after = page[len(page)-1]
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("VectorMemories paged = %v, want %v (each memory once, ascending)", got, want)
	}
}

// EnsureNodeIDContract checks MetadataStore.EnsureNodeID: the first call
// stores the candidate, and every later call returns it, ignoring its
// own candidate.
func EnsureNodeIDContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	first, err := s.EnsureNodeID(ctx, types.NewID())
	if err != nil {
		t.Fatalf("EnsureNodeID: %v", err)
	}
	if _, err := types.ParseID("node_id", first); err != nil {
		t.Fatalf("stored node ID: %v", err)
	}
	if again, err := s.EnsureNodeID(ctx, types.NewID()); err != nil || again != first {
		t.Fatalf("second EnsureNodeID = %q, %v; want the stored %q", again, err, first)
	}
}

// KeywordSearchContract checks MetadataStore.SearchKeyword: memories
// matching more query terms rank higher; full-text operator characters
// in the query are plain text; the whole store is searched, not just
// recent memories; forgotten memories drop out; and the workspace and
// collection scopes hold.
func KeywordSearchContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	agent := types.NewID()
	newWS := func(name string) string {
		t.Helper()
		ws := &types.Workspace{Name: name + "-" + types.NewID()}
		if err := s.CreateWorkspace(ctx, ws); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		return ws.ID
	}
	imprint := func(ws, coll, content string) string {
		t.Helper()
		m := &types.Memory{WorkspaceID: ws, CollectionID: coll, Content: content, WrittenByAgentID: agent}
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		return m.ID
	}
	search := func(q adapter.KeywordQuery) []adapter.KeywordHit {
		t.Helper()
		if q.K == 0 {
			q.K = 50
		}
		hits, err := s.SearchKeyword(ctx, q)
		if err != nil {
			t.Fatalf("SearchKeyword(%q): %v", q.Query, err)
		}
		return hits
	}
	ids := func(hits []adapter.KeywordHit) map[string]bool {
		out := map[string]bool{}
		for _, h := range hits {
			out[h.MemoryID] = true
		}
		return out
	}

	wsA, wsB := newWS("kw-a"), newWS("kw-b")
	coll := &types.Collection{WorkspaceID: wsA, Name: "kw-coll"}
	if err := s.CreateCollection(ctx, coll); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	// "zircon" is unique to this run's corpus, so other contracts sharing a
	// database cannot match.
	both := imprint(wsA, coll.ID, "zircon quarry report and quarry logistics")
	one := imprint(wsA, "", "zircon prices only")
	other := imprint(wsB, "", "zircon quarry in another workspace")
	imprint(wsA, "", "unrelated grocery list")

	ranked := search(adapter.KeywordQuery{WorkspaceIDs: []string{wsA}, Query: "zircon quarry"})
	if len(ranked) != 2 || ranked[0].MemoryID != both || ranked[1].MemoryID != one {
		t.Fatalf("ranking = %+v, want %s (both terms) then %s (one term)", ranked, both, one)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Fatalf("scores %v <= %v; higher must be better", ranked[0].Score, ranked[1].Score)
	}

	for _, q := range []string{`zircon "quarry`, `zircon*`, `zircon NEAR quarry`, `-zircon`, `(zircon) AND OR`, `zircon: quarry^`} {
		if got := ids(search(adapter.KeywordQuery{WorkspaceIDs: []string{wsA}, Query: q})); !got[both] {
			t.Errorf("query %q: hits %v, want %s (operators must be literal)", q, got, both)
		}
	}
	if hits := search(adapter.KeywordQuery{AllWorkspaces: true, Query: `"" * - ()`}); len(hits) != 0 {
		t.Errorf("a query with no terms returned %d hits", len(hits))
	}

	if got := ids(search(adapter.KeywordQuery{AllWorkspaces: true, Query: "zircon"})); !got[both] || !got[one] || !got[other] {
		t.Errorf("node-wide hits %v, want memories from both workspaces", got)
	}
	if got := ids(search(adapter.KeywordQuery{WorkspaceIDs: []string{wsA}, CollectionID: coll.ID, Query: "zircon"})); len(got) != 1 || !got[both] {
		t.Errorf("collection-scoped hits %v, want only %s", got, both)
	}
	if _, err := s.SearchKeyword(ctx, adapter.KeywordQuery{Query: "zircon"}); !errors.Is(err, types.ErrInvalidInput) {
		t.Errorf("unscoped query err = %v, want ErrInvalidInput", err)
	}

	if err := s.ForgetMemory(ctx, wsA, one); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
	if got := ids(search(adapter.KeywordQuery{WorkspaceIDs: []string{wsA}, Query: "zircon"})); got[one] {
		t.Errorf("forgotten memory %s still matches", one)
	}

	// The old keyword path scanned only the 500 newest memories; the index
	// must find an older one.
	wsC := newWS("kw-depth")
	old := imprint(wsC, "", "basalt column survey")
	for i := 0; i < 510; i++ {
		imprint(wsC, "", "filler memory text")
	}
	if got := ids(search(adapter.KeywordQuery{WorkspaceIDs: []string{wsC}, Query: "basalt"})); !got[old] {
		t.Errorf("memory older than the newest 500 not found")
	}
}

// BatchLoadContract checks MetadataStore.GetMemories and
// GetCellsForMemories: one call returns exactly the live memories asked
// for, and every asked-for memory's cells in seq order.
func BatchLoadContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	ws := &types.Workspace{Name: "batch-" + types.NewID()}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	agent := types.NewID()
	var ids []string
	for i := 0; i < 3; i++ {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "batch", WrittenByAgentID: agent}
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		cells := []types.Cell{
			{Seq: 1, Text: "second", TextMD5: types.MD5Hex("second"), WrittenByAgentID: agent},
			{Seq: 0, Text: "first", TextMD5: types.MD5Hex("first"), WrittenByAgentID: agent},
		}
		if err := s.UpsertCells(ctx, m.ID, cells); err != nil {
			t.Fatalf("UpsertCells: %v", err)
		}
		ids = append(ids, m.ID)
	}
	if err := s.ForgetMemory(ctx, ws.ID, ids[2]); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}

	mems, err := s.GetMemories(ctx, []string{ids[0], ids[1], ids[2], types.NewID()})
	if err != nil {
		t.Fatalf("GetMemories: %v", err)
	}
	got := map[string]bool{}
	for _, m := range mems {
		got[m.ID] = true
	}
	if len(mems) != 2 || !got[ids[0]] || !got[ids[1]] {
		t.Fatalf("GetMemories = %v, want only the two live memories", got)
	}
	if empty, err := s.GetMemories(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("GetMemories(nil) = %v, %v", empty, err)
	}

	cells, err := s.GetCellsForMemories(ctx, ids[:2])
	if err != nil {
		t.Fatalf("GetCellsForMemories: %v", err)
	}
	for _, id := range ids[:2] {
		cs := cells[id]
		if len(cs) != 2 || cs[0].Seq != 0 || cs[0].Text != "first" || cs[1].Seq != 1 {
			t.Fatalf("cells for %s = %+v, want first, second in seq order", id, cs)
		}
	}
}

// AgentsPerWorkspaceContract checks that agents are registered per
// workspace (#6154): every reserved sentinel is registered in each
// workspace, and one agent ID can be registered in several workspaces.
func AgentsPerWorkspaceContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	for _, id := range types.ReservedAgentIDs {
		if _, err := types.ParseID("reserved agent_id", id); err != nil {
			t.Fatalf("reserved agent: %v", err)
		}
	}
	var wss []string
	for i := 0; i < 2; i++ {
		ws := &types.Workspace{Name: "agents-" + types.NewID()}
		if err := s.CreateWorkspace(ctx, ws); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		wss = append(wss, ws.ID)
	}
	for _, ws := range wss {
		for _, id := range types.ReservedAgentIDs {
			a, err := s.GetAgent(ctx, ws, id)
			if err != nil {
				t.Fatalf("sentinel %s in workspace %s: %v", id, ws, err)
			}
			if a.DisplayName != types.ReservedAgentDisplayNames[id] {
				t.Errorf("sentinel %s display name = %q", id, a.DisplayName)
			}
		}
	}
	shared := types.NewID()
	for _, ws := range wss {
		if err := s.RegisterAgent(ctx, &types.Agent{AgentID: shared, WorkspaceID: ws, IdentityProvider: "opaque"}); err != nil {
			t.Fatalf("RegisterAgent in %s: %v", ws, err)
		}
	}
	for _, ws := range wss {
		agents, err := s.ListAgents(ctx, ws, 100)
		if err != nil {
			t.Fatalf("ListAgents: %v", err)
		}
		found := false
		for _, a := range agents {
			found = found || a.AgentID == shared
		}
		if !found {
			t.Errorf("agent %s registered in %s is not listed there", shared, ws)
		}
	}
}

// ListChronologicalContract checks ListMemories and ListWorkspaces (ID-6):
// newest first by (created_at, id), stable across pages, whatever zone
// created_at was given in, with ties broken by id and a bad cursor
// refused as ErrInvalidInput.
func ListChronologicalContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	// Later than anything an earlier run left in a shared database, so
	// this run's workspaces are the newest. Microseconds, which every
	// store keeps.
	base := time.Now().UTC().AddDate(900, 0, 0).Truncate(time.Second)
	ist := time.FixedZone("IST", 5*3600+30*60)
	pst := time.FixedZone("PST", -8*3600)
	at := []time.Time{
		base,
		base.Add(30 * time.Minute).In(ist), // later, but "…+05:30" wall clock reads later still
		base.Add(time.Hour).In(pst),        // later, wall clock reads earlier
		base.Add(2 * time.Hour),            // two-way tie
		base.Add(2 * time.Hour),
		base.Add(3*time.Hour + 100*time.Millisecond),
		base.Add(3*time.Hour + 120*time.Millisecond),
		base.Add(3*time.Hour + 500*time.Millisecond),
	}
	type row struct {
		t  time.Time
		id string
	}
	newestFirst := func(rows []row) []string {
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].t.Equal(rows[j].t) {
				return rows[i].t.After(rows[j].t)
			}
			return rows[i].id > rows[j].id
		})
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.id
		}
		return ids
	}

	ws := &types.Workspace{Name: "list-" + types.NewID()}
	other := &types.Workspace{Name: "list-other-" + types.NewID()}
	for _, w := range []*types.Workspace{ws, other} {
		if err := s.CreateWorkspace(ctx, w); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
	}
	agent := types.NewID()
	var rows []row
	for _, ts := range at {
		m := &types.Memory{WorkspaceID: ws.ID, Content: "list", WrittenByAgentID: agent, CreatedAt: ts}
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		rows = append(rows, row{ts, m.ID})
	}
	gone := &types.Memory{WorkspaceID: ws.ID, Content: "forgotten", WrittenByAgentID: agent, CreatedAt: base.Add(4 * time.Hour)}
	foreign := &types.Memory{WorkspaceID: other.ID, Content: "other", WrittenByAgentID: agent, CreatedAt: base.Add(4 * time.Hour)}
	for _, m := range []*types.Memory{gone, foreign} {
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
	}
	if err := s.ForgetMemory(ctx, ws.ID, gone.ID); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
	want := newestFirst(rows)

	var got []string
	cursor, pages := "", 0
	for {
		page, next, err := s.ListMemories(ctx, ws.ID, "", cursor, 3)
		if err != nil {
			t.Fatalf("ListMemories(cursor %q): %v", cursor, err)
		}
		pages++
		for _, m := range page {
			got = append(got, m.ID)
		}
		if next == "" {
			break
		}
		if pages > len(want) {
			t.Fatal("ListMemories: cursor never ends")
		}
		cursor = next
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ListMemories pages = %v\nwant newest first  %v", got, want)
	}
	if pages != 3 {
		t.Fatalf("ListMemories took %d pages of 3 for %d rows, want 3", pages, len(want))
	}
	if all, next, err := s.ListMemories(ctx, ws.ID, "", "", 100); err != nil || next != "" || len(all) != len(want) {
		t.Fatalf("one page = %d rows, next %q, err %v; want %d rows and no next", len(all), next, err, len(want))
	}
	if _, _, err := s.ListMemories(ctx, ws.ID, "", "not-a-cursor", 3); !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("bad cursor: err = %v, want ErrInvalidInput", err)
	}

	var wrows []row
	for i, ts := range []time.Time{at[1], at[2], at[3], at[4]} {
		w := &types.Workspace{Name: fmt.Sprintf("chrono-%d-%s", i, types.NewID()), CreatedAt: ts.Add(24 * time.Hour)}
		if err := s.CreateWorkspace(ctx, w); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		wrows = append(wrows, row{ts.Add(24 * time.Hour), w.ID})
	}
	wantWS := newestFirst(wrows)
	// A shared database may hold workspaces from other runs, so compare
	// the order this run's workspaces come back in, across pages of 3.
	mine := map[string]bool{}
	for _, r := range wrows {
		mine[r.id] = true
	}
	var gotWS []string
	cursor = ""
	for len(gotWS) < len(wantWS) {
		page, next, err := s.ListWorkspaces(ctx, cursor, 3)
		if err != nil {
			t.Fatalf("ListWorkspaces: %v", err)
		}
		for _, w := range page {
			if mine[w.ID] {
				gotWS = append(gotWS, w.ID)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if fmt.Sprint(gotWS) != fmt.Sprint(wantWS) {
		t.Fatalf("ListWorkspaces newest = %v\nwant              %v", gotWS, wantWS)
	}
	if _, _, err := s.ListWorkspaces(ctx, "bm90LWEtY3Vyc29y", 3); !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("bad workspace cursor: err = %v, want ErrInvalidInput", err)
	}
}

// PeerStoreContract checks the federation peer registry (F-7): upsert
// and list round-trip every field, sealed bytes included; a seed never
// overwrites a row, live or deleted; deletion is a tombstone that an
// upsert revives; deleting an absent peer is ErrNotFound.
func PeerStoreContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	find := func(id string) *adapter.PeerRecord {
		t.Helper()
		peers, err := s.ListPeers(ctx)
		if err != nil {
			t.Fatalf("ListPeers: %v", err)
		}
		for i := range peers {
			if peers[i].NodeID == id {
				return &peers[i]
			}
		}
		return nil
	}
	p := adapter.PeerRecord{
		NodeID: types.NewID(), Name: "peer", URL: "https://peer.example.com",
		InboundKeySHA256: "ab", SealedAPIKey: []byte{0, 1, 2, 255}, ClientCertSHA256: "cd",
		TimeoutMS: 1500, RateLimitQPS: 2.5, InboundQPS: 7, Revoked: true,
	}
	if err := s.UpsertPeer(ctx, p); err != nil {
		t.Fatalf("UpsertPeer: %v", err)
	}
	got := find(p.NodeID)
	if got == nil || got.Name != "peer" || got.URL != p.URL || got.InboundKeySHA256 != "ab" ||
		string(got.SealedAPIKey) != string(p.SealedAPIKey) || got.ClientCertSHA256 != "cd" ||
		got.TimeoutMS != 1500 || got.RateLimitQPS != 2.5 || got.InboundQPS != 7 || !got.Revoked || got.CreatedAt.IsZero() {
		t.Fatalf("round trip = %+v, want %+v", got, p)
	}
	created := got.CreatedAt

	seeded := p
	seeded.Name, seeded.URL = "from-config", "https://config.example.com"
	if ok, err := s.SeedPeer(ctx, seeded); err != nil || ok {
		t.Fatalf("SeedPeer over a live row = %v, %v; want false", ok, err)
	}
	if got := find(p.NodeID); got.Name != "peer" {
		t.Fatalf("seed overwrote the peer: %+v", got)
	}

	p.Name, p.Revoked = "renamed", false
	if err := s.UpsertPeer(ctx, p); err != nil {
		t.Fatalf("UpsertPeer: %v", err)
	}
	if got := find(p.NodeID); got.Name != "renamed" || got.Revoked || !got.CreatedAt.Equal(created) {
		t.Fatalf("update = %+v, want renamed, not revoked, created_at kept", got)
	}

	if err := s.DeletePeer(ctx, p.NodeID); err != nil {
		t.Fatalf("DeletePeer: %v", err)
	}
	if got := find(p.NodeID); got != nil {
		t.Fatalf("deleted peer still listed: %+v", got)
	}
	if err := s.DeletePeer(ctx, p.NodeID); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("second DeletePeer = %v, want ErrNotFound", err)
	}
	if ok, err := s.SeedPeer(ctx, seeded); err != nil || ok {
		t.Fatalf("SeedPeer over a tombstone = %v, %v; want false", ok, err)
	}
	if got := find(p.NodeID); got != nil {
		t.Fatalf("seed revived a deleted peer: %+v", got)
	}
	if err := s.UpsertPeer(ctx, p); err != nil {
		t.Fatalf("UpsertPeer after delete: %v", err)
	}
	if got := find(p.NodeID); got == nil || got.Name != "renamed" {
		t.Fatalf("upsert did not revive the peer: %+v", got)
	}

	fresh := adapter.PeerRecord{NodeID: types.NewID(), Name: "seed", URL: "https://s.example.com"}
	if ok, err := s.SeedPeer(ctx, fresh); err != nil || !ok {
		t.Fatalf("SeedPeer of a new peer = %v, %v; want true", ok, err)
	}
	if got := find(fresh.NodeID); got == nil || got.SealedAPIKey != nil && len(got.SealedAPIKey) != 0 {
		t.Fatalf("seeded peer = %+v", got)
	}
	if err := s.UpsertPeer(ctx, adapter.PeerRecord{NodeID: "peer_1", Name: "x", URL: "https://x"}); !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("UpsertPeer with a bad node_id = %v, want ErrInvalidInput", err)
	}
}

// KeywordFilterContract checks that SearchKeyword applies its Filter in
// the query (S-4): agent, agent sets, a time range and tags restrict the
// K results to matching memories, even when non-matching memories rank
// higher.
func KeywordFilterContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	ws := &types.Workspace{Name: "kwf-" + types.NewID()}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	agentA, agentB := types.NewID(), types.NewID()
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	write := func(agent string, at time.Time, tags map[string]string, content string) string {
		m := &types.Memory{WorkspaceID: ws.ID, Content: content, WrittenByAgentID: agent, Tags: tags, CreatedAt: at}
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatalf("ImprintMemory: %v", err)
		}
		return m.ID
	}
	// Decoys rank first ("obsidian" four times) and fail every filter.
	for i := 0; i < 8; i++ {
		write(agentA, base, map[string]string{"kind": "decoy"}, "obsidian obsidian obsidian obsidian decoy")
	}
	byB := write(agentB, base, map[string]string{"kind": "decoy"}, "obsidian once, by agent b")
	tagged := write(agentA, base, map[string]string{"kind": "target", "site": "north"}, "obsidian once, tagged")
	late := write(agentA, base.Add(30*time.Minute), map[string]string{"kind": "decoy"}, "obsidian once, written late")

	search := func(f adapter.MemoryFilter) map[string]bool {
		t.Helper()
		hits, err := s.SearchKeyword(ctx, adapter.KeywordQuery{WorkspaceIDs: []string{ws.ID}, Query: "obsidian", Filter: f, K: 3})
		if err != nil {
			t.Fatalf("SearchKeyword(%+v): %v", f, err)
		}
		got := map[string]bool{}
		for _, h := range hits {
			got[h.MemoryID] = true
		}
		return got
	}
	lateFrom := base.Add(10 * time.Minute)
	for name, c := range map[string]struct {
		f    adapter.MemoryFilter
		want []string
	}{
		"agent":       {adapter.MemoryFilter{AgentID: agentB}, []string{byB}},
		"agent in":    {adapter.MemoryFilter{AgentIDIn: []string{agentB, types.NewID()}}, []string{byB}},
		"tag":         {adapter.MemoryFilter{Tags: map[string][]string{"kind": {"target", "other"}}}, []string{tagged}},
		"two tags":    {adapter.MemoryFilter{Tags: map[string][]string{"kind": {"target"}, "site": {"north"}}}, []string{tagged}},
		"after":       {adapter.MemoryFilter{TsAfter: &lateFrom}, []string{late}},
		"before+tag":  {adapter.MemoryFilter{TsBefore: &lateFrom, Tags: map[string][]string{"kind": {"target"}}}, []string{tagged}},
		"agent notin": {adapter.MemoryFilter{AgentIDNotIn: []string{agentA}}, []string{byB}},
	} {
		got := search(c.f)
		if len(got) != len(c.want) {
			t.Errorf("%s: hits %v, want %v", name, got, c.want)
			continue
		}
		for _, id := range c.want {
			if !got[id] {
				t.Errorf("%s: hits %v, want %v", name, got, c.want)
			}
		}
	}
	if got := search(adapter.MemoryFilter{Tags: map[string][]string{"kind": {"target"}, "site": {"south"}}}); len(got) != 0 {
		t.Errorf("tags that no memory has both of matched %v", got)
	}
}

// TagEditContract checks that UpsertTag and DeleteTag change the tags
// every reader sees (#6182): GetMemory, GetMemories and keyword filter
// pushdown agree after each edit; edits are scoped to the memory's
// workspace; a forgotten memory cannot be tagged.
func TagEditContract(t *testing.T, s adapter.MetadataStore) {
	t.Helper()
	ctx := context.Background()
	ws, other := &types.Workspace{Name: "tags-" + types.NewID()}, &types.Workspace{Name: "tags-other-" + types.NewID()}
	for _, w := range []*types.Workspace{ws, other} {
		if err := s.CreateWorkspace(ctx, w); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
	}
	m := &types.Memory{WorkspaceID: ws.ID, Content: "tagged basalt sample", WrittenByAgentID: types.NewID(), Tags: map[string]string{"k": "v"}}
	if _, err := s.ImprintMemory(ctx, m); err != nil {
		t.Fatalf("ImprintMemory: %v", err)
	}
	check := func(step string, want map[string]string) {
		t.Helper()
		got, err := s.GetMemory(ctx, m.ID)
		if err != nil {
			t.Fatalf("%s: GetMemory: %v", step, err)
		}
		if fmt.Sprint(got.Tags) != fmt.Sprint(want) && !(len(got.Tags) == 0 && len(want) == 0) {
			t.Errorf("%s: GetMemory tags = %v, want %v", step, got.Tags, want)
		}
		batch, err := s.GetMemories(ctx, []string{m.ID})
		if err != nil || len(batch) != 1 || fmt.Sprint(batch[0].Tags) != fmt.Sprint(got.Tags) {
			t.Errorf("%s: GetMemories = %+v, %v; want the same tags as GetMemory", step, batch, err)
		}
		for k, v := range want {
			hits, err := s.SearchKeyword(ctx, adapter.KeywordQuery{WorkspaceIDs: []string{ws.ID}, Query: "basalt", K: 5,
				Filter: adapter.MemoryFilter{Tags: map[string][]string{k: {v}}}})
			if err != nil || len(hits) != 1 {
				t.Errorf("%s: keyword search with tag %s=%s: %d hits, %v; want the memory", step, k, v, len(hits), err)
			}
		}
	}
	check("imprint", map[string]string{"k": "v"})

	if err := s.UpsertTag(ctx, ws.ID, m.ID, "team", "red"); err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	check("upsert", map[string]string{"k": "v", "team": "red"})
	if err := s.UpsertTag(ctx, ws.ID, m.ID, "team", "blue"); err != nil {
		t.Fatalf("UpsertTag (change): %v", err)
	}
	check("change", map[string]string{"k": "v", "team": "blue"})
	if err := s.DeleteTag(ctx, ws.ID, m.ID, "k"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	check("delete", map[string]string{"team": "blue"})
	hits, err := s.SearchKeyword(ctx, adapter.KeywordQuery{WorkspaceIDs: []string{ws.ID}, Query: "basalt", K: 5,
		Filter: adapter.MemoryFilter{Tags: map[string][]string{"k": {"v"}}}})
	if err != nil || len(hits) != 0 {
		t.Errorf("deleted tag still matches: %d hits, %v", len(hits), err)
	}

	// Another workspace cannot edit it.
	if err := s.UpsertTag(ctx, other.ID, m.ID, "team", "green"); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("UpsertTag from another workspace: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTag(ctx, other.ID, m.ID, "team"); err != nil {
		t.Errorf("DeleteTag from another workspace: %v", err)
	}
	check("foreign edits", map[string]string{"team": "blue"})

	if err := s.DeleteTag(ctx, ws.ID, m.ID, "team"); err != nil {
		t.Fatalf("DeleteTag (last): %v", err)
	}
	check("last tag deleted", map[string]string{})

	if err := s.ForgetMemory(ctx, ws.ID, m.ID); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
	if err := s.UpsertTag(ctx, ws.ID, m.ID, "team", "red"); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("UpsertTag on a forgotten memory: err = %v, want ErrNotFound", err)
	}
}
