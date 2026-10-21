package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/internal/store/pgvector"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// seedMemories writes n memories with cfg's adapters and returns their
// workspace.
func seedMemories(t *testing.T, cfg config.Config, n int) string {
	t.Helper()
	ctx := context.Background()
	svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	ws := &types.Workspace{Name: "reembed"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := svc.Imprint(ctx, ws.ID, types.NewID(), api.ImprintRequest{Content: "aurora borealis sighting"}); err != nil {
			t.Fatal(err)
		}
	}
	return ws.ID
}

func vectorHits(t *testing.T, cfg config.Config, ws string) int {
	t.Helper()
	svc, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	r, err := svc.Recall(context.Background(), ws, api.RecallRequest{Query: "aurora", Mode: api.RecallModeVector, K: 10})
	if err != nil {
		t.Fatal(err)
	}
	return len(r.Results)
}

// TestReembedCommand_SQLite: after a model change, `reembed --dry-run`
// counts without writing, `reembed` restores vector hits, and a
// workspace scope that does not exist is an error.
func TestReembedCommand_SQLite(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Embedding.Model = "hash:default"
	ws := seedMemories(t, cfg, 3)
	cfg.Embedding.Model = "hash:256"
	if n := vectorHits(t, cfg, ws); n != 0 {
		t.Fatalf("after the switch: %d hits, want 0", n)
	}
	ctx := context.Background()
	dry, err := reembed(ctx, cfg, service.ReembedOptions{DryRun: true}, io.Discard)
	if err != nil || dry.Stale != 3 || dry.Reembedded != 0 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	res, err := reembed(ctx, cfg, service.ReembedOptions{WorkspaceID: ws}, io.Discard)
	if err != nil || res.Reembedded != 3 || res.Model != "hash:256" || res.Dim != 256 {
		t.Fatalf("reembed = %+v, %v", res, err)
	}
	if n := vectorHits(t, cfg, ws); n != 3 {
		t.Fatalf("after reembed: %d hits, want 3", n)
	}
	if _, err := reembed(ctx, cfg, service.ReembedOptions{WorkspaceID: types.NewID()}, io.Discard); err == nil {
		t.Fatal("an unknown workspace was accepted")
	}
}

// TestReembedCommand_PgvectorDimensionChange (S-7): a pgvector column
// sized for the old model refuses start-up with a message pointing at
// reembed; a dry run does not touch it; reembed rebuilds it and
// re-embeds every cell, after which the node starts and searches.
func TestReembedCommand_PgvectorDimensionChange(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.VectorDriver = "pgvector"
	cfg.Storage.VectorDSN = freshPostgresDB(t)
	cfg.Embedding.Model = "hash:64"
	ws := seedMemories(t, cfg, 2)

	cfg.Embedding.Model = "hash:128"
	_, _, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if !errors.Is(err, pgvector.ErrDimensionMismatch) || !strings.Contains(err.Error(), "memora-core reembed") ||
		!strings.Contains(err.Error(), "vector(64)") || !strings.Contains(err.Error(), "128") {
		t.Fatalf("start-up with a wider model: err = %v", err)
	}
	ctx := context.Background()
	if _, err := reembed(ctx, cfg, service.ReembedOptions{DryRun: true}, io.Discard); !errors.Is(err, pgvector.ErrDimensionMismatch) {
		t.Fatalf("dry run: err = %v, want the mismatch reported", err)
	}
	cfg.Embedding.Model = "hash:64"
	if n := vectorHits(t, cfg, ws); n != 2 {
		t.Fatalf("the dry run changed the old vectors: %d hits", n)
	}

	cfg.Embedding.Model = "hash:128"
	res, err := reembed(ctx, cfg, service.ReembedOptions{}, io.Discard)
	if err != nil || res.Reembedded != 2 || res.Dim != 128 {
		t.Fatalf("reembed = %+v, %v", res, err)
	}
	if n := vectorHits(t, cfg, ws); n != 2 {
		t.Fatalf("after reembed: %d hits, want 2", n)
	}
}
