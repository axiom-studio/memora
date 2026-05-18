// memora-core is the Memora server binary. It boots the configured
// PrimaryStore / VectorStore / LedgerStore adapters and serves the
// REST surface (plus the bundled MCP transport in `serve` mode).
package main

import (
	"context"
	"flag"
	"fmt"
	stdlog "log"
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
	_ "github.com/axiom-studio/memora/internal/ledger/sqlite"
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
  --mode               single-tenant | multi-tenant (default single-tenant)`)
}

func serve() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", getenv("MEMORA_ADDR", ":7777"), "listen address")
	dataDir := fs.String("data-dir", getenv("MEMORA_DATA_DIR", "./data"), "data directory for SQLite")
	primaryDriver := fs.String("primary-driver", getenv("MEMORA_PRIMARY_DRIVER", "sqlite"), "primary-store driver")
	vectorDriver := fs.String("vector-driver", getenv("MEMORA_VECTOR_DRIVER", "sqlite-vec"), "vector-store driver")
	ledgerDriver := fs.String("ledger-driver", getenv("MEMORA_LEDGER_DRIVER", "sqlite"), "ledger-store driver")
	embedModel := fs.String("embedding-model", getenv("MEMORA_EMBEDDING_MODEL", "noop:default"), "embedding model id")
	apiKey := fs.String("api-key", os.Getenv("MEMORA_API_KEY"), "API bearer key (empty disables auth)")
	mode := fs.String("mode", getenv("MEMORA_MODE", "single-tenant"), "single-tenant | multi-tenant")
	_ = fs.Parse(os.Args[1:])

	logger := stdlog.New(os.Stderr, "memora-core ", stdlog.LstdFlags|stdlog.LUTC)
	logger.Printf("starting on %s (mode=%s, primary=%s, vector=%s, ledger=%s, embedding=%s)",
		*addr, *mode, *primaryDriver, *vectorDriver, *ledgerDriver, *embedModel)

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
	logger.Printf("embedding provider: %s (dim=%d)", embedProvider.ModelID(), embedProvider.Dim())

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

	httpsrv := httpserver.New(httpserver.Config{
		Addr:    *addr,
		APIKey:  *apiKey,
		Service: svc,
		Logger:  logger,
		Mode:    *mode,
	})

	go func() {
		// Confirm the port is actually listening before we log "ready".
		host := *addr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		for i := 0; i < 20; i++ {
			c, err := net.Dial("tcp", host)
			if err == nil {
				_ = c.Close()
				logger.Printf("HTTP API ready at http://%s", host)
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
			logger.Fatalf("server failed: %v", err)
		}
	case sig := <-sigCh:
		logger.Printf("signal %v — shutting down", sig)
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
	_ = fs.Parse(os.Args[1:])

	logger := stdlog.New(os.Stderr, "memora-mcp ", stdlog.LstdFlags|stdlog.LUTC)
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
	server := mcp.NewServer(svc, logger)
	logger.Printf("MCP server ready on stdio (data-dir=%s, embedding=%s)", *dataDir, embedProvider.ModelID())
	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Fatalf("mcp serve: %v", err)
	}
}
