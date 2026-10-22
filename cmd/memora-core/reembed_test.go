package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

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

// TestPgvectorFilteredVectorSearch_HNSW (#4359 QA): on pgvector at a
// size where the planner uses the HNSW index, a filtered vector search
// still returns k matches. 3,000 strong decoys fail the filters; 30 weak
// targets pass. An HNSW scan used to return a short page (ef_search 40),
// which the refill took for exhaustion: 0 of 10.
func TestPgvectorFilteredVectorSearch_HNSW(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.VectorDriver = "pgvector"
	cfg.Storage.VectorDSN = freshPostgresDB(t)
	cfg.Embedding.Model = "hash:64"
	ctx := context.Background()
	svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	ws := &types.Workspace{Name: "hnsw"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	decoyAgent, targetAgent := types.NewID(), types.NewID()
	off := false
	write := func(agent, content, team string) {
		if _, err := svc.Imprint(ctx, ws.ID, agent, api.ImprintRequest{Content: content, Tags: map[string]string{"team": team}, AutoLink: &off}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3000; i++ {
		write(decoyAgent, fmt.Sprintf("alpha bravo charlie delta decoy %d", i), "ops")
	}
	for i := 0; i < 30; i++ {
		write(targetAgent, fmt.Sprintf("alpha zulu yankee xray whiskey target %d", i), "qa")
	}

	// The query shape must actually use the HNSW index at this size.
	vec, err := svc.Embedder.Embed(ctx, []string{"alpha bravo"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, cfg.Storage.VectorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// As autovacuum would, so the planner sees the real row counts.
	if _, err := conn.Exec(ctx, "ANALYZE memora_cell_vectors"); err != nil {
		t.Fatal(err)
	}
	lit := strings.ReplaceAll(fmt.Sprint(vec[0]), " ", ",")
	var plan strings.Builder
	rows, err := conn.Query(ctx, `EXPLAIN SELECT cell_id FROM memora_cell_vectors WHERE model = $1 ORDER BY embedding <=> $2::vector LIMIT 40`, "hash:64", lit)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain: %v", err)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "idx_cellvec_hnsw") {
		t.Fatalf("the planner does not use the HNSW index, so this test proves nothing:\n%s", plan.String())
	}

	for name, f := range map[string]api.RecallFilters{
		"tag":          {Tags: map[string][]string{"team": {"qa"}}},
		"agent":        {AgentID: targetAgent},
		"agent not in": {AgentIDNotIn: []string{decoyAgent}},
	} {
		for _, mode := range []api.RecallMode{api.RecallModeVector, api.RecallModeHybrid} {
			r, err := svc.Search(ctx, api.SearchRequest{Query: "alpha bravo", Mode: mode, K: 10, Filters: f})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Results) != 10 {
				t.Fatalf("%s/%s: %d hits, want 10", name, mode, len(r.Results))
			}
			for _, h := range r.Results {
				if h.WrittenByAgentID != targetAgent {
					t.Fatalf("%s/%s: hit %s by %s fails the filter", name, mode, h.MemoryID, h.WrittenByAgentID)
				}
			}
		}
	}
}

// TestReembedCommand_PgvectorPartialRebuild (#6180): with a pgvector
// dimension change, `reembed --workspace` refuses instead of dropping
// every workspace's vectors; and a rebuild that re-embedded only part of
// the node is visible as missing vectors, at start-up and in /readyz's
// counts, until a full reembed.
func TestReembedCommand_PgvectorPartialRebuild(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.VectorDriver = "pgvector"
	cfg.Storage.VectorDSN = freshPostgresDB(t)
	cfg.Embedding.Model = "hash:64"
	wsA := seedMemories(t, cfg, 3)
	wsB := seedMemories(t, cfg, 3)
	ctx := context.Background()

	cfg.Embedding.Model = "hash:128"
	_, err := reembed(ctx, cfg, service.ReembedOptions{WorkspaceID: wsA}, io.Discard)
	if !errors.Is(err, pgvector.ErrDimensionMismatch) || !strings.Contains(err.Error(), "without --workspace") {
		t.Fatalf("reembed --workspace with a dimension change: err = %v, want a refusal", err)
	}
	cfg.Embedding.Model = "hash:64"
	if a, b := vectorHits(t, cfg, wsA), vectorHits(t, cfg, wsB); a != 3 || b != 3 {
		t.Fatalf("the refused run touched the vectors: %d and %d hits, want 3 and 3", a, b)
	}

	// An interrupted rebuild: the table is rebuilt at the new width and
	// only workspace A is re-embedded before the run stops.
	cfg.Embedding.Model = "hash:128"
	svc, closeAll, err := openCoreAdaptersWith(ctx, cfg, discardLogger(), adapterOptions{RebuildVectors: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reembed(ctx, service.ReembedOptions{WorkspaceID: wsA}, nil); err != nil {
		t.Fatal(err)
	}
	closeAll()

	health := func() service.VectorHealth {
		t.Helper()
		svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
		if err != nil {
			t.Fatal(err)
		}
		defer closeAll()
		h, ok, err := svc.VectorHealth(ctx)
		if err != nil || !ok {
			t.Fatalf("VectorHealth: ok=%v, %v", ok, err)
		}
		return h
	}
	if h := health(); h.Missing != 3 || h.Stale != 0 {
		t.Fatalf("after a partial rebuild: %+v, want 3 missing (workspace B), 0 stale", h)
	}
	if res, err := reembed(ctx, cfg, service.ReembedOptions{}, io.Discard); err != nil || res.Reembedded != 3 {
		t.Fatalf("full reembed = %+v, %v; want B's 3 cells re-embedded", res, err)
	}
	if h := health(); h.Missing != 0 || h.Stale != 0 {
		t.Fatalf("after a full reembed: %+v, want 0 and 0", h)
	}
	if b := vectorHits(t, cfg, wsB); b != 3 {
		t.Fatalf("workspace B after the full reembed: %d hits, want 3", b)
	}
}
