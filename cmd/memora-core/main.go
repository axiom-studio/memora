// memora-core is the Memora server binary. It boots the configured
// PrimaryStore / VectorStore / LedgerStore adapters and serves the
// REST surface (plus the bundled MCP transport in `serve` mode).
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
  memora-core serve [flags]   start the HTTP + MCP server
  memora-core version         print version info

Common flags for 'serve':
  --addr               listen address (default :7777; can also be set via MEMORA_ADDR)
  --data-dir           directory for the SQLite database file (default ./data)
  --primary-driver     primary-store driver (default sqlite)
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
	addr := fs.String("addr", getenv("MEMORA_ADDR", ":7777"), "listen address")
	dataDir := fs.String("data-dir", getenv("MEMORA_DATA_DIR", "./data"), "data directory for SQLite")
	primaryDriver := fs.String("primary-driver", getenv("MEMORA_PRIMARY_DRIVER", "sqlite"), "primary-store driver")
	vectorDriver := fs.String("vector-driver", getenv("MEMORA_VECTOR_DRIVER", "sqlite-vec"), "vector-store driver")
	ledgerDriver := fs.String("ledger-driver", getenv("MEMORA_LEDGER_DRIVER", "sqlite"), "ledger-store driver")
	contentDriver := fs.String("content-driver", os.Getenv("MEMORA_CONTENT_DRIVER"), "content-store driver (empty = disabled; file | sqlite)")
	contentDSN := fs.String("content-dsn", os.Getenv("MEMORA_CONTENT_DSN"), "content-store DSN (e.g. /var/lib/memora/content for file driver)")
	embedModel := fs.String("embedding-model", getenv("MEMORA_EMBEDDING_MODEL", "noop:default"), "embedding model id")
	apiKey := fs.String("api-key", os.Getenv("MEMORA_API_KEY"), "API bearer key (empty disables auth)")
	allowNoAuth := fs.Bool("allow-no-auth", false, "explicit opt-in to run with an empty API key on a non-loopback bind")
	mode := fs.String("mode", getenv("MEMORA_MODE", "single-tenant"), "single-tenant | multi-tenant")
	mcpEnable := fs.Bool("mcp-enable", os.Getenv("MEMORA_MCP_ENABLE") == "true", "mount MCP WebSocket endpoint at /mcp on the HTTP server")
	_ = fs.Parse(os.Args[1:])

	bootLog := stdlog.New(os.Stderr, "memora-core ", stdlog.LstdFlags|stdlog.LUTC)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := validateAuthMode(*addr, *apiKey, *allowNoAuth); err != nil {
		bootLog.Fatalf("%v", err)
	}
	if *apiKey == "" {
		logger.Warn("auth disabled", "addr", *addr, "allow_no_auth", *allowNoAuth)
	} else {
		logger.Info("auth enabled", "key_tag", hashTag(*apiKey))
	}
	logger.Info("starting",
		"addr", *addr, "mode", *mode, "primary", *primaryDriver,
		"vector", *vectorDriver, "ledger", *ledgerDriver, "embedding", *embedModel)

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		bootLog.Fatalf("mkdir data-dir: %v", err)
	}
	dbPath := filepath.Join(*dataDir, "memora.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	primary, err := adapter.OpenPrimary(ctx, adapter.PrimaryConfig{Driver: *primaryDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open primary: %v", err)
	}
	defer primary.Close()
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{Driver: *vectorDriver, DSN: dbPath, Dim: 384})
	if err != nil {
		bootLog.Fatalf("open vector: %v", err)
	}
	defer vec.Close()
	led, err := adapter.OpenLedger(ctx, adapter.LedgerConfig{Driver: *ledgerDriver, DSN: dbPath})
	if err != nil {
		bootLog.Fatalf("open ledger: %v", err)
	}
	defer led.Close()

	embedProvider, err := embedding.Open(*embedModel)
	if err != nil {
		bootLog.Fatalf("open embedding: %v", err)
	}
	logger.Info("embedding provider ready", "model", embedProvider.ModelID(), "dim", embedProvider.Dim())

	// Pre-instantiate the identity providers Memora ships with.
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
		Primary:  primary,
		Vector:   vec,
		Ledger:   led,
		Embedder: embedProvider,
		Identity: identityMap,
	}
	if *contentDriver != "" {
		cdsn := *contentDSN
		if cdsn == "" {
			if *contentDriver == "sqlite" {
				cdsn = dbPath
			} else {
				cdsn = filepath.Join(*dataDir, "content")
			}
		}
		cs, err := adapter.OpenContent(ctx, adapter.ContentConfig{Driver: *contentDriver, DSN: cdsn})
		if err != nil {
			bootLog.Fatalf("open content: %v", err)
		}
		defer cs.Close()
		svc.Content = cs
		logger.Info("content store enabled", "driver", *contentDriver, "dsn", cdsn)
	}

	httpsrv := httpserver.New(httpserver.Config{
		Addr:        *addr,
		APIKey:      *apiKey,
		Service:     svc,
		Logger:      logger,
		Mode:        *mode,
		AllowNoAuth: *allowNoAuth,
	})

	if *mcpEnable {
		mcp.MountMCP(httpsrv.Mux(), svc, mcp.Config{APIKey: *apiKey})
		logger.Info("MCP WebSocket endpoint enabled", "path", "/mcp")
	}

	go func() {
		host := *addr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		for i := 0; i < 20; i++ {
			c, err := net.Dial("tcp", host)
			if err == nil {
				_ = c.Close()
				logger.Info("HTTP API ready", "url", "http://"+host)
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

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// runMCP serves the bundled MCP transport over stdio. The Server uses
// the same Service + adapter triple as `serve`, but bypasses the HTTP
// router — agent loops attach via memora-core's stdin/stdout.
func runMCP() {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dataDir := fs.String("data-dir", getenv("MEMORA_DATA_DIR", "./data"), "data directory")
	primaryDriver := fs.String("primary-driver", getenv("MEMORA_PRIMARY_DRIVER", "sqlite"), "primary-store driver")
	vectorDriver := fs.String("vector-driver", getenv("MEMORA_VECTOR_DRIVER", "sqlite-vec"), "vector-store driver")
	ledgerDriver := fs.String("ledger-driver", getenv("MEMORA_LEDGER_DRIVER", "sqlite"), "ledger-store driver")
	embedModel := fs.String("embedding-model", getenv("MEMORA_EMBEDDING_MODEL", "noop:default"), "embedding model id")
	apiKey := fs.String("api-key", os.Getenv("MEMORA_API_KEY"), "MCP API bearer key (empty disables auth — single-tenant local-dev only)")
	_ = fs.Parse(os.Args[1:])

	logger := stdlog.New(os.Stderr, "memora-mcp ", stdlog.LstdFlags|stdlog.LUTC)
	if *apiKey == "" {
		logger.Printf("WARNING: MCP AUTH DISABLED (no MEMORA_API_KEY) — stdio is local-only but any process with stdin access can call write tools")
	} else {
		logger.Printf("MCP AUTH ENABLED via API key (sha256[:8]=%s)", hashTag(*apiKey))
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		logger.Fatalf("mkdir data-dir: %v", err)
	}
	dbPath := filepath.Join(*dataDir, "memora.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	primary, err := adapter.OpenPrimary(ctx, adapter.PrimaryConfig{Driver: *primaryDriver, DSN: dbPath})
	if err != nil {
		logger.Fatalf("open primary: %v", err)
	}
	defer primary.Close()
	vec, err := adapter.OpenVector(ctx, adapter.VectorConfig{Driver: *vectorDriver, DSN: dbPath, Dim: 384})
	if err != nil {
		logger.Fatalf("open vector: %v", err)
	}
	defer vec.Close()
	led, err := adapter.OpenLedger(ctx, adapter.LedgerConfig{Driver: *ledgerDriver, DSN: dbPath})
	if err != nil {
		logger.Fatalf("open ledger: %v", err)
	}
	defer led.Close()

	embedProvider, err := embedding.Open(*embedModel)
	if err != nil {
		logger.Fatalf("open embedding: %v", err)
	}
	identityMap := map[string]adapter.IdentityProvider{}
	for _, name := range []string{string(types.IdentityProviderOpaque), string(types.IdentityProviderAnthropicSession)} {
		p, _ := adapter.OpenIdentity(name)
		identityMap[name] = p
	}
	svc := &service.Service{
		Primary:  primary,
		Vector:   vec,
		Ledger:   led,
		Embedder: embedProvider,
		Identity: identityMap,
	}
	server := mcp.NewServerWithConfig(svc, logger, mcp.Config{APIKey: *apiKey})
	logger.Printf("MCP server ready on stdio (data-dir=%s, embedding=%s)", *dataDir, embedProvider.ModelID())
	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Fatalf("mcp serve: %v", err)
	}
}
