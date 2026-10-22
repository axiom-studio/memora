package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/internal/store/pgvector"
	"github.com/axiom-studio/memora/pkg/adapter"
)

// warnStaleVectors logs, at start-up, vector rows that search skips
// because another embedding model wrote them.
func warnStaleVectors(ctx context.Context, svc *service.Service, logger *slog.Logger) {
	mav, ok := svc.Vector.(adapter.ModelAwareVectorStore)
	if !ok {
		return
	}
	if n, err := mav.StaleVectors(ctx); err == nil && n > 0 {
		logger.Warn("vector rows from another embedding model or dimension are not searched; run `memora-core reembed`",
			"stale_vectors", n, "model", svc.Embedder.ModelID())
	}
}

// runReembed is `memora-core reembed`: give every cell a vector from the
// configured embedding model (S-7). It prints the counts as JSON.
func runReembed() {
	fs := flag.NewFlagSet("reembed", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config file")
	workspace := fs.String("workspace", "", "re-embed only this workspace (default: all)")
	dryRun := fs.Bool("dry-run", false, "count the cells to re-embed; write nothing")
	batch := fs.Int("batch", 100, "memories per batch")
	_ = fs.Parse(os.Args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(config.EX_CONFIG)
	}
	cfg.ApplyEnv()
	res, err := reembed(context.Background(), cfg, service.ReembedOptions{WorkspaceID: *workspace, DryRun: *dryRun, Batch: *batch}, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}

// reembed opens the adapters for re-embedding and runs it. A real run
// lets a vector store sized for another dimension rebuild itself; a dry
// run never does, and reports that the rebuild would re-embed every cell.
func reembed(ctx context.Context, cfg config.Config, o service.ReembedOptions, progress io.Writer) (service.ReembedResult, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, closeAll, err := openCoreAdaptersWith(ctx, cfg, logger, adapterOptions{RebuildVectors: !o.DryRun})
	if errors.Is(err, pgvector.ErrDimensionMismatch) {
		return service.ReembedResult{DryRun: true}, fmt.Errorf("%w (a run without --dry-run rebuilds the vector table and re-embeds every cell)", err)
	}
	if err != nil {
		return service.ReembedResult{}, err
	}
	defer closeAll()
	return svc.Reembed(ctx, o, func(r service.ReembedResult) {
		fmt.Fprintf(progress, "reembed: %d workspaces, %d memories, %d cells, %d stale, %d re-embedded, %d forgotten memories' vectors removed\n",
			r.Workspaces, r.Memories, r.Cells, r.Stale, r.Reembedded, r.Orphans)
	})
}
