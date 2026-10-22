package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Issue #4373: Postgres mirror of the sqlite tenant-isolation tests.
func TestTenantIsolation_MetadataStoreScopesByWorkspace(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()
	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	owner := &types.Workspace{Name: "iso-owner-" + types.NewID()}
	other := &types.Workspace{Name: "iso-other-" + types.NewID()}
	for _, w := range []*types.Workspace{owner, other} {
		if err := ms.CreateWorkspace(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	m := &types.Memory{WorkspaceID: owner.ID, Content: "owner secret", WrittenByAgentID: types.AgentLegacyVibeflowID}
	wmk, err := ms.ImprintMemory(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.UpsertCells(ctx, m.ID, []types.Cell{{Seq: 0, Text: "owner secret", TextMD5: types.MD5Hex("owner secret"), WrittenByAgentID: types.AgentLegacyVibeflowID}}); err != nil {
		t.Fatal(err)
	}
	p := &types.Pin{WorkspaceID: owner.ID, Query: "q", Mode: "hybrid", K: 10, Watermark: wmk}
	if err := ms.CreatePin(ctx, p); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ms.DeletePin(ctx, owner.ID, p.PinID)
		_ = ms.ForgetMemory(ctx, owner.ID, m.ID)
		_ = ms.DeleteWorkspace(ctx, owner.ID)
		_ = ms.DeleteWorkspace(ctx, other.ID)
	}()

	upd := &types.Memory{Content: "pwned", LastModifiedByAgentID: types.AgentLegacyVibeflowID}
	if _, err := ms.UpdateMemory(ctx, other.ID, m.ID, wmk, upd); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("cross-workspace UpdateMemory err = %v, want ErrNotFound", err)
	}
	if got, err := ms.UpdateMemory(ctx, other.ID, m.ID, "wmk_stale", upd); !errors.Is(err, types.ErrNotFound) || got != "" {
		t.Errorf("cross-workspace stale UpdateMemory = (%q, %v), want (\"\", ErrNotFound): CAS oracle", got, err)
	}
	if _, _, err := ms.AppendMemory(ctx, other.ID, m.ID, wmk, " pwned", types.AgentLegacyVibeflowID); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("cross-workspace AppendMemory err = %v, want ErrNotFound", err)
	}
	if _, _, _, err := ms.PatchMemory(ctx, other.ID, m.ID, wmk, []api.PatchOp{{OldString: "secret", NewString: "public"}}, types.AgentLegacyVibeflowID); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("cross-workspace PatchMemory err = %v, want ErrNotFound", err)
	}
	if cells, err := ms.GetCells(ctx, other.ID, m.ID); err != nil || len(cells) != 0 {
		t.Errorf("cross-workspace GetCells = (%d, %v), want none", len(cells), err)
	}
	if err := ms.DeletePin(ctx, other.ID, p.PinID); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("cross-workspace DeletePin err = %v, want ErrNotFound", err)
	}

	got, err := ms.GetMemory(ctx, m.ID)
	if err != nil || got.Content != "owner secret" || got.HeadWatermark != wmk {
		t.Fatalf("owner memory after cross-workspace calls = %+v (%v), want unchanged", got, err)
	}
	if cells, _ := ms.GetCells(ctx, owner.ID, m.ID); len(cells) != 1 {
		t.Fatalf("same-workspace GetCells = %d cells, want 1", len(cells))
	}
	if pins, _ := ms.ListPins(ctx, owner.ID); len(pins) != 1 {
		t.Fatalf("owner pins = %d, want 1", len(pins))
	}
}
