package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// openWithModel opens a service on dir's database with the given
// embedding model, as a node restarted with a new model would.
func openWithModel(t *testing.T, dir, model string) *Service {
	t.Helper()
	ctx := context.Background()
	emb, err := embedding.Open(model)
	if err != nil {
		t.Fatal(err)
	}
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{DSN: filepath.Join(dir, "memora.db"), Dim: emb.Dim(), Model: emb.ModelID()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	return &Service{Metadata: meta, Vector: vec, Embedder: emb}
}

// TestReembed_ModelSwitch (S-7): after switching the embedding model,
// old vectors are never compared with the new model's (zero vector hits,
// no error), /readyz-style staleness counts them, a dry run counts
// without writing, and reembed restores full hits; a second run is a
// no-op. Covered for a same-width switch and a width change.
func TestReembed_ModelSwitch(t *testing.T) {
	for name, models := range map[string][2]string{
		"same dimension":      {"hash:default", "hash:384"},
		"different dimension": {"hash:default", "hash:128"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			a := openWithModel(t, dir, models[0])
			ws := newTestWorkspace(t, a)
			var ids []string
			for _, c := range []string{"glacier melt survey", "glacier calving front", "glacier ice core"} {
				ids = append(ids, imprint(t, a, ws, c))
			}
			search := func(s *Service) int {
				t.Helper()
				r, err := s.Recall(ctx, ws, api.RecallRequest{Query: "glacier", Mode: api.RecallModeVector, K: 10})
				if err != nil {
					t.Fatalf("vector recall: %v", err)
				}
				return len(r.Results)
			}
			if n := search(a); n != 3 {
				t.Fatalf("model A: %d hits, want 3", n)
			}

			b := openWithModel(t, dir, models[1])
			if n := search(b); n != 0 {
				t.Fatalf("model B before reembed: %d hits, want 0", n)
			}
			if n, err := b.Vector.(adapter.ModelAwareVectorStore).StaleVectors(ctx); err != nil || n != 3 {
				t.Fatalf("stale vectors = %d, %v; want 3", n, err)
			}

			dry, err := b.Reembed(ctx, ReembedOptions{DryRun: true, Batch: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if dry.Stale != 3 || dry.Reembedded != 0 || dry.Memories != 3 || dry.Model != models[1] {
				t.Fatalf("dry run = %+v", dry)
			}
			if n := search(b); n != 0 {
				t.Fatalf("a dry run wrote vectors: %d hits", n)
			}

			batches := 0
			res, err := b.Reembed(ctx, ReembedOptions{Batch: 2}, func(ReembedResult) { batches++ })
			if err != nil {
				t.Fatal(err)
			}
			if res.Stale != 3 || res.Reembedded != 3 || batches != 2 {
				t.Fatalf("reembed = %+v over %d batches, want 3 re-embedded in 2", res, batches)
			}
			if n := search(b); n != 3 {
				t.Fatalf("model B after reembed: %d hits, want 3", n)
			}
			if n, _ := b.Vector.(adapter.ModelAwareVectorStore).StaleVectors(ctx); n != 0 {
				t.Fatalf("stale after reembed = %d", n)
			}
			for _, id := range ids {
				cells, _ := b.Metadata.GetCells(ctx, ws, id)
				for _, c := range cells {
					if c.EmbeddingModel != models[1] {
						t.Fatalf("cell %s records model %q, want %q", c.CellID, c.EmbeddingModel, models[1])
					}
				}
			}
			again, err := b.Reembed(ctx, ReembedOptions{}, nil)
			if err != nil || again.Stale != 0 || again.Reembedded != 0 {
				t.Fatalf("second run = %+v, %v; want a no-op", again, err)
			}
		})
	}
}

// TestReembed_AdoptsLegacyRows: vectors written before rows recorded a
// model are adopted by the model that opens the store at their width.
func TestReembed_AdoptsLegacyRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openWithModel(t, dir, "hash:default")
	ws := newTestWorkspace(t, s)
	imprint(t, s, ws, "legacy row text")
	// Simulate a pre-S-7 row: clear its model.
	raw, err := sql.Open("sqlite", filepath.Join(dir, "memora.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `UPDATE memora_cell_vectors SET model = ''`); err != nil {
		t.Fatal(err)
	}
	reopened := openWithModel(t, dir, "hash:default")
	if n, _ := reopened.Vector.(adapter.ModelAwareVectorStore).StaleVectors(ctx); n != 0 {
		t.Fatalf("legacy rows at the active width are stale: %d", n)
	}
	r, err := reopened.Recall(ctx, ws, api.RecallRequest{Query: "legacy", Mode: api.RecallModeVector})
	if err != nil || len(r.Results) != 1 {
		t.Fatalf("legacy row not searchable: %v %v", r, err)
	}
}
