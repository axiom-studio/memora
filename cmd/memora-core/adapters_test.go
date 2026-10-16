package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

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
	ctx := context.Background()
	svc, closeAll, err := openCoreAdapters(ctx, testAdapterConfig(t), discardLogger())
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
