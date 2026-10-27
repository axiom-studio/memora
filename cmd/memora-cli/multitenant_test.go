package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	mcli "github.com/axiom-studio/memora/internal/cli"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/testmesh"
	"github.com/axiom-studio/memora/pkg/client"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestSearch_MultiTenant (#4379): against a multi-tenant server, the Go
// client with Workspace set and `memora-cli search` with --workspace /
// $MEMORA_WORKSPACE search only that tenant; without it the server
// refuses (400 missing_workspace) instead of searching across tenants.
func TestSearch_MultiTenant(t *testing.T) {
	n := testmesh.New(t, []string{"a"}, testmesh.Options{})[0]
	ts := httptest.NewServer(httpserver.New(httpserver.Config{
		Service: n.Svc, Mode: "multi-tenant", MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler())
	t.Cleanup(ts.Close)

	ctx := context.Background()
	admin := client.New(ts.URL, "")
	admin.AgentID = testmesh.AgentID
	var ws [2]string
	var mems [2]string
	for i, name := range []string{"tenant-a", "tenant-b"} {
		w, err := admin.CreateWorkspace(ctx, api.CreateWorkspaceRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		ws[i] = w.ID
		admin.Workspace = w.ID
		r, err := admin.Imprint(ctx, w.ID, api.ImprintRequest{Content: "salary bands for " + name})
		if err != nil {
			t.Fatal(err)
		}
		mems[i] = r.ID
	}

	sdk := client.New(ts.URL, "")
	var apiErr *client.APIError
	if _, err := sdk.Search(ctx, api.SearchRequest{Query: "salary", Mode: api.RecallModeKeyword}); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("search without a tenant: err = %v, want 400 missing_workspace", err)
	}
	sdk.Workspace = ws[0]
	resp, err := sdk.Search(ctx, api.SearchRequest{Query: "salary", Mode: api.RecallModeKeyword, WorkspaceIDs: []string{ws[0], ws[1]}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].MemoryID != mems[0] {
		t.Fatalf("tenant A search = %+v, want only A's memory", resp.Results)
	}

	cli := func(argv ...string) api.RecallResponse {
		t.Helper()
		g := parseGlobal(argv)
		var buf bytes.Buffer
		g.out = mcli.NewOutput(mcli.ParseFormat("json"), true, false, false)
		g.out.W = &buf
		g.Endpoint, g.Output, g.Args = ts.URL, "json", g.Args[1:] // drop "search"
		c := client.New(ts.URL, "")
		c.AgentID, c.Workspace = g.AgentID, g.Workspace
		cmdSearch(ctx, c, g)
		var r api.RecallResponse
		if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
			t.Fatalf("%v: %q: %v", argv, buf.String(), err)
		}
		return r
	}
	t.Setenv("MEMORA_WORKSPACE", "")
	if r := cli("search", "salary", "--mode", "keyword", "--workspace", ws[1]); len(r.Results) != 1 || r.Results[0].MemoryID != mems[1] {
		t.Fatalf("cli --workspace B: %+v", r.Results)
	}
	t.Setenv("MEMORA_WORKSPACE", ws[0])
	if r := cli("search", "salary", "--mode", "keyword"); len(r.Results) != 1 || r.Results[0].MemoryID != mems[0] {
		t.Fatalf("cli $MEMORA_WORKSPACE=A: %+v", r.Results)
	}
	t.Setenv("MEMORA_WORKSPACE", "")
	if r := cli("search", "salary", "--mode", "keyword", "--workspaces", ws[1]); len(r.Results) != 1 || r.Results[0].MemoryID != mems[1] {
		t.Fatalf("cli --workspaces B alone: %+v", r.Results)
	}
}
