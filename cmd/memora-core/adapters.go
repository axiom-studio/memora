package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
)

// openCoreAdapters opens every storage seam, the embedding provider and the
// identity providers, and returns the Service that both `serve` and `mcp`
// run on. Keeping one constructor stops the two transports drifting apart
// (Issue #4376: mcp once silently omitted Graph and Content).
//
// The returned close func releases every opened store; on error, anything
// already opened is closed before returning.
func openCoreAdapters(ctx context.Context, cfg config.Config, logger *slog.Logger) (svc *service.Service, closeAll func(), err error) {
	var closers []func() error
	closeOpened := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i]() // best-effort shutdown; nothing useful to do on failure
		}
	}
	defer func() {
		if err != nil {
			closeOpened()
		}
	}()

	dbPath := filepath.Join(cfg.Storage.DataDir, "memora.db")
	dsnOr := func(explicit string) string {
		if explicit != "" {
			return explicit
		}
		return dbPath
	}

	meta, err := adapter.OpenMetadata(ctx, adapter.MetadataConfig{Driver: cfg.Storage.MetadataDriver, DSN: dsnOr(cfg.Storage.MetadataDSN)})
	if err != nil {
		return nil, nil, fmt.Errorf("open metadata (driver %q): %w", cfg.Storage.MetadataDriver, err)
	}
	closers = append(closers, meta.Close)

	led, err := adapter.OpenLedger(ctx, adapter.LedgerConfig{Driver: cfg.Storage.LedgerDriver, DSN: dsnOr(cfg.Storage.LedgerDSN)})
	if err != nil {
		return nil, nil, fmt.Errorf("open ledger (driver %q): %w", cfg.Storage.LedgerDriver, err)
	}
	closers = append(closers, led.Close)

	embedProvider, err := embedding.Open(cfg.Embedding.Model)
	if err != nil {
		return nil, nil, fmt.Errorf("open embedding (model %q): %w", cfg.Embedding.Model, err)
	}
	logger.Info("embedding provider ready", "model", embedProvider.ModelID(), "dim", embedProvider.Dim())

	// The vector store sizes its column from Dim, so the embedder opens
	// first and dictates it (Issue #4375).
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{Driver: cfg.Storage.VectorDriver, DSN: dsnOr(cfg.Storage.VectorDSN), Dim: embedProvider.Dim()})
	if err != nil {
		return nil, nil, fmt.Errorf("open vector (driver %q, dim %d): %w", cfg.Storage.VectorDriver, embedProvider.Dim(), err)
	}
	closers = append(closers, vec.Close)

	identityMap := map[string]adapter.IdentityProvider{}
	for _, name := range []string{
		string(types.IdentityProviderOpaque),
		string(types.IdentityProviderAnthropicSession),
	} {
		if p, err := adapter.OpenIdentity(name); err == nil {
			identityMap[name] = p
		}
	}

	svc = &service.Service{
		Metadata: meta,
		Vector:   vec,
		Ledger:   led,
		Embedder: embedProvider,
		Identity: identityMap,
	}

	if cfg.Storage.ContentDriver != "" {
		cdsn := cfg.Storage.ContentDSN
		if cdsn == "" {
			if cfg.Storage.ContentDriver == "sqlite" {
				cdsn = dbPath
			} else {
				cdsn = filepath.Join(cfg.Storage.DataDir, "content")
			}
		}
		cs, err := adapter.OpenContent(ctx, adapter.ContentConfig{Driver: cfg.Storage.ContentDriver, DSN: cdsn})
		if err != nil {
			return nil, nil, fmt.Errorf("open content (driver %q): %w", cfg.Storage.ContentDriver, err)
		}
		closers = append(closers, cs.Close)
		svc.Content = cs
		logger.Info("content store enabled", "driver", cfg.Storage.ContentDriver, "dsn", cdsn)
	}

	var gcaps *adapter.GraphCapabilities
	if cfg.Storage.GraphDriver != "" {
		// sqlite_graph reuses the metadata database's edge tables, so an
		// unset graph_dsn follows the metadata DSN rather than data_dir.
		gdsn := cfg.Storage.GraphDSN
		if gdsn == "" {
			gdsn = dsnOr(cfg.Storage.MetadataDSN)
		}
		gs, err := adapter.OpenGraph(ctx, adapter.GraphConfig{Driver: cfg.Storage.GraphDriver, DSN: gdsn})
		if err != nil {
			return nil, nil, fmt.Errorf("open graph (driver %q): %w", cfg.Storage.GraphDriver, err)
		}
		closers = append(closers, gs.Close)
		svc.Graph = gs
		c := gs.Capabilities()
		gcaps = &c
		logger.Info("graph store enabled", "driver", cfg.Storage.GraphDriver)
	} else {
		logger.Warn("graph store disabled (storage.graph_driver is empty); graph tools and auto-link are unavailable")
	}

	var ccaps *adapter.ContentCapabilities
	if svc.Content != nil {
		c := svc.Content.Capabilities()
		ccaps = &c
	}
	if err := adapter.ValidateCompatibility(logger, meta.Capabilities(), ccaps, gcaps); err != nil {
		return nil, nil, fmt.Errorf("compatibility check: %w", err)
	}
	return svc, closeOpened, nil
}
