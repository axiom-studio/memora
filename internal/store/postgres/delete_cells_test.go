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
