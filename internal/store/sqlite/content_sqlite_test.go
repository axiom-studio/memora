package sqlite

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func openTestContentStore(t *testing.T) *ContentStore {
	t.Helper()
	cs := &ContentStore{}
	dsn := t.TempDir() + "/content_test.db"
	if err := cs.Open(context.Background(), adapter.ContentConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestContentStore_MemoryRoundtrip(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()
	ws, mem := "ws-1", "mem-1"

	if err := cs.PutMemoryContent(ctx, ws, mem, "abc123", "hello world"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetMemoryContent(ctx, ws, mem)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}

	if err := cs.DeleteMemoryContent(ctx, ws, mem); err != nil {
		t.Fatal(err)
	}
	_, err = cs.GetMemoryContent(ctx, ws, mem)
	if err != types.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestContentStore_CellRoundtrip(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()
	ws, mem, cell := "ws-1", "mem-1", "cell-1"

	if err := cs.PutCellContent(ctx, ws, mem, cell, "md5abc", "chunk text"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetCellContent(ctx, ws, mem, cell)
	if err != nil {
		t.Fatal(err)
	}
	if got != "chunk text" {
		t.Errorf("got %q, want %q", got, "chunk text")
	}

	if err := cs.DeleteCellContent(ctx, ws, mem, cell); err != nil {
		t.Fatal(err)
	}
	_, err = cs.GetCellContent(ctx, ws, mem, cell)
	if err != types.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestContentStore_BatchGet(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()
	ws, mem := "ws-1", "mem-1"

	for _, id := range []string{"c1", "c2", "c3"} {
		if err := cs.PutCellContent(ctx, ws, mem, id, "md5", "text-"+id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cs.GetCellContentBatch(ctx, ws, mem, []string{"c1", "c3", "c-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("batch got %d results, want 2", len(got))
	}
	if got["c1"] != "text-c1" || got["c3"] != "text-c3" {
		t.Errorf("batch results = %v", got)
	}
}

func TestContentStore_IdempotentPut(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()
	ws, mem, cell := "ws-1", "mem-1", "cell-1"

	if err := cs.PutCellContent(ctx, ws, mem, cell, "md5a", "original"); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutCellContent(ctx, ws, mem, cell, "md5b", "updated"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetCellContent(ctx, ws, mem, cell)
	if err != nil {
		t.Fatal(err)
	}
	if got != "updated" {
		t.Errorf("got %q after re-put, want %q", got, "updated")
	}
}

func TestContentStore_DeleteAllForMemory(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()
	ws, mem := "ws-1", "mem-1"

	_ = cs.PutMemoryContent(ctx, ws, mem, "md5", "body")
	_ = cs.PutCellContent(ctx, ws, mem, "c1", "md5", "cell1")
	_ = cs.PutCellContent(ctx, ws, mem, "c2", "md5", "cell2")

	if err := cs.DeleteAllForMemory(ctx, ws, mem); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetMemoryContent(ctx, ws, mem); err != types.ErrNotFound {
		t.Errorf("memory content should be gone, got %v", err)
	}
	if _, err := cs.GetCellContent(ctx, ws, mem, "c1"); err != types.ErrNotFound {
		t.Errorf("cell c1 should be gone, got %v", err)
	}
}

func TestContentStore_WorkspaceIsolation(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()

	_ = cs.PutMemoryContent(ctx, "ws-A", "mem-1", "md5", "ws-A content")
	_ = cs.PutMemoryContent(ctx, "ws-B", "mem-1", "md5", "ws-B content")

	gotA, _ := cs.GetMemoryContent(ctx, "ws-A", "mem-1")
	gotB, _ := cs.GetMemoryContent(ctx, "ws-B", "mem-1")
	if gotA != "ws-A content" || gotB != "ws-B content" {
		t.Errorf("workspace isolation failed: A=%q B=%q", gotA, gotB)
	}
}

func TestContentStore_EmptyBatch(t *testing.T) {
	cs := openTestContentStore(t)
	ctx := context.Background()

	got, err := cs.GetCellContentBatch(ctx, "ws-1", "mem-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty batch should return empty map, got %v", got)
	}
}
