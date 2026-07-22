package queue_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/embed/queue"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// harness wires real SQLite-backed Primary and Vector stores plus a
// configurable Provider. Failure-injection providers and the recording
// ledger are local impls of public interfaces, used to exercise retry /
// dedup / ledger paths that real backends don't expose deterministically.
type harness struct {
	t        *testing.T
	ctx      context.Context
	metadata adapter.MetadataStore
	vector   adapter.VectorStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	primary := &storesqlite.Store{}
	if err := primary.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "primary.db")}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	vector := &sqlitevec.Store{}
	if err := vector.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vector.db"), Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	return &harness{t: t, ctx: ctx, metadata: primary, vector: vector}
}

func (h *harness) imprint(ws, content, agent string) *types.Memory {
	h.t.Helper()
	m := &types.Memory{WorkspaceID: ws, Content: content, WrittenByAgentID: agent}
	if _, err := h.metadata.ImprintMemory(h.ctx, m); err != nil {
		h.t.Fatalf("imprint: %v", err)
	}
	return m
}

func (h *harness) seedCells(memID, agent string, texts []string) []types.Cell {
	h.t.Helper()
	cells := make([]types.Cell, len(texts))
	for i, t := range texts {
		cells[i] = types.Cell{
			Seq:              i,
			Text:             t,
			TextMD5:          types.MD5Hex(t),
			WrittenByAgentID: agent,
		}
	}
	if err := h.metadata.UpsertCells(h.ctx, memID, cells); err != nil {
		h.t.Fatalf("upsert cells: %v", err)
	}
	stored, err := h.metadata.GetCells(h.ctx, memID)
	if err != nil {
		h.t.Fatalf("get cells: %v", err)
	}
	return stored
}

// --- Test-local providers -------------------------------------------------

// recordingProvider wraps a Provider and records the (texts) of each
// Embed call so tests can assert batch shape.
type recordingProvider struct {
	inner    embedding.Provider
	mu       sync.Mutex
	calls    [][]string
	failures atomic.Int32 // remaining failures to inject before success
	failErr  error
}

func (p *recordingProvider) Name() string    { return "recording" }
func (p *recordingProvider) ModelID() string { return p.inner.ModelID() }
func (p *recordingProvider) Dim() int        { return p.inner.Dim() }
func (p *recordingProvider) Capabilities() embedding.EmbeddingCapabilities {
	return p.inner.Capabilities()
}
func (p *recordingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	snapshot := append([]string(nil), texts...)
	p.calls = append(p.calls, snapshot)
	p.mu.Unlock()
	if p.failErr != nil && p.failures.Add(-1) >= 0 {
		return nil, p.failErr
	}
	return p.inner.Embed(ctx, texts)
}

func (p *recordingProvider) callShapes() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]string, len(p.calls))
	for i, c := range p.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// alwaysFailProvider returns the configured error on every Embed call.
type alwaysFailProvider struct {
	inner embedding.Provider
	err   error
	calls atomic.Int32
}

func (p *alwaysFailProvider) Name() string    { return "always-fail" }
func (p *alwaysFailProvider) ModelID() string { return p.inner.ModelID() }
func (p *alwaysFailProvider) Dim() int        { return p.inner.Dim() }
func (p *alwaysFailProvider) Capabilities() embedding.EmbeddingCapabilities {
	return p.inner.Capabilities()
}
func (p *alwaysFailProvider) Embed(_ context.Context, _ []string) ([][]float32, error) {
	p.calls.Add(1)
	return nil, p.err
}

// recordingLedger is an in-memory LedgerStore that records every
// Append call. Used by failure-path tests so verifications don't
// depend on the production ledger's Query implementation (which has
// an unrelated scan-time-column bug, tracked separately).
type recordingLedger struct {
	mu      sync.Mutex
	entries []api.LedgerEntry
}

