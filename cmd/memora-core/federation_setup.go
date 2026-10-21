package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/internal/service"
)

// setupFederation builds the peer registry for svc and sets
// svc.Federation, for serve and the stdio MCP server alike. Config
// peers are validated (TLS files included) and seed the metadata store
// once; the store is the source of truth from then on (F-7). It returns
// nils when federation is off.
func setupFederation(ctx context.Context, cfg config.Config, svc *service.Service, logger *slog.Logger) (*federation.Registry, *federation.Manager, error) {
	reg, err := federation.NewRegistry(cfg.Federation, svc.NodeID)
	if err != nil || reg == nil {
		return nil, nil, err
	}
	keyFile := cfg.Federation.SecretKeyFile
	if keyFile == "" {
		keyFile = filepath.Join(cfg.Storage.DataDir, "federation.key")
	}
	sealer, err := federation.LoadSealer(keyFile)
	if err != nil {
		return nil, nil, err
	}
	peers := federation.NewManager(reg, svc.Metadata, sealer)
	peers.AllowClientCertPins = cfg.Server.TLS.Enabled
	n, err := peers.Seed(ctx, cfg.Federation.Peers)
	if err != nil {
		return nil, nil, fmt.Errorf("seed peers: %w", err)
	}
	if err := peers.Reload(ctx); err != nil {
		return nil, nil, fmt.Errorf("load peers: %w", err)
	}
	if reg.RequiresClientCerts() && !cfg.Server.TLS.Enabled {
		return nil, nil, errors.New("a peer sets client_cert_sha256 but server.tls is not enabled")
	}
	if svc.Federation, err = federation.NewSearcher(reg); err != nil {
		return nil, nil, err
	}
	logger.Info("federation peers loaded", "peers", len(reg.Peers()), "seeded_from_config", n)
	return reg, peers, nil
}
