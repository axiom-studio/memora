package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func vectorRows(t *testing.T, dir, memoryID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "memora.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memora_cell_vectors WHERE memory_id = ?`, memoryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestForget_DeletesVectors (#6179): forgetting a memory removes every
// vector of it, and only its vectors.
func TestForget_DeletesVectors(t *testing.T) {
	dir := t.TempDir()
	s := openWithModel(t, dir, "hash:default")
	ws := newTestWorkspace(t, s)
	gone := imprint(t, s, ws, "first paragraph of the forgotten memory\n\nsecond paragraph of the forgotten memory")
	kept := imprint(t, s, ws, "a memory that stays")
	if vectorRows(t, dir, gone) == 0 || vectorRows(t, dir, kept) == 0 {
		t.Fatal("imprint wrote no vectors")
	}
	if _, err := s.Forget(context.Background(), ws, gone, testAgent); err != nil {
		t.Fatal(err)
	}
	if n := vectorRows(t, dir, gone); n != 0 {
		t.Fatalf("%d vectors left for the forgotten memory", n)
	}
	if vectorRows(t, dir, kept) == 0 {
		t.Fatal("forget removed another memory's vectors")
	}
}

// TestReembed_RemovesForgottenMemoriesVectors (#6179): vectors left by
// a forget before the fix are removed by reembed (counted by a dry run,
// deleted by a real one), so stale_vectors reaches 0 after a model
// switch.
func TestReembed_RemovesForgottenMemoriesVectors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := openWithModel(t, dir, "hash:default")
	ws := newTestWorkspace(t, a)
	live := imprint(t, a, ws, "glacier memory that stays")
	orphan := imprint(t, a, ws, "glacier memory forgotten by the old binary")
	// As the old Forget did: metadata gone, vectors left behind.
	if err := a.Metadata.ForgetMemory(ctx, ws, orphan); err != nil {
		t.Fatal(err)
	}
	if vectorRows(t, dir, orphan) == 0 {
		t.Fatal("setup: no orphan vectors")
	}

	b := openWithModel(t, dir, "hash:384")
	mav := b.Vector.(adapter.ModelAwareVectorStore)
	dry, err := b.Reembed(ctx, ReembedOptions{DryRun: true}, nil)
	if err != nil || dry.Orphans != 1 || vectorRows(t, dir, orphan) == 0 {
		t.Fatalf("dry run = %+v, %v; orphan rows %d; want 1 counted, none removed", dry, err, vectorRows(t, dir, orphan))
	}
	res, err := b.Reembed(ctx, ReembedOptions{Batch: 1}, nil)
	if err != nil || res.Orphans != 1 || res.Reembedded == 0 {
		t.Fatalf("reembed = %+v, %v", res, err)
	}
	if n := vectorRows(t, dir, orphan); n != 0 {
		t.Fatalf("%d orphan vectors left after reembed", n)
	}
	if vectorRows(t, dir, live) == 0 {
		t.Fatal("reembed removed a live memory's vectors")
	}
	if n, err := mav.StaleVectors(ctx); err != nil || n != 0 {
		t.Fatalf("stale vectors after reembed = %d, %v; want 0", n, err)
	}
}