func (r *recordingLedger) Open(context.Context, adapter.LedgerConfig) error { return nil }
func (r *recordingLedger) Close() error                                     { return nil }
func (r *recordingLedger) Ping(context.Context) error                       { return nil }
func (r *recordingLedger) Capabilities() adapter.LedgerCapabilities {
	return adapter.LedgerCapabilities{SupportsAppend: true, SupportsBatchAppend: true, SupportsQuery: true}
}
func (r *recordingLedger) Append(_ context.Context, e api.LedgerEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}
func (r *recordingLedger) AppendBatch(ctx context.Context, es []api.LedgerEntry) error {
	for _, e := range es {
		_ = r.Append(ctx, e)
	}
	return nil
}
func (r *recordingLedger) Query(_ context.Context, q adapter.LedgerQuery) ([]api.LedgerEntry, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]bool{}
	for _, op := range q.Op {
		want[op] = true
	}
	var out []api.LedgerEntry
	for _, e := range r.entries {
		if q.WorkspaceID != "" && e.WorkspaceID != q.WorkspaceID {
			continue
		}
		if len(want) > 0 && !want[e.Op] {
			continue
		}
		out = append(out, e)
	}
	return out, "", nil
}
func (r *recordingLedger) Redact(context.Context, string, []string) error { return nil }

// blockingProvider blocks its Embed call until release is signaled, so
// the dedup test can hold the first job in-flight while submitting the
// duplicate.
type blockingProvider struct {
	inner   embedding.Provider
	release chan struct{}
	calls   atomic.Int32
	mu      sync.Mutex
	seen    [][]string
}

func (p *blockingProvider) Name() string    { return "blocking" }
func (p *blockingProvider) ModelID() string { return p.inner.ModelID() }
func (p *blockingProvider) Dim() int        { return p.inner.Dim() }
func (p *blockingProvider) Capabilities() embedding.EmbeddingCapabilities {
	return p.inner.Capabilities()
}
func (p *blockingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	p.seen = append(p.seen, append([]string(nil), texts...))
	p.mu.Unlock()
	p.calls.Add(1)
	<-p.release
	return p.inner.Embed(ctx, texts)
}

// --- Tests ----------------------------------------------------------------

// TestEmbedNow_PatchedCellOnly_RestSkipped pins the headline economic
// property: re-embed exactly the cells whose text_md5 changed.
func TestEmbedNow_PatchedCellOnly_RestSkipped(t *testing.T) {
	h := newHarness(t)
	provider, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatalf("open provider: %v", err)
	}
	deps := queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: provider}

	ws := &types.Workspace{Name: "moat"}
	if err := h.metadata.CreateWorkspace(h.ctx, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	mem := h.imprint(ws.ID, "moat-content", "agent_opaque_m")

	texts := make([]string, 25)
	for i := range texts {
		texts[i] = fmt.Sprintf("cell-%d-content", i)
	}
	old := h.seedCells(mem.ID, "agent_opaque_m", texts)

	res0, err := queue.EmbedNow(h.ctx, deps, ws.ID, "", mem.ID, nil, old)
	if err != nil {
		t.Fatalf("initial EmbedNow: %v", err)
	}
	if len(res0.Reembed) != 25 {
		t.Fatalf("initial: want 25 reembed, got %d", len(res0.Reembed))
	}
	if len(res0.Skipped) != 0 {
		t.Fatalf("initial: want 0 skipped, got %d", len(res0.Skipped))
	}
	// Persist the vector_keys so the second pass can see them.
	if err := h.metadata.UpsertCells(h.ctx, mem.ID, old); err != nil {
		t.Fatalf("upsert cells after initial: %v", err)
	}
	oldWithVK, err := h.metadata.GetCells(h.ctx, mem.ID)
	if err != nil {
		t.Fatalf("get cells after initial: %v", err)
	}

	newCells := append([]types.Cell(nil), oldWithVK...)
	newCells[7].Text = "cell-7-content-PATCHED"
	newCells[7].TextMD5 = types.MD5Hex(newCells[7].Text)

	res1, err := queue.EmbedNow(h.ctx, deps, ws.ID, "", mem.ID, oldWithVK, newCells)
	if err != nil {
		t.Fatalf("patched EmbedNow: %v", err)
	}
	if len(res1.Reembed) != 1 {
		t.Fatalf("after patch: want 1 reembed, got %d (reembed=%v)", len(res1.Reembed), res1.Reembed)
	}
	if len(res1.Skipped) != 24 {
		t.Fatalf("after patch: want 24 skipped, got %d", len(res1.Skipped))
	}
}

