package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/axiom-studio/memora/internal/config"
	embedqueue "github.com/axiom-studio/memora/internal/embed/queue"
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
	return openCoreAdaptersWith(ctx, cfg, logger, adapterOptions{})
}

// adapterOptions are open-time switches for maintenance commands.
type adapterOptions struct {
	// RebuildVectors lets a vector store sized for another dimension
	// discard its vectors and resize (memora-core reembed).
	RebuildVectors bool
}

func openCoreAdaptersWith(ctx context.Context, cfg config.Config, logger *slog.Logger, opts adapterOptions) (svc *service.Service, closeAll func(), err error) {
	// Commands also validate up front to exit with EX_CONFIG; checking here
	// too means no command can open adapters on an invalid config (#4378).
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("config validation failed: %w", err)
	}
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
	if err := refuseLegacyIDs(ctx, meta); err != nil {
		return nil, nil, err
	}
	nodeID, nodeName, err := resolveNode(ctx, cfg.Node, meta)
	if err != nil {
		return nil, nil, err
	}
	logger.Info("node identity", "node_id", nodeID, "node_name", nodeName)

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
	if !embedding.Searchable(embedProvider) {
		logger.Warn("embedding model produces no searchable vectors: vector search is unavailable and hybrid search uses keywords only; set embedding.model (e.g. hash:default offline)",
			"model", embedProvider.ModelID())
	}

	// The vector store sizes its column from Dim, so the embedder opens
	// first and dictates it (Issue #4375).
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{
		Driver: cfg.Storage.VectorDriver, DSN: dsnOr(cfg.Storage.VectorDSN),
		Dim: embedProvider.Dim(), Model: embedProvider.ModelID(), Rebuild: opts.RebuildVectors,
	})
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
		NodeID:   nodeID,
		NodeName: nodeName,
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
		logger.Info("content store enabled", "driver", cfg.Storage.ContentDriver, "dsn", redactDSN(cdsn))
	}

	var gcaps *adapter.GraphCapabilities
	if cfg.Storage.GraphEnabled() {
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
		logger.Warn("graph store disabled (storage.graph_driver is empty or \"none\"); graph tools and auto-link are unavailable")
	}

	var ccaps *adapter.ContentCapabilities
	if svc.Content != nil {
		c := svc.Content.Capabilities()
		ccaps = &c
	}
	if err := adapter.ValidateCompatibility(logger, meta.Capabilities(), ccaps, gcaps); err != nil {
		return nil, nil, fmt.Errorf("compatibility check: %w", err)
	}

	// Opened last so it closes first: Close drains queued cells while the
	// stores they are written to are still open.
	if cfg.Embedding.Async {
		pool := embedqueue.NewPool(embedqueue.EmbedderDeps{
			Metadata: meta, Vector: vec, Provider: embedProvider, Ledger: led,
		}, embedqueue.PoolConfig{PostEmbedHook: svc.PostEmbedAutoLinkHook()})
		closers = append(closers, func() error { pool.Close(); return nil })
		svc.Pool = pool
		logger.Info("async embed enabled; writes return before their cells are embedded")
	}
	return svc, closeOpened, nil
}

// libpqPassword matches a password or sslpassword keyword in a libpq keyword
// DSN, including a single-quoted value with spaces or escapes ('a b', 'it\'s').
var libpqPassword = regexp.MustCompile(`(?i)\b((?:ssl)?password\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`)

// redactedQueryParams are URL query parameters that carry a secret; pgx and
// libpq accept a password there as well as in the userinfo (#6034).
var redactedQueryParams = map[string]bool{"password": true, "sslpassword": true}

// resolveNode returns this node's ID and name. A configured [node] id
// wins; otherwise the ID persisted in the metadata store is used, minted
// on first start, so it survives restarts and config-free containers.
// The name defaults to the hostname.
func resolveNode(ctx context.Context, cfg config.NodeConfig, meta adapter.MetadataStore) (id, name string, err error) {
	id = cfg.ID
	if id == "" {
		if id, err = meta.EnsureNodeID(ctx, types.NewID()); err != nil {
			return "", "", fmt.Errorf("resolve node id: %w", err)
		}
	}
	name = cfg.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	return id, name, nil
}

// refuseLegacyIDs stops start-up on a database written before Memora's
// IDs were UUIDv4s. Pre-1.0 releases do not migrate such data (PRD ID-8,
// docs/user-docs/operations.md "Upgrading from Prefixed IDs"): every
// table, vector key and content path holds the old IDs, and serving them
// would fail validation on every request. Every memory lives in a
// workspace, so checking workspace IDs is enough. O(workspaces), once.
func refuseLegacyIDs(ctx context.Context, meta adapter.MetadataStore) error {
	cursor := ""
	for {
		page, next, err := meta.ListWorkspacesPaged(ctx, cursor, 500)
		if err != nil {
			return fmt.Errorf("check workspace IDs: %w", err)
		}
		for _, w := range page {
			if _, err := types.ParseID("workspace_id", w.ID); err != nil {
				return fmt.Errorf("metadata store holds workspace %q from before IDs were UUIDv4s; "+
					"this release does not migrate it. Start with an empty database "+
					"(see docs/user-docs/operations.md, \"Upgrading from Prefixed IDs\")", w.ID)
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// redactDSN masks the passwords in a URL DSN (postgres://user:pw@host/db,
// ?password=pw) or a libpq keyword DSN (password=pw, password='p w') so it
// can be logged. Anything without a password, such as a path, is returned
// unchanged (#5985, #6034).
func redactDSN(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Opaque == "" {
		changed := false
		if _, ok := u.User.Password(); ok {
			changed = true
		}
		if u.RawQuery != "" {
			parts := strings.Split(u.RawQuery, "&")
			for i, kv := range parts {
				k, _, _ := strings.Cut(kv, "=")
				if name, err := url.QueryUnescape(k); err == nil && redactedQueryParams[strings.ToLower(name)] {
					parts[i] = k + "=xxxxx"
					changed = true
				}
			}
			u.RawQuery = strings.Join(parts, "&")
		}
		if !changed {
			return dsn
		}
		return u.Redacted()
	}
	return libpqPassword.ReplaceAllString(dsn, "${1}xxxxx")
}
