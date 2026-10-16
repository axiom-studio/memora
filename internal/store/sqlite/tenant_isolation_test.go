package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Issue #4373: every primitive that addresses a row by ID must also scope it
// to the caller's workspace and report a foreign row exactly like a missing
// one (ErrNotFound), so IDs from another tenant are neither readable,
// writable, nor confirmable.

type isoFixture struct {
	s         *Store
	ctx       context.Context
	owner     string // workspace that owns the memory
	other     string // attacker's workspace
	mem       *types.Memory
	watermark string
}

func newIsoFixture(t *testing.T) isoFixture {
	t.Helper()
	s, ctx := openStore(t)
	w1, w2 := &types.Workspace{Name: "owner"}, &types.Workspace{Name: "other"}
	if err := s.CreateWorkspace(ctx, w1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(ctx, w2); err != nil {
		t.Fatal(err)
	}
	m := &types.Memory{WorkspaceID: w1.ID, Content: "owner secret", WrittenByAgentID: "agent_opaque_iso"}
	wmk, err := s.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCells(ctx, m.ID, []types.Cell{{Seq: 0, Text: "owner secret", TextMD5: types.MD5Hex("owner secret"), WrittenByAgentID: "agent_opaque_iso"}}); err != nil {
		t.Fatal(err)
	}
	return isoFixture{s: s, ctx: ctx, owner: w1.ID, other: w2.ID, mem: m, watermark: wmk}
}

func (f isoFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	got, err := f.s.GetMemory(f.ctx, f.mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "owner secret" || got.HeadWatermark != f.watermark {
		t.Fatalf("owner's memory changed by a cross-workspace call: content=%q watermark=%s", got.Content, got.HeadWatermark)
	}
}

func TestTenantIsolation_UpdateMemory(t *testing.T) {
	f := newIsoFixture(t)
	upd := &types.Memory{Content: "overwritten", LastModifiedByAgentID: "agent_opaque_attacker"}

	// Correct watermark, wrong workspace: must not write.
	if _, err := f.s.UpdateMemory(f.ctx, f.other, f.mem.ID, f.watermark, upd); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace update err = %v, want ErrNotFound", err)
	}
	// Stale watermark, wrong workspace: must not become a CAS oracle that
	// confirms the memory exists and leaks its head watermark.
	wmk, err := f.s.UpdateMemory(f.ctx, f.other, f.mem.ID, "wmk_stale", upd)
	if !errors.Is(err, types.ErrNotFound) || wmk != "" {
		t.Fatalf("cross-workspace stale update = (%q, %v), want (\"\", ErrNotFound)", wmk, err)
	}
	f.assertUnchanged(t)

	if _, err := f.s.UpdateMemory(f.ctx, f.owner, f.mem.ID, f.watermark, upd); err != nil {
		t.Fatalf("same-workspace update: %v", err)
	}
}

func TestTenantIsolation_AppendMemory(t *testing.T) {
	f := newIsoFixture(t)
	if _, _, err := f.s.AppendMemory(f.ctx, f.other, f.mem.ID, f.watermark, " injected", "agent_opaque_attacker"); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace append err = %v, want ErrNotFound", err)
	}
	f.assertUnchanged(t)
	if _, _, err := f.s.AppendMemory(f.ctx, f.owner, f.mem.ID, f.watermark, " more", "agent_opaque_iso"); err != nil {
		t.Fatalf("same-workspace append: %v", err)
	}
}

func TestTenantIsolation_PatchMemory(t *testing.T) {
	f := newIsoFixture(t)
	ops := []api.PatchOp{{OldString: "secret", NewString: "public"}}
	if _, _, _, err := f.s.PatchMemory(f.ctx, f.other, f.mem.ID, f.watermark, ops, "agent_opaque_attacker"); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace patch err = %v, want ErrNotFound", err)
	}
	f.assertUnchanged(t)
	if _, _, _, err := f.s.PatchMemory(f.ctx, f.owner, f.mem.ID, f.watermark, ops, "agent_opaque_iso"); err != nil {
		t.Fatalf("same-workspace patch: %v", err)
	}
}

func TestTenantIsolation_GetCells(t *testing.T) {
	f := newIsoFixture(t)
	cells, err := f.s.GetCells(f.ctx, f.other, f.mem.ID)
	if err != nil || len(cells) != 0 {
		t.Fatalf("cross-workspace GetCells = (%d cells, %v), want none", len(cells), err)
	}
	if cells, _ := f.s.GetCells(f.ctx, f.owner, f.mem.ID); len(cells) != 1 || cells[0].Text != "owner secret" {
		t.Fatalf("same-workspace GetCells = %+v, want the owner's cell", cells)
	}
}

func TestTenantIsolation_DeletePin(t *testing.T) {
	f := newIsoFixture(t)
	p := &types.Pin{WorkspaceID: f.owner, Query: "q", Mode: "hybrid", K: 10, Watermark: f.watermark}
	if err := f.s.CreatePin(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeletePin(f.ctx, f.other, p.PinID); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace DeletePin err = %v, want ErrNotFound", err)
	}
	if pins, _ := f.s.ListPins(f.ctx, f.owner); len(pins) != 1 {
		t.Fatalf("owner pins after cross-workspace delete = %d, want 1", len(pins))
	}
	if err := f.s.DeletePin(f.ctx, f.owner, p.PinID); err != nil {
		t.Fatalf("same-workspace DeletePin: %v", err)
	}
}

func TestTenantIsolation_GraphStoreUnlinkAndCascadeForget(t *testing.T) {
	f := newIsoFixture(t)
	target := &types.Memory{WorkspaceID: f.owner, Content: "target", WrittenByAgentID: "agent_opaque_iso"}
	if _, err := f.s.ImprintMemory(f.ctx, target); err != nil {
		t.Fatal(err)
	}
	gs := &GraphStore{db: f.s.db}
	e, err := gs.Link(f.ctx, types.Edge{WorkspaceID: f.owner, SourceMemoryID: f.mem.ID, TargetMemoryID: target.ID, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: "agent_opaque_iso"})
	if err != nil {
		t.Fatal(err)
	}

	if err := gs.Unlink(f.ctx, f.other, e.EdgeID, "agent_opaque_attacker"); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace Unlink err = %v, want ErrNotFound", err)
	}
	if n, err := gs.CascadeForget(f.ctx, f.other, f.mem.ID, "agent_opaque_attacker"); err != nil || n != 0 {
		t.Fatalf("cross-workspace CascadeForget = (%d, %v), want (0, nil)", n, err)
	}
	edges, _, err := gs.Neighbors(f.ctx, f.owner, f.mem.ID, adapter.NeighborsOpts{Direction: api.GraphDirOut})
	if err != nil || len(edges) != 1 {
		t.Fatalf("owner's edges after cross-workspace calls = (%d, %v), want 1 surviving edge", len(edges), err)
	}

	if err := gs.Unlink(f.ctx, f.owner, e.EdgeID, "agent_opaque_iso"); err != nil {
		t.Fatalf("same-workspace Unlink: %v", err)
	}
}
