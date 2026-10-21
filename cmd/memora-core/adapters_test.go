package main

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/axiom-studio/memora/pkg/types/api"
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
		m := &types.Memory{WorkspaceID: ws.ID, Content: body, WrittenByAgentID: "7c9517b4-7277-400b-959a-2ae960d295f6"}
		if _, err := svc.Metadata.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := svc.GraphLink(ctx, types.Edge{
		WorkspaceID: ws.ID, SourceMemoryID: ids[0], TargetMemoryID: ids[1],
		EdgeType: types.EdgeType("references"), CreatedByAgentID: "7c9517b4-7277-400b-959a-2ae960d295f6",
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
		// Never print the DSN itself: on a public repo's CI logs it is a secret (#6034).
		t.Fatalf("connect %s: %v", redactDSN(base), err)
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

// Issue #5985: adapter logs must never carry a database password.
func TestRedactDSN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://memora:s3cret@db:5432/memora?sslmode=require", "postgres://memora:xxxxx@db:5432/memora?sslmode=require"},
		{"postgres://memora@db:5432/memora", "postgres://memora@db:5432/memora"},
		{"host=db user=memora password=s3cret dbname=memora", "host=db user=memora password=xxxxx dbname=memora"},
		{"/data/content", "/data/content"},
		{"./data/memora.db?_pragma=foreign_keys(ON)", "./data/memora.db?_pragma=foreign_keys(ON)"},
		{"", ""},
		// #6034: a password in the query string, with and without a user.
		{"postgres://memora@db:5432/m?password=S3cret", "postgres://memora@db:5432/m?password=xxxxx"},
		{"postgres://db/m?password=S3cret&sslmode=require", "postgres://db/m?password=xxxxx&sslmode=require"},
		{"postgres://memora:a@db/m?sslmode=require&sslpassword=k3y", "postgres://memora:xxxxx@db/m?sslmode=require&sslpassword=xxxxx"},
		{"postgres://db/m?PassWord=S3cret", "postgres://db/m?PassWord=xxxxx"},
		// #6034: quoted libpq values, which may contain spaces and escapes.
		{"host=db user=memora password='my S3cret' dbname=m", "host=db user=memora password=xxxxx dbname=m"},
		{`host=db password='it\'s S3cret' dbname=m`, "host=db password=xxxxx dbname=m"},
		{"host=db password = S3cret sslpassword='k 3y' dbname=m", "host=db password = xxxxx sslpassword=xxxxx dbname=m"},
		{"file:/data/memora.db?mode=ro", "file:/data/memora.db?mode=ro"},
	}
	for _, c := range cases {
		if got := redactDSN(c.in); got != c.want {
			t.Errorf("redactDSN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOpenCoreAdapters_ContentLogOmitsPassword(t *testing.T) {
	dsn := freshPostgresDB(t) // URL DSN carrying the server's real password
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		t.Skip("MEMORA_POSTGRES_DSN has no password to leak")
	}
	var buf strings.Builder
	cfg := testAdapterConfig(t)
	cfg.Storage.ContentDriver = "postgres"
	cfg.Storage.ContentDSN = dsn
	_, closeAll, err := openCoreAdapters(t.Context(), cfg, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	defer closeAll()
	logs := buf.String()
	if !strings.Contains(logs, "content store enabled") {
		t.Fatalf("expected the content-store log line; got:\n%s", logs)
	}
	if leaked := u.User.Username() + ":" + password + "@"; strings.Contains(logs, leaked) {
		t.Fatalf("startup logs contain the database credentials %q:\n%s", leaked, logs)
	}
}

// Todo #4229: embedding.async wires the embed pool, and closeAll drains it
// before closing the stores, so a write accepted just before shutdown is
// still embedded.
func TestOpenCoreAdapters_AsyncEmbedPoolDrainsOnClose(t *testing.T) {
	cfg := testAdapterConfig(t)
	ctx := context.Background()

	svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	if svc.Pool != nil {
		closeAll()
		t.Fatal("Pool wired with embedding.async unset; want nil (inline embed is the default)")
	}
	closeAll()

	cfg.Embedding.Async = true
	svc, closeAll, err = openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters (async): %v", err)
	}
	if svc.Pool == nil {
		closeAll()
		t.Fatal("Pool is nil with embedding.async = true")
	}
	ws := &types.Workspace{Name: "async"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		closeAll()
		t.Fatalf("CreateWorkspace: %v", err)
	}
	resp, err := svc.Imprint(ctx, ws.ID, "8acd8ee5-bb49-40d3-814a-329358a35fa4", api.ImprintRequest{Content: "queued before shutdown"})
	if err != nil {
		closeAll()
		t.Fatalf("Imprint: %v", err)
	}
	if resp.RecallReady {
		t.Error("Imprint returned recall_ready=true with async embed")
	}
	closeAll()

	cfg.Embedding.Async = false
	svc, closeAll, err = openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer closeAll()
	m, err := svc.Metadata.GetMemory(ctx, resp.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if !m.RecallReady {
		t.Fatal("memory not recall_ready after closeAll; the pool did not drain before the stores closed")
	}
}

// TestOpenCoreAdapters_RefusesLegacyPrefixedIDs: a database written
// before IDs were UUIDv4s (here, a "ws_…" workspace row) stops start-up
// with a message naming the workspace and the upgrade doc, instead of
// serving data every request would reject. A fresh database starts.
func TestOpenCoreAdapters_RefusesLegacyPrefixedIDs(t *testing.T) {
	cfg := testAdapterConfig(t)
	_, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("fresh database must start: %v", err)
	}
	closeAll()

	db, err := sql.Open("sqlite", filepath.Join(cfg.Storage.DataDir, "memora.db"))
	if err != nil {
		t.Fatal(err)
	}
	const legacy = "ws_01HXYZABCDEFGHJKMNPQRSTVWX"
	_, err = db.Exec(`INSERT INTO memora_workspaces (id, name, created_at, updated_at) VALUES (?, 'old', datetime('now'), datetime('now'))`, legacy)
	_ = db.Close()
	if err != nil {
		t.Fatalf("insert legacy workspace: %v", err)
	}

	_, _, err = openCoreAdapters(context.Background(), cfg, discardLogger())
	if err == nil || !strings.Contains(err.Error(), legacy) || !strings.Contains(err.Error(), "Upgrading from Prefixed IDs") {
		t.Fatalf("openCoreAdapters on a legacy database = %v, want a refusal naming %s and the upgrade doc", err, legacy)
	}
}

// TestOpenCoreAdapters_NodeIdentity: without config the node mints an ID
// on first start and keeps it across restarts on the same data dir; a
// configured [node] id wins, and [node] name is used as given.
func TestOpenCoreAdapters_NodeIdentity(t *testing.T) {
	cfg := testAdapterConfig(t)
	svc, closeAll, err := openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters: %v", err)
	}
	first := svc.NodeID
	closeAll()
	if _, err := types.ParseID("node_id", first); err != nil {
		t.Fatalf("minted node ID: %v", err)
	}
	if svc.NodeName == "" {
		t.Error("node name should default to the hostname")
	}

	svc, closeAll, err = openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if svc.NodeID != first {
		t.Fatalf("node ID after restart = %q, want %q", svc.NodeID, first)
	}
	closeAll()

	cfg.Node.ID, cfg.Node.Name = types.NewID(), "edge-1"
	svc, closeAll, err = openCoreAdapters(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("configured node: %v", err)
	}
	defer closeAll()
	if svc.NodeID != cfg.Node.ID || svc.NodeName != "edge-1" {
		t.Fatalf("node = %q/%q, want configured %q/edge-1", svc.NodeID, svc.NodeName, cfg.Node.ID)
	}
}

// TestOpenCoreAdapters_WarnsOnPlaceholderEmbedder (S-6): booting with the
// default noop model logs a warning that vector search is unavailable;
// a searchable model does not.
func TestOpenCoreAdapters_WarnsOnPlaceholderEmbedder(t *testing.T) {
	for model, wantWarn := range map[string]bool{"noop:default": true, "hash:default": false} {
		cfg := testAdapterConfig(t)
		cfg.Embedding.Model = model
		var buf bytes.Buffer
		_, closeAll, err := openCoreAdapters(context.Background(), cfg, slog.New(slog.NewTextHandler(&buf, nil)))
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		closeAll()
		if got := strings.Contains(buf.String(), "vector search is unavailable"); got != wantWarn {
			t.Errorf("%s: warning logged = %v, want %v", model, got, wantWarn)
		}
	}
}
