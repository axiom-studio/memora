package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	embedqueue "github.com/axiom-studio/memora/internal/embed/queue"
	ledgersqlite "github.com/axiom-studio/memora/internal/ledger/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// newAsyncTestService is newTestService with the embed pool wired the way
// openCoreAdapters wires it for `embedding.async = true` (todo #4229).
func newAsyncTestService(t *testing.T) *Service {
	t.Helper()
	s := newTestService(t)
	// Auto-link ledgers every edge it makes; production always has a ledger.
	led := &ledgersqlite.Store{}
	if err := led.Open(context.Background(), adapter.LedgerConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "ledger.db")}); err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = led.Close() })
	s.Ledger = led
	s.Pool = embedqueue.NewPool(embedqueue.EmbedderDeps{
		Metadata: s.Metadata, Vector: s.Vector, Provider: s.Embedder, Ledger: led,
	}, embedqueue.PoolConfig{Workers: 2, BatchFlushInterval: 5 * time.Millisecond, PostEmbedHook: s.PostEmbedAutoLinkHook()})
	// Registered after newTestService's store cleanups, so it runs first.
	t.Cleanup(s.Pool.Close)
	return s
}

// waitEmbedded polls until every stored cell of memoryID has a vector and
// the memory is recall_ready.
func waitEmbedded(t *testing.T, s *Service, ws, memoryID string) []types.Cell {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		cells, err := s.Metadata.GetCells(context.Background(), ws, memoryID)
		if err != nil {
			t.Fatalf("GetCells: %v", err)
		}
		done := len(cells) > 0 && storedRecallReady(t, s, memoryID)
		for _, c := range cells {
			if c.VectorKey == "" {
				done = false
			}
		}
		if done {
			return cells
		}
		if time.Now().After(deadline) {
			t.Fatalf("memory %s not fully embedded after 5s: %+v", memoryID, cells)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// asyncDoc is three cells under the markdown chunker, one per heading.
const asyncDoc = "# alpha\nalpha body\n# beta\nbeta body\n# gamma\ngamma body"

func newMarkdownWorkspace(t *testing.T, s *Service) string {
	t.Helper()
	ws := &types.Workspace{Name: "async-embed", ChunkerID: "markdown"}
	if err := s.Metadata.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return ws.ID
}

func TestAsyncEmbed_ImprintReturnsNotReadyThenFlips(t *testing.T) {
	s := newAsyncTestService(t)
	ws := newMarkdownWorkspace(t, s)
	ctx := context.Background()

	resp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: asyncDoc})
	if err != nil {
		t.Fatalf("Imprint: %v", err)
	}
	if resp.RecallReady {
		t.Fatal("ImprintResponse.RecallReady = true with async embed; want false until the pool drains")
	}
	cells := waitEmbedded(t, s, ws, resp.ID)
	if len(cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(cells))
	}

	// The vectors the pool wrote are what vector recall finds.
	rr, err := s.Recall(ctx, ws, api.RecallRequest{Query: "# beta\nbeta body", Mode: "vector", K: 1})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(rr.Results) != 1 || rr.Results[0].MemoryID != resp.ID {
		t.Fatalf("vector recall hits = %+v, want the imprinted memory", rr.Results)
	}
}

// The moat counts must not depend on the mode: the same Patch reports the
// same re-embed and skip counts inline and async.
func TestAsyncEmbed_PatchMoatCountsMatchSync(t *testing.T) {
	ctx := context.Background()
	patch := func(s *Service, async bool) *api.PatchResponse {
		ws := newMarkdownWorkspace(t, s)
		imp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: asyncDoc})
		if err != nil {
			t.Fatalf("Imprint: %v", err)
		}
		if async {
			waitEmbedded(t, s, ws, imp.ID)
		}
		resp, err := s.Patch(ctx, ws, imp.ID, testAgent, imp.Watermark, api.PatchRequest{
			Patch: []api.PatchOp{{OldString: "beta body", NewString: "BETA body"}},
		})
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		if async {
			cells := waitEmbedded(t, s, ws, imp.ID)
			// The re-embedded cell's vector is recorded against the stored
			// cell_id, so the pool found the row it embedded.
			for _, c := range cells {
				if c.VectorKey != c.CellID {
					t.Fatalf("cell %s vector_key = %q, want its own cell_id", c.CellID, c.VectorKey)
				}
			}
			rr, err := s.Recall(ctx, ws, api.RecallRequest{Query: "# beta\nBETA body", Mode: "vector", K: 1})
			if err != nil {
				t.Fatalf("Recall: %v", err)
			}
			// Recall hits carry the memory's content, so check the score: only
			// a vector for exactly the patched cell text scores 1.
			if len(rr.Results) != 1 || rr.Results[0].VectorScore < 0.9999 {
				t.Fatalf("vector recall after async patch = %+v, want a vector for the patched cell", rr.Results)
			}
		}
		return resp
	}

	syncResp := patch(newTestService(t), false)
	asyncResp := patch(newAsyncTestService(t), true)
	if syncResp.CellsReembed != 1 || syncResp.CellsSkipped != 2 {
		t.Fatalf("sync patch re-embed/skip = %d/%d, want 1/2", syncResp.CellsReembed, syncResp.CellsSkipped)
	}
	if asyncResp.CellsReembed != syncResp.CellsReembed || asyncResp.CellsSkipped != syncResp.CellsSkipped {
		t.Fatalf("async patch re-embed/skip = %d/%d, sync = %d/%d", asyncResp.CellsReembed, asyncResp.CellsSkipped,
			syncResp.CellsReembed, syncResp.CellsSkipped)
	}
}

// auto_link=false on an imprint must hold when the link is made later by
// the pool's post-embed hook rather than inline.
func TestAsyncEmbed_AutoLinkOptOutHonoured(t *testing.T) {
	s := newAsyncTestService(t)
	ctx := context.Background()
	w := &types.Workspace{Name: "autolink", AutoLinkEnabled: true}
	if err := s.Metadata.CreateWorkspace(ctx, w); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ws := w.ID

	imprintWait := func(autoLink bool) string {
		resp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: "same text", AutoLink: &autoLink})
		if err != nil {
			t.Fatalf("Imprint: %v", err)
		}
		waitEmbedded(t, s, ws, resp.ID)
		return resp.ID
	}
	edgesFrom := func(memID string) int {
		edges, _, err := s.GraphNeighbors(ctx, ws, memID, adapter.NeighborsOpts{Direction: api.GraphDirOut})
		if err != nil {
			t.Fatalf("Neighbors: %v", err)
		}
		return len(edges)
	}

	imprintWait(true) // target for the later imprints
	optOut := imprintWait(false)
	control := imprintWait(true)
	// The hook runs after the recall_ready flip, so poll for the control
	// link instead of sleeping a fixed time (#6162). The opt-out memory
	// was embedded before the control was even imprinted, so once the
	// control has linked, the opt-out's hook has run too.
	deadline := time.Now().Add(5 * time.Second)
	for edgesFrom(control) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("auto_link=true imprint has no outgoing edges after 5s; the control case should link")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := edgesFrom(optOut); n != 0 {
		t.Fatalf("auto_link=false imprint has %d outgoing edges, want 0", n)
	}
}
