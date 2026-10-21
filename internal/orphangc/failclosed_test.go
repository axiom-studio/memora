package orphangc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/internal/store/file"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// Issue #5991: the sweeper must delete content only for memories that are
// really gone. These tests use the real sqlite metadata store and the real
// file content store (the driver that ignored ctx).

type realStores struct {
	meta    *storesqlite.Store
	content *file.ContentStore
	ws      string
}

func newRealStores(t *testing.T) realStores {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	content := &file.ContentStore{}
	if err := content.Open(ctx, adapter.ContentConfig{Driver: "file", DSN: filepath.Join(dir, "content")}); err != nil {
		t.Fatal(err)
	}
	ws := &types.Workspace{Name: "gc"}
	if err := meta.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	return realStores{meta: meta, content: content, ws: ws.ID}
}

// liveMemory imprints a memory and writes its content-store copy.
func (r realStores) liveMemory(t *testing.T, body string) string {
	t.Helper()
	ctx := context.Background()
	m := &types.Memory{WorkspaceID: r.ws, Content: body, WrittenByAgentID: "2986919d-4168-42c5-a3e9-134e802edf7b"}
	if _, err := r.meta.ImprintMemory(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := r.content.PutMemoryContent(ctx, r.ws, m.ID, types.MD5Hex(body), body); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func (r realStores) hasContent(t *testing.T, memID string) bool {
	t.Helper()
	_, err := r.content.GetMemoryContent(context.Background(), r.ws, memID)
	return err == nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// failingMetadata makes every GetMemory fail with a non-NotFound error.
type failingMetadata struct {
	adapter.MetadataStore
	err func(ctx context.Context) error
}

func (f failingMetadata) GetMemory(ctx context.Context, _ string) (*types.Memory, error) {
	return nil, f.err(ctx)
}

func TestSweep_TransientMetadataError_DeletesNothing(t *testing.T) {
	r := newRealStores(t)
	ids := []string{r.liveMemory(t, "a"), r.liveMemory(t, "b")}
	meta := failingMetadata{MetadataStore: r.meta, err: func(context.Context) error { return errors.New("database is locked") }}

	sw := New(meta, r.content, nil, quietLogger(), Config{MinAge: -1})
	sw.cfg.MinAge = 0 // New() defaults non-positive MinAge to 1h; include brand-new content
	sw.sweep(context.Background())

	for _, id := range ids {
		if !r.hasContent(t, id) {
			t.Fatalf("live memory %s lost its content on a transient metadata error", id)
		}
	}
	if sw.LastDeleted() != 0 {
		t.Fatalf("LastDeleted = %d, want 0", sw.LastDeleted())
	}
}

func TestSweep_CancelledMidPass_KeepsLiveContent(t *testing.T) {
	r := newRealStores(t)
	ids := []string{r.liveMemory(t, "a"), r.liveMemory(t, "b"), r.liveMemory(t, "c")}

	ctx, cancel := context.WithCancel(context.Background())
	// The first GetMemory simulates shutdown: cancel, then fail the way
	// QueryRowContext does on a cancelled context.
	meta := failingMetadata{MetadataStore: r.meta, err: func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}}
	sw := New(meta, r.content, nil, quietLogger(), Config{})
	sw.cfg.MinAge = 0
	sw.sweep(ctx)

	for _, id := range ids {
		if !r.hasContent(t, id) {
			t.Fatalf("live memory %s lost its content when the pass was cancelled", id)
		}
	}
}

func TestSweep_ForgottenMemory_ContentReclaimed(t *testing.T) {
	r := newRealStores(t)
	live := r.liveMemory(t, "keep")
	gone := r.liveMemory(t, "forget me")
	if err := r.meta.ForgetMemory(context.Background(), r.ws, gone); err != nil {
		t.Fatal(err)
	}

	sw := New(r.meta, r.content, nil, quietLogger(), Config{})
	sw.cfg.MinAge = 0
	sw.sweep(context.Background())

	if r.hasContent(t, gone) {
		t.Fatal("forgotten memory's content survived the sweep (GDPR Art. 17 backstop)")
	}
	if !r.hasContent(t, live) {
		t.Fatal("live memory's content was reclaimed")
	}
}

func TestFileContentStore_DeleteAllForMemory_HonoursCancelledContext(t *testing.T) {
	r := newRealStores(t)
	id := r.liveMemory(t, "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.content.DeleteAllForMemory(ctx, r.ws, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteAllForMemory on cancelled ctx err = %v, want context.Canceled", err)
	}
	if !r.hasContent(t, id) {
		t.Fatal("content deleted despite a cancelled context")
	}
}

// A pass aborted by cancellation must not overwrite the last completed
// pass's LastDeleted (it reported 0 and broke TestSweeper_ReclaimsOrphans
// intermittently after #5991).
func TestSweep_AbortedPass_KeepsLastCompletedStats(t *testing.T) {
	r := newRealStores(t)
	gone := r.liveMemory(t, "orphan")
	if err := r.meta.ForgetMemory(context.Background(), r.ws, gone); err != nil {
		t.Fatal(err)
	}
	sw := New(r.meta, r.content, nil, quietLogger(), Config{})
	sw.cfg.MinAge = 0
	sw.sweep(context.Background())
	if sw.LastDeleted() != 1 {
		t.Fatalf("completed pass LastDeleted = %d, want 1", sw.LastDeleted())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sw.sweep(ctx)
	if sw.LastDeleted() != 1 {
		t.Fatalf("aborted pass overwrote LastDeleted with %d, want the completed pass's 1", sw.LastDeleted())
	}
}
