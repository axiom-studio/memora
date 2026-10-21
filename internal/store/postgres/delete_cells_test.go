package postgres

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/adapter/compliance"
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
