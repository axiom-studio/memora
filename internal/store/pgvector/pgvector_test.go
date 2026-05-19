package pgvector

import (
	"context"
	"os"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func skipWithoutPostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MEMORA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MEMORA_POSTGRES_DSN not set; skipping pgvector integration test")
	}
	return dsn
}

func TestStore_Capabilities(t *testing.T) {
	s := &Store{}
	caps := s.Capabilities()
	if !caps.SupportsExactSearch || !caps.SupportsANN || !caps.SupportsHybridFilter {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.MaxDimensions != 16000 {
		t.Fatalf("expected max 16000, got %d", caps.MaxDimensions)
	}
}

func TestStore_PutQueryDelete(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()

	s := &Store{}
	if err := s.Open(ctx, adapter.VectorConfig{DSN: dsn, Dim: 3}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ws := "ws_pgvec_test"
	puts := []adapter.VectorPut{
		{Key: adapter.VectorKey{WorkspaceID: ws, MemoryID: "m1", CellID: "c1"}, Embedding: []float32{1, 0, 0}},
		{Key: adapter.VectorKey{WorkspaceID: ws, MemoryID: "m1", CellID: "c2"}, Embedding: []float32{0, 1, 0}},
		{Key: adapter.VectorKey{WorkspaceID: ws, MemoryID: "m2", CellID: "c3"}, Embedding: []float32{0, 0, 1}},
	}
	if err := s.PutVectorsBatch(ctx, puts); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Query(ctx, adapter.VectorQuery{
		WorkspaceID: ws,
		Embedding:   []float32{1, 0, 0},
		K:           2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Key.CellID != "c1" {
		t.Fatalf("expected c1 as top hit, got %s", hits[0].Key.CellID)
	}

	if err := s.DeleteVectors(ctx, []adapter.VectorKey{{CellID: "c1"}, {CellID: "c2"}, {CellID: "c3"}}); err != nil {
		t.Fatal(err)
	}

	hits, err = s.Query(ctx, adapter.VectorQuery{WorkspaceID: ws, Embedding: []float32{1, 0, 0}, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits after delete, got %d", len(hits))
	}
}

func TestFloatsToVectorLiteral(t *testing.T) {
	got := floatsToVectorLiteral([]float32{1.5, -2.3, 0})
	expected := "[1.5,-2.3,0]"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
