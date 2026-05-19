package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func openTestFileStore(t *testing.T) *ContentStore {
	t.Helper()
	cs := &ContentStore{}
	root := t.TempDir()
	if err := cs.Open(context.Background(), adapter.ContentConfig{Driver: "file", DSN: root}); err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestFileContentStore_MemoryRoundtrip(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()
	ws, mem := "ws-1", "mem-1"

	if err := cs.PutMemoryContent(ctx, ws, mem, "md5", "hello disk"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetMemoryContent(ctx, ws, mem)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello disk" {
		t.Errorf("got %q, want %q", got, "hello disk")
	}

	if err := cs.DeleteMemoryContent(ctx, ws, mem); err != nil {
		t.Fatal(err)
	}
	_, err = cs.GetMemoryContent(ctx, ws, mem)
	if err != types.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileContentStore_CellRoundtrip(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	if err := cs.PutCellContent(ctx, "ws-1", "mem-1", "cell-1", "md5", "chunk data"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetCellContent(ctx, "ws-1", "mem-1", "cell-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "chunk data" {
		t.Errorf("got %q, want %q", got, "chunk data")
	}
}

func TestFileContentStore_BatchGet(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	_ = cs.PutCellContent(ctx, "ws-1", "mem-1", "c1", "md5", "t1")
	_ = cs.PutCellContent(ctx, "ws-1", "mem-1", "c2", "md5", "t2")

	got, err := cs.GetCellContentBatch(ctx, "ws-1", "mem-1", []string{"c1", "c2", "c-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("batch got %d, want 2", len(got))
	}
}

func TestFileContentStore_DeleteAllForMemory(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	_ = cs.PutMemoryContent(ctx, "ws-1", "mem-1", "md5", "body")
	_ = cs.PutCellContent(ctx, "ws-1", "mem-1", "c1", "md5", "cell")

	if err := cs.DeleteAllForMemory(ctx, "ws-1", "mem-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetMemoryContent(ctx, "ws-1", "mem-1"); err != types.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileContentStore_FilePermissions(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	if err := cs.PutMemoryContent(ctx, "ws-1", "mem-1", "md5", "secure content"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(cs.root, "ws-1", "mem-1", "memory.txt")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestFileContentStore_PathTraversalRejected(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	cases := []struct{ ws, mem string }{
		{"../../etc", "passwd"},
		{"ws_a", "../mem_evil"},
		{"ws_a/../escape", "mem_x"},
		{"ws_a\x00null", "mem_x"},
		{"ws a space", "mem_x"},
		{"ws_a", "mem/slash"},
	}
	for _, c := range cases {
		err := cs.PutMemoryContent(ctx, c.ws, c.mem, "", "x")
		if err == nil {
			t.Errorf("traversal not rejected for ws=%q mem=%q", c.ws, c.mem)
		}
	}

	if err := cs.PutMemoryContent(ctx, "ws_abc", "mem_xyz", "", "hello"); err != nil {
		t.Errorf("valid ids rejected: %v", err)
	}
}

func TestFileContentStore_WorkspaceIsolation(t *testing.T) {
	cs := openTestFileStore(t)
	ctx := context.Background()

	_ = cs.PutMemoryContent(ctx, "ws-A", "mem-1", "md5", "A content")
	_ = cs.PutMemoryContent(ctx, "ws-B", "mem-1", "md5", "B content")

	gotA, _ := cs.GetMemoryContent(ctx, "ws-A", "mem-1")
	gotB, _ := cs.GetMemoryContent(ctx, "ws-B", "mem-1")
	if gotA != "A content" || gotB != "B content" {
		t.Errorf("isolation failed: A=%q B=%q", gotA, gotB)
	}
}