// TestPool_Batching_RespectsBatchSize verifies the worker batches
// jobs up to BatchSize per Provider.Embed call.
func TestPool_Batching_RespectsBatchSize(t *testing.T) {
	h := newHarness(t)
	inner, _ := embedding.Open("noop:default")
	rec := &recordingProvider{inner: inner}

	ws := &types.Workspace{Name: "batching"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)
	mem := h.imprint(ws.ID, "x", "agent_opaque_b")
	texts := make([]string, 25)
	for i := range texts {
		texts[i] = fmt.Sprintf("b-%d", i)
	}
	stored := h.seedCells(mem.ID, "agent_opaque_b", texts)

	// Workers=1 so we deterministically observe call counts. BatchSize=8.
	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: rec},
		queue.PoolConfig{Workers: 1, BatchSize: 8, BatchFlushInterval: 50 * time.Millisecond})
	for _, c := range stored {
		pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: c})
	}
	pool.Close()

	shapes := rec.callShapes()
	if len(shapes) < 3 || len(shapes) > 4 {
		t.Fatalf("expected 3-4 Embed calls for 25 jobs at batch=8, got %d (shapes=%v)", len(shapes), shapes)
	}
	total := 0
	for _, s := range shapes {
		if len(s) > 8 {
			t.Fatalf("batch exceeded BatchSize: %d", len(s))
		}
		total += len(s)
	}
	if total != 25 {
		t.Fatalf("expected 25 total embedded cells across batches, got %d", total)
	}
}

// TestPool_RecallReadyFlip flips recall_ready=1 only when every cell
// of a memory has been embedded.
func TestPool_RecallReadyFlip(t *testing.T) {
	h := newHarness(t)
	provider, _ := embedding.Open("noop:default")

	ws := &types.Workspace{Name: "ready"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)

	memA := h.imprint(ws.ID, "a", "agent_opaque_r")
	cellsA := h.seedCells(memA.ID, "agent_opaque_r", []string{"a0", "a1", "a2"})
	memB := h.imprint(ws.ID, "b", "agent_opaque_r")
	cellsB := h.seedCells(memB.ID, "agent_opaque_r", []string{"b0", "b1"})

	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: provider},
		queue.PoolConfig{Workers: 2, BatchSize: 4, BatchFlushInterval: 20 * time.Millisecond})
	// Memory A: submit all 3 cells → should flip.
	for _, c := range cellsA {
		pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: memA.ID, Cell: c})
	}
	// Memory B: submit only 1 of 2 → must NOT flip.
	pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: memB.ID, Cell: cellsB[0]})
	pool.Close()

	gotA, err := h.metadata.GetMemory(h.ctx, memA.ID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if !gotA.RecallReady {
		t.Fatal("Memory A: expected RecallReady=true after all cells embedded")
	}
	gotB, err := h.metadata.GetMemory(h.ctx, memB.ID)
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if gotB.RecallReady {
		t.Fatal("Memory B: RecallReady flipped while 1 of 2 cells unembedded")
	}
}

