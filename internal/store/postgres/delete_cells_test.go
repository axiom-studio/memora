package postgres

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/adapter/compliance"
	"github.com/axiom-studio/memora/pkg/types"
)

func TestDeleteCellsFrom_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.DeleteCellsFromContract(t, ms)
}

func TestResolveIdentityKey_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.ResolveIdentityKeyContract(t, ms)
}

// TestEnsureNodeID_Postgres runs on a shared database, so the stored ID
// may come from an earlier run; the contract only needs it stable.
func TestEnsureNodeID_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.EnsureNodeIDContract(t, ms)
}

func TestKeywordSearch_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.KeywordSearchContract(t, ms)
}

func TestBatchLoad_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.BatchLoadContract(t, ms)
}

func TestAgentsPerWorkspace_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ms := &MetadataStore{}
	if err := ms.Open(context.Background(), adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	compliance.AgentsPerWorkspaceContract(t, ms)
}

// TestSeedReservedAgents_BackfillsExistingWorkspaces: a workspace whose
// sentinel rows are missing (as after the node-wide-PK era) gets them
// back on the next Open.
func TestSeedReservedAgents_BackfillsExistingWorkspaces(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()
	ms := &MetadataStore{}
	if err := ms.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	ws := &types.Workspace{Name: "backfill-" + types.NewID()}
	if err := ms.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.pool.Exec(ctx, `DELETE FROM memora_agents WHERE workspace_id = $1`, ws.ID); err != nil {
		t.Fatal(err)
	}
	ms.Close()

	reopened := &MetadataStore{}
	if err := reopened.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range types.ReservedAgentIDs {
		if _, err := reopened.GetAgent(ctx, ws.ID, id); err != nil {
			t.Errorf("sentinel %s not back-filled: %v", id, err)
		}
	}
}
