package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

func testAdapterConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Storage.DataDir = t.TempDir()
	return cfg
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestOpenCoreAdapters_WiresEverySeam is the boot-parity guard for serve and
// mcp: both build their Service here, so a missing seam fails this test
// instead of silently disabling tools on one transport.
func TestOpenCoreAdapters_WiresEverySeam(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.ContentDriver = "sqlite"

	svc, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()

	for name, missing := range map[string]bool{
		"Metadata": svc.Metadata == nil,
		"Vector":   svc.Vector == nil,
		"Ledger":   svc.Ledger == nil,
		"Embedder": svc.Embedder == nil,
		"Graph":    svc.Graph == nil,
		"Content":  svc.Content == nil,
	} {
		if missing {
			t.Errorf("Service.%s is nil", name)
		}
	}
	for _, provider := range []types.IdentityProvider{types.IdentityProviderOpaque, types.IdentityProviderAnthropicSession} {
		if svc.Identity[string(provider)] == nil {
			t.Errorf("identity provider %q not registered", provider)
		}
	}
}

func TestOpenCoreAdapters_ContentDisabledByDefault(t *testing.T) {
	svc, closeAll, err := openCoreAdapters(context.Background(), testAdapterConfig(t), discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()
	if svc.Content != nil {
		t.Fatal("Content opened with empty content_driver; want nil (opt-in)")
	}
	if svc.Graph == nil {
		t.Fatal("Graph is nil; graph_driver defaults to sqlite_graph and must always be wired")
	}
}

func TestOpenCoreAdapters_GraphStoreRoundTrip(t *testing.T) {
	assertGraphLinkWorks(t, testAdapterConfig(t))
}

// With a custom metadata_dsn and no graph_dsn, sqlite_graph must open the
// metadata database (where memora_edges lives), not <data_dir>/memora.db.
func TestOpenCoreAdapters_GraphDSNFollowsMetadataDSN(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.MetadataDSN = filepath.Join(t.TempDir(), "custom-metadata.db")
	assertGraphLinkWorks(t, cfg)
}

func TestOpenCoreAdapters_EmptyGraphDriver_DisablesGraph(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.GraphDriver = ""
	svc, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()
	if svc.Graph != nil {
		t.Fatal("Graph opened with empty graph_driver; want nil (disabled)")
	}
	if _, err := svc.GraphLink(context.Background(), types.Edge{}); !errors.Is(err, types.ErrCapability) {
		t.Fatalf("GraphLink with graph disabled err = %v, want ErrCapability", err)
	}
}

func assertGraphLinkWorks(t *testing.T, cfg config.Config) {
	t.Helper()
	ctx := context.Background()
	svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()

	ws := &types.Workspace{Name: "graph"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, body := range []string{"source", "target"} {
		m := &types.Memory{WorkspaceID: ws.ID, Content: body, WrittenByAgentID: "agent_opaque_local"}
		if _, err := svc.Metadata.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := svc.GraphLink(ctx, types.Edge{
		WorkspaceID: ws.ID, SourceMemoryID: ids[0], TargetMemoryID: ids[1],
		EdgeType: types.EdgeType("references"), CreatedByAgentID: "agent_opaque_local",
	}); err != nil {
		t.Fatalf("GraphLink through the wired GraphStore: %v", err)
	}
}

func TestOpenCoreAdapters_UnknownGraphDriver_NamesTheSeam(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.GraphDriver = "no-such-driver"
	_, _, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err == nil || !strings.Contains(err.Error(), `open graph (driver "no-such-driver")`) {
		t.Fatalf("err = %v, want it to name the graph seam and driver", err)
	}
}

// freshPostgresDB creates a throwaway database on MEMORA_POSTGRES_DSN's
// server and returns a DSN pointing at it, so table DDL from other tests
// (memora_cell_vectors is CREATE TABLE IF NOT EXISTS) cannot leak in.
func freshPostgresDB(t *testing.T) string {
	t.Helper()
	base := os.Getenv("MEMORA_POSTGRES_DSN")
	if base == "" {
		t.Skip("MEMORA_POSTGRES_DSN not set; skipping postgres integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect %s: %v", base, err)
	}
	name := fmt.Sprintf("memora_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("MEMORA_POSTGRES_DSN must be a URL DSN: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestOpenCoreAdapters_PgvectorColumnSizedFromEmbedder(t *testing.T) {
	dsn := freshPostgresDB(t)
	cfg := testAdapterConfig(t)
	cfg.Storage.VectorDriver = "pgvector"
	cfg.Storage.VectorDSN = dsn
	cfg.Embedding.Model = "openai:text-embedding-3-small" // Dim 1536; opening makes no network call

	_, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var colType string
	if err := conn.QueryRow(context.Background(), `
SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a
WHERE a.attrelid = 'memora_cell_vectors'::regclass AND a.attname = 'embedding'`).Scan(&colType); err != nil {
		t.Fatalf("read embedding column type: %v", err)
	}
	if colType != "vector(1536)" {
		t.Fatalf("embedding column = %s, want vector(1536) from the embedder's Dim()", colType)
	}
}