// TestPool_RetryWithBackoff confirms a transient embed failure is
// retried up to RetryAttempts and that the cell ends up embedded.
func TestPool_RetryWithBackoff(t *testing.T) {
	h := newHarness(t)
	inner, _ := embedding.Open("noop:default")
	rec := &recordingProvider{inner: inner, failErr: errors.New("transient: upstream 503")}
	rec.failures.Store(2) // first 2 attempts fail, third succeeds
	rl := &recordingLedger{}

	ws := &types.Workspace{Name: "retry"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)
	mem := h.imprint(ws.ID, "r", "agent_opaque_rt")
	cells := h.seedCells(mem.ID, "agent_opaque_rt", []string{"rt0"})

	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: rec, Ledger: rl},
		queue.PoolConfig{Workers: 1, BatchSize: 4, BatchFlushInterval: 10 * time.Millisecond,
			RetryAttempts: 3, RetryBaseDelay: 5 * time.Millisecond})
	pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: cells[0]})
	pool.Close()

	if got := len(rec.callShapes()); got != 3 {
		t.Fatalf("expected 3 Embed attempts (2 fail + 1 success), got %d", got)
	}
	stored, _ := h.metadata.GetCells(h.ctx, mem.ID)
	if stored[0].VectorKey == "" {
		t.Fatal("cell has empty vector_key after successful retry")
	}
	// No embed_failed ledger entry should land — retries succeeded.
	entries, _, err := rl.Query(h.ctx, adapter.LedgerQuery{WorkspaceID: ws.ID, Op: []string{queue.OpEmbedFailed}})
	if err != nil {
		t.Fatalf("ledger query: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 embed_failed entries on successful retry, got %d", len(entries))
	}
}

// TestPool_PermanentFailure_AppendsEmbedFailed verifies that after the
// retry budget is exhausted, each failed cell produces an `embed_failed`
// ledger entry and the memory's recall_ready stays false. Uses an
// in-memory recordingLedger (real LedgerStore impl) to sidestep the
// sqlite ledger Query scan bug tracked in a follow-up issue.
func TestPool_PermanentFailure_AppendsEmbedFailed(t *testing.T) {
	h := newHarness(t)
	inner, _ := embedding.Open("noop:default")
	bad := &alwaysFailProvider{inner: inner, err: errors.New("permanent: bad API key")}
	rl := &recordingLedger{}

	ws := &types.Workspace{Name: "fail"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)
	mem := h.imprint(ws.ID, "f", "agent_opaque_f")
	cells := h.seedCells(mem.ID, "agent_opaque_f", []string{"f0", "f1", "f2"})

	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: bad, Ledger: rl},
		queue.PoolConfig{Workers: 1, BatchSize: 8, BatchFlushInterval: 10 * time.Millisecond,
			RetryAttempts: 2, RetryBaseDelay: 1 * time.Millisecond})
	for _, c := range cells {
		pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: c})
	}
	pool.Close()

	// One batched call per attempt → 2 calls total (RetryAttempts=2).
	if got := bad.calls.Load(); got != 2 {
		t.Fatalf("expected 2 Embed attempts at RetryAttempts=2, got %d", got)
	}
	entries, _, err := rl.Query(h.ctx, adapter.LedgerQuery{WorkspaceID: ws.ID, Op: []string{queue.OpEmbedFailed}})
	if err != nil {
		t.Fatalf("ledger query: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 embed_failed entries (one per cell), got %d", len(entries))
	}
	seenCells := map[string]bool{}
	for _, e := range entries {
		if e.Target != mem.ID {
			t.Errorf("entry target %q != memory %q", e.Target, mem.ID)
		}
		cid, _ := e.Metadata["cell_id"].(string)
		if cid == "" {
			t.Errorf("entry missing cell_id metadata: %+v", e.Metadata)
		}
		seenCells[cid] = true
		if _, ok := e.Metadata["error"]; !ok {
			t.Errorf("entry missing error metadata: %+v", e.Metadata)
		}
	}
	if len(seenCells) != 3 {
		t.Fatalf("expected ledger entries for 3 distinct cells, got %d (entries=%+v)", len(seenCells), entries)
	}
	got, _ := h.metadata.GetMemory(h.ctx, mem.ID)
	if got.RecallReady {
		t.Fatal("recall_ready flipped after permanent embed failure")
	}
	// Cells stay with empty vector_key.
	stored, _ := h.metadata.GetCells(h.ctx, mem.ID)
	for _, c := range stored {
		if c.VectorKey != "" {
			t.Fatalf("cell %s has vector_key after permanent failure", c.CellID)
		}
	}
}

