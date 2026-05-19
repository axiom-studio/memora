// memora-core is the Memora server binary. It boots the configured
// MetadataStore / VectorStore / LedgerStore / ContentStore / GraphStore
// adapters and serves the REST + MCP surface.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/axiom-studio/memora/internal/certgen"
	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/mcp"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"

	// Register adapters in init().
	_ "github.com/axiom-studio/memora/internal/ledger/file"
	_ "github.com/axiom-studio/memora/internal/ledger/postgres"
	_ "github.com/axiom-studio/memora/internal/ledger/sqlite"
	_ "github.com/axiom-studio/memora/internal/store/file"
	_ "github.com/axiom-studio/memora/internal/store/pgvector"
	_ "github.com/axiom-studio/memora/internal/store/postgres"
	_ "github.com/axiom-studio/memora/internal/store/sqlite"
	_ "github.com/axiom-studio/memora/internal/store/sqlitevec"
	_ "github.com/axiom-studio/memora/pkg/identity"
)

var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		serve()
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "mcp" {
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		runMCP()
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "init-cert" {
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		runInitCert()
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "print-config" {
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		runPrintConfig()
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "check-config" {
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		runCheckConfig()
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Printf("memora-core %s (commit=%s built=%s)\n", version, commit, buildDate)
		return
	}
	if len(os.Args) >= 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		printHelp()
		return
	}
	printHelp()
}

func printHelp() {
	fmt.Println(`memora-core — the Memora server.

Usage:
  memora-core serve [flags]       start the HTTP + MCP server
  memora-core init-cert [flags]   generate self-signed TLS certificate
  memora-core print-config        print resolved config (secrets redacted)
  memora-core check-config        validate config (exit 78 on error)
  memora-core version             print version info

Common flags for 'serve':
  --config             path to TOML config file (search: $MEMORA_CONFIG → ~/.memora/config.toml → ./memora.toml)
  --addr               listen address (default :7777; can also be set via MEMORA_ADDR)
  --data-dir           directory for the SQLite database file (default ./data)
  --metadata-driver    metadata-store driver (default sqlite)
  --vector-driver      vector-store driver (default sqlite-vec)
  --ledger-driver      ledger-store driver (default sqlite)
  --embedding-model    embedding model id (default noop:default)
  --api-key            API bearer key (default $MEMORA_API_KEY; empty disables auth — local dev)
  --allow-no-auth      explicit opt-in to run with an empty API key on a non-loopback bind
  --mode               single-tenant | multi-tenant (default single-tenant)
  --mcp-enable         mount MCP WebSocket endpoint at /mcp on the HTTP server (default false)`)
}

// errNoAuthNonLoopback is returned by validateAuthMode when the operator
// runs without an API key on an externally-reachable bind without the
// explicit --allow-no-auth opt-in. The textbook silent-no-auth default
// anti-pattern (CWE-1188) is replaced with a refuse-to-start.
var errNoAuthNonLoopback = errors.New("AUTH DISABLED on a non-loopback bind")

// validateAuthMode returns an error when running with an empty API key
// against a non-loopback address without the explicit --allow-no-auth
// opt-in. Pure function — exercised by main_test.go without spawning a
// process.
func validateAuthMode(addr, apiKey string, allowNoAuth bool) error {
	if apiKey != "" {
		return nil
	}
	if allowNoAuth {
		return nil
	}
	if bindsToLoopback(addr) {
		return nil
	}
	return fmt.Errorf("%w: --addr=%q is reachable beyond loopback; set MEMORA_API_KEY, pass --allow-no-auth, or bind 127.0.0.1:PORT for local dev", errNoAuthNonLoopback, addr)
}

// bindsToLoopback returns true when addr accepts only loopback traffic.
// Recognizes the empty string, ":NNNN" (Go's "all interfaces" form is
// rejected — it binds 0.0.0.0), 127.0.0.1, ::1, and the literal
// "localhost". ":NNNN" alone (no host) is treated as non-loopback because
// net.Listen interprets it as 0.0.0.0:NNNN.
func bindsToLoopback(addr string) bool {
	if addr == "" {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Could be a bare host with no port; treat conservatively.
		host = addr
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// hashTag returns the first 8 hex chars of sha256(key). 32 bits of
// entropy lets an operator confirm which API key is loaded without
// leaking material — a brute-force attacker would need 2^256 work to
// recover the full key from this tag.
func hashTag(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

func serve() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config file")
	addr := fs.String("addr", "", "listen address")
	dataDir := fs.String("data-dir", "", "data directory for SQLite")
	metadataDriver := fs.String("metadata-driver", "", "metadata-store driver")
	vectorDriver := fs.String("vector-driver", "", "vector-store driver")
	ledgerDriver := fs.String("ledger-driver", "", "ledger-store driver")
	graphDriver := fs.String("graph-driver", "", "graph-store driver")
	contentDriver := fs.String("content-driver", "", "content-store driver (empty = disabled; file | sqlite)")
	contentDSN := fs.String("content-dsn", "", "content-store DSN")
	embedModel := fs.String("embedding-model", "", "embedding model id")
	apiKey := fs.String("api-key", "", "API bearer key (empty disables auth)")
	allowNoAuth := fs.Bool("allow-no-auth", false, "explicit opt-in to run with an empty API key on a non-loopback bind")
	mode := fs.String("mode", "", "single-tenant | multi-tenant")
	mcpEnable := fs.Bool("mcp-enable", false, "mount MCP WebSocket endpoint at /mcp on the HTTP server")
	_ = fs.Parse(os.Args[1:])

	bootLog := stdlog.New(os.Stderr, "memora-core ", stdlog.LstdFlags|stdlog.LUTC)

	// Load config: file defaults → env overlay → CLI flag overlay.
	cfg, err := config.Load(*configPath)
	if err != nil {
		bootLog.Fatalf("config: %v", err)
	}
	cfg.ApplyEnv()
	applyServeFlags(fs, &cfg, *addr, *dataDir, *metadataDriver, *vectorDriver,
		*ledgerDriver, *graphDriver, *contentDriver, *contentDSN,
		*embedModel, *apiKey, *mode, *allowNoAuth, *mcpEnable)

	if err := cfg.Validate(); err != nil {
		bootLog.Fatalf("config validation failed (exit %d): %v", config.EX_CONFIG, err)
		os.Exit(config.EX_CONFIG)
	}

	resolvedKey, err := cfg.ResolveAPIKey()
	if err != nil {
		bootLog.Fatalf("config: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := validateAuthMode(cfg.Server.Addr, resolvedKey, cfg.Server.AllowNoAuth); err != nil {
		bootLog.Fatalf("%v", err)
	}
	if resolvedKey == "" {
		logger.Warn("auth disabled", "addr", cfg.Server.Addr, "allow_no_auth", cfg.Server.AllowNoAuth)
	} else {
		logger.Info("auth enabled", "key_tag", hashTag(resolvedKey))
	}
	if cfg.Source != "" {
		logger.Info("config loaded", "source", cfg.Source)
	}
	logger.Info("starting",
		"addr", cfg.Server.Addr, "mode", cfg.Server.Mode,
		"metadata", cfg.Storage.MetadataDriver, "vector", cfg.Storage.VectorDriver,
		"ledger", cfg.Storage.LedgerDriver, "embedding", cfg.Embedding.Model)

	if err := os.MkdirAll(cfg.Storage.DataDir, 0o755); err != nil {
		bootLog.Fatalf("mkdir data-dir: %v", err)
	}
	dbPath := filepath.Join(cfg.Storage.DataDir, "memora.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meta, err := adapter.OpenMetadata(ctx, adapter.MetadataConfig{Driver: cfg.Storage.MetadataDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open metadata: %v", err)
	}
	defer meta.Close()
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{Driver: cfg.Storage.VectorDriver, DSN: dbPath, Dim: 384})
	if err != nil {
		bootLog.Fatalf("open vector: %v", err)
	}
	defer vec.Close()
	led, err := adapter.OpenLedger(ctx, adapter.LedgerConfig{Driver: cfg.Storage.LedgerDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open ledger: %v", err)
	}
	defer led.Close()

	embedProvider, err := embedding.Open(cfg.Embedding.Model)
	if err != nil {
		bootLog.Fatalf("open embedding: %v", err)
	}
	logger.Info("embedding provider ready", "model", embedProvider.ModelID(), "dim", embedProvider.Dim())

	identityMap := map[string]adapter.IdentityProvider{}
	for _, name := range []string{
		string(types.IdentityProviderOpaque),
		string(types.IdentityProviderAnthropicSession),
	} {
		p, err := adapter.OpenIdentity(name)
		if err == nil {
			identityMap[name] = p
		}
	}

	svc := &service.Service{
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
			bootLog.Fatalf("open content: %v", err)
		}
		defer cs.Close()
		svc.Content = cs
		logger.Info("content store enabled", "driver", cfg.Storage.ContentDriver, "dsn", cdsn)
	}
	gs, err := adapter.OpenGraph(ctx, adapter.GraphConfig{Driver: cfg.Storage.GraphDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open graph: %v", err)
	}
	defer gs.Close()
	svc.Graph = gs
	logger.Info("graph store enabled", "driver", cfg.Storage.GraphDriver)

	var ccaps *adapter.ContentCapabilities
	if svc.Content != nil {
		c := svc.Content.Capabilities()
		ccaps = &c
	}
	gcaps := gs.Capabilities()
	if err := adapter.ValidateCompatibility(logger, meta.Capabilities(), ccaps, &gcaps); err != nil {
		bootLog.Fatalf("compatibility check failed: %v", err)
	}

	httpsrv := httpserver.New(httpserver.Config{
		Addr:        cfg.Server.Addr,
		APIKey:      resolvedKey,
		Service:     svc,
		Logger:      logger,
		Mode:        cfg.Server.Mode,
		AllowNoAuth: cfg.Server.AllowNoAuth,
		TLS: httpserver.TLSConfig{
			Enabled:        cfg.Server.TLS.Enabled,
			CertFile:       cfg.Server.TLS.CertFile,
			KeyFile:        cfg.Server.TLS.KeyFile,
			AutoSelfSigned: cfg.Server.TLS.AutoSelfSigned,
			Host:           cfg.Server.Addr,
		},
	})

	if cfg.Server.MCPEnable {
		mcp.MountMCP(httpsrv.Mux(), svc, mcp.Config{APIKey: resolvedKey})
		logger.Info("MCP WebSocket endpoint enabled", "path", "/mcp")
	}

	go func() {
		host := cfg.Server.Addr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		for i := 0; i < 20; i++ {
			c, err := net.Dial("tcp", host)
			if err == nil {
				_ = c.Close()
				scheme := "http"
				if cfg.Server.TLS.Enabled {
					scheme = "https"
				}
				logger.Info("HTTP API ready", "url", scheme+"://"+host)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() { errCh <- httpsrv.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			bootLog.Fatalf("server failed: %v", err)
		}
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpsrv.Shutdown(shutdownCtx)
	}
}

// applyServeFlags overlays explicitly-set CLI flags onto the config.
// Only flags the user actually passed on the command line win; flags
// left at their zero-value are skipped so the config-file / env layer
// is preserved.
func applyServeFlags(fs *flag.FlagSet, cfg *config.Config,
	addr, dataDir, metaDriver, vecDriver, ledgerDriver, graphDriver,
	contentDriver, contentDSN, embedModel, apiKey, mode string,
	allowNoAuth, mcpEnable bool,
) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if set["addr"] {
		cfg.Server.Addr = addr
	}
	if set["data-dir"] {
		cfg.Storage.DataDir = dataDir
	}
	if set["metadata-driver"] {
		cfg.Storage.MetadataDriver = metaDriver
	}
	if set["vector-driver"] {
		cfg.Storage.VectorDriver = vecDriver
	}
	if set["ledger-driver"] {
		cfg.Storage.LedgerDriver = ledgerDriver
	}
	if set["graph-driver"] {
		cfg.Storage.GraphDriver = graphDriver
	}
	if set["content-driver"] {
		cfg.Storage.ContentDriver = contentDriver
	}
	if set["content-dsn"] {
		cfg.Storage.ContentDSN = contentDSN
	}
	if set["embedding-model"] {
		cfg.Embedding.Model = embedModel
	}
	if set["api-key"] {
		cfg.Server.APIKey = apiKey
	}
	if set["mode"] {
		cfg.Server.Mode = mode
	}
	if set["allow-no-auth"] {
		cfg.Server.AllowNoAuth = allowNoAuth
	}
	if set["mcp-enable"] {
		cfg.Server.MCPEnable = mcpEnable
	}
}

// runMCP serves the bundled MCP transport over stdio. The Server uses
// the same Service + adapter triple as `serve`, but bypasses the HTTP
// router — agent loops attach via memora-core's stdin/stdout.
func runMCP() {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config file")
	dataDir := fs.String("data-dir", "", "data directory")
	metadataDriver := fs.String("metadata-driver", "", "metadata-store driver")
	vectorDriver := fs.String("vector-driver", "", "vector-store driver")
	ledgerDriver := fs.String("ledger-driver", "", "ledger-store driver")
	embedModel := fs.String("embedding-model", "", "embedding model id")
	apiKey := fs.String("api-key", "", "MCP API bearer key")
	_ = fs.Parse(os.Args[1:])

	bootLog := stdlog.New(os.Stderr, "memora-mcp ", stdlog.LstdFlags|stdlog.LUTC)

	cfg, err := config.Load(*configPath)
	if err != nil {
		bootLog.Fatalf("config: %v", err)
	}
	cfg.ApplyEnv()

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["data-dir"] {
		cfg.Storage.DataDir = *dataDir
	}
	if set["metadata-driver"] {
		cfg.Storage.MetadataDriver = *metadataDriver
	}
	if set["vector-driver"] {
		cfg.Storage.VectorDriver = *vectorDriver
	}
	if set["ledger-driver"] {
		cfg.Storage.LedgerDriver = *ledgerDriver
	}
	if set["embedding-model"] {
		cfg.Embedding.Model = *embedModel
	}
	if set["api-key"] {
		cfg.Server.APIKey = *apiKey
	}

	resolvedKey, err := cfg.ResolveAPIKey()
	if err != nil {
		bootLog.Fatalf("config: %v", err)
	}

	if resolvedKey == "" {
		bootLog.Printf("WARNING: MCP AUTH DISABLED (no MEMORA_API_KEY) — stdio is local-only but any process with stdin access can call write tools")
	} else {
		bootLog.Printf("MCP AUTH ENABLED via API key (sha256[:8]=%s)", hashTag(resolvedKey))
	}
	if err := os.MkdirAll(cfg.Storage.DataDir, 0o755); err != nil {
		bootLog.Fatalf("mkdir data-dir: %v", err)
	}
	dbPath := filepath.Join(cfg.Storage.DataDir, "memora.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meta, err := adapter.OpenMetadata(ctx, adapter.MetadataConfig{Driver: cfg.Storage.MetadataDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open metadata: %v", err)
	}
	defer meta.Close()
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{Driver: cfg.Storage.VectorDriver, DSN: dbPath, Dim: 384})
	if err != nil {
		bootLog.Fatalf("open vector: %v", err)
	}
	defer vec.Close()
	led, err := adapter.OpenLedger(ctx, adapter.LedgerConfig{Driver: cfg.Storage.LedgerDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open ledger: %v", err)
	}
	defer led.Close()

	embedProvider, err := embedding.Open(cfg.Embedding.Model)
	if err != nil {
		bootLog.Fatalf("open embedding: %v", err)
	}
	identityMap := map[string]adapter.IdentityProvider{}
	for _, name := range []string{string(types.IdentityProviderOpaque), string(types.IdentityProviderAnthropicSession)} {
		p, _ := adapter.OpenIdentity(name)
		identityMap[name] = p
	}
	svc := &service.Service{
		Metadata: meta,
		Vector:   vec,
		Ledger:   led,
		Embedder: embedProvider,
		Identity: identityMap,
	}
	server := mcp.NewServerWithConfig(svc, bootLog, mcp.Config{APIKey: resolvedKey})
	bootLog.Printf("MCP server ready on stdio (data-dir=%s, embedding=%s)", cfg.Storage.DataDir, embedProvider.ModelID())
	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		bootLog.Fatalf("mcp serve: %v", err)
	}
}

func runInitCert() {
	fs := flag.NewFlagSet("init-cert", flag.ExitOnError)
	host := fs.String("host", "", "hostname for the certificate CN and SAN (required)")
	outputDir := fs.String("output", "", "output directory (default ~/.memora/tls/)")
	useRSA := fs.Bool("rsa", false, "use RSA 4096 instead of ECDSA P-256")
	var extraSANs stringSlice
	fs.Var(&extraSANs, "san", "additional SAN (repeatable)")
	_ = fs.Parse(os.Args[1:])

	if *host == "" {
		fmt.Fprintln(os.Stderr, "error: --host is required")
		fmt.Fprintln(os.Stderr, "usage: memora-core init-cert --host <hostname> [--output <dir>] [--rsa] [--san <host>...]")
		os.Exit(1)
	}

	res, err := certgen.Generate(certgen.Opts{
		Host:      *host,
		OutputDir: *outputDir,
		UseRSA:    *useRSA,
		ExtraSANs: extraSANs,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Certificate: %s\n", res.CertPath)
	fmt.Printf("Private key: %s\n", res.KeyPath)
	fmt.Printf("Fingerprint: SHA256:%s\n", res.Fingerprint)
	fmt.Println()
	fmt.Println("Add to your config.toml:")
	fmt.Println(res.ConfigSnippet())
}

func runPrintConfig() {
	fs := flag.NewFlagSet("print-config", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config file")
	_ = fs.Parse(os.Args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	cfg.ApplyEnv()

	redacted := cfg.Redacted()
	if err := toml.NewEncoder(os.Stdout).Encode(redacted); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding config: %v\n", err)
		os.Exit(1)
	}
}

func runCheckConfig() {
	fs := flag.NewFlagSet("check-config", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config file")
	_ = fs.Parse(os.Args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(config.EX_CONFIG)
	}
	cfg.ApplyEnv()

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation failed: %v\n", err)
		os.Exit(config.EX_CONFIG)
	}
	fmt.Println("config ok")
}

type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}