// TestPool_InFlightDedup_SameCell submits two jobs for the same cell
// while the first is blocked in-flight; the second must be dropped
// before reaching Provider.Embed.
func TestPool_InFlightDedup_SameCell(t *testing.T) {
	h := newHarness(t)
	inner, _ := embedding.Open("noop:default")
	release := make(chan struct{})
	bp := &blockingProvider{inner: inner, release: release}

	ws := &types.Workspace{Name: "dedup"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)
	mem := h.imprint(ws.ID, "d", "agent_opaque_d")
	cells := h.seedCells(mem.ID, "agent_opaque_d", []string{"d0"})

	// Workers=2 so the duplicate has a chance to be picked up by the
	// other worker, exercising the cross-worker dedup gate.
	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: bp},
		queue.PoolConfig{Workers: 2, BatchSize: 1, BatchFlushInterval: 5 * time.Millisecond})
	pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: cells[0]})
	// Wait for the first Embed to enter the blocking provider.
	deadline := time.Now().Add(2 * time.Second)
	for bp.calls.Load() == 0 {
		if time.Now().After(deadline) {
			close(release)
			pool.Close()
			t.Fatal("first Embed never entered blocking provider")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Submit the duplicate while the first holds the in-flight gate.
	pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: cells[0]})
	// Give the duplicate a moment to be picked up and (correctly) dropped.
	time.Sleep(50 * time.Millisecond)
	close(release)
	pool.Close()

	if got := bp.calls.Load(); got != 1 {
		t.Fatalf("expected 1 Embed call after dedup, got %d", got)
	}
}

// TestPool_SkipsAlreadyEmbeddedCells confirms the moat skip-check
// applies on the async path: a job whose Cell already has matching
// text_md5 + vector_key in the primary is not re-embedded.
func TestPool_SkipsAlreadyEmbeddedCells(t *testing.T) {
	h := newHarness(t)
	inner, _ := embedding.Open("noop:default")
	rec := &recordingProvider{inner: inner}

	ws := &types.Workspace{Name: "skip"}
	_ = h.metadata.CreateWorkspace(h.ctx, ws)
	mem := h.imprint(ws.ID, "s", "agent_opaque_s")
	cells := h.seedCells(mem.ID, "agent_opaque_s", []string{"s0", "s1"})

	// Manually mark cells as already embedded so the skip-check fires.
	for i := range cells {
		cells[i].VectorKey = cells[i].CellID
		cells[i].EmbeddingModel = "noop:default"
	}
	if err := h.metadata.UpsertCells(h.ctx, mem.ID, cells); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	pool := queue.NewPool(queue.EmbedderDeps{Metadata: h.metadata, Vector: h.vector, Provider: rec},
		queue.PoolConfig{Workers: 1, BatchSize: 4, BatchFlushInterval: 10 * time.Millisecond})
	for _, c := range cells {
		pool.Submit(queue.Job{WorkspaceID: ws.ID, MemoryID: mem.ID, Cell: c})
	}
	pool.Close()

	if got := len(rec.callShapes()); got != 0 {
		t.Fatalf("expected 0 Embed calls (all skipped), got %d (shapes=%v)", got, rec.callShapes())
	}
	// Both cells already had vector_keys → memory should flip to ready.
	got, _ := h.metadata.GetMemory(h.ctx, mem.ID)
	if !got.RecallReady {
		t.Fatal("expected recall_ready=true after skip-only run with all cells already embedded")
	}
}

// Silence "imported and not used" if api is only referenced indirectly.
var _ = api.LedgerEntry{}
