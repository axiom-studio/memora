package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/pkg/client"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// seedTwoWorkspaces creates two workspaces holding the same phrase and
// returns their IDs.
func seedTwoWorkspaces(t *testing.T, ts *httptest.Server, phrase string) (string, string) {
	t.Helper()
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}
	var ids []string
	for _, name := range []string{"search-a", "search-b"} {
		_, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": name}, nil))
		id := ws["id"].(string)
		if status, body := doJSON(t, c, jsonReq(t, "POST", base+"/"+id+"/memories", map[string]any{"content": phrase}, agentH)); status != 201 {
			t.Fatalf("imprint into %s: %d %v", name, status, body)
		}
		ids = append(ids, id)
	}
	return ids[0], ids[1]
}

func resultWorkspaces(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	results, _ := body["results"].([]any)
	for _, r := range results {
		got[r.(map[string]any)["workspace_id"].(string)] = true
	}
	return got
}

// TestSearch_E2E_NodeWide: POST /v1/search returns hits from every
// workspace with their workspace_id, needs no agent header (it is a
// query), and rejects a bad workspace_ids entry.
func TestSearch_E2E_NodeWide(t *testing.T) {
	ts := newTestServer(t)
	wsA, wsB := seedTwoWorkspaces(t, ts, "telescope calibration log")

	status, body := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/search", map[string]any{"query": "telescope calibration", "mode": "keyword"}, nil))
	if got := resultWorkspaces(t, body); status != 200 || !got[wsA] || !got[wsB] {
		t.Fatalf("search: status=%d workspaces=%v, want 200 with %s and %s", status, got, wsA, wsB)
	}

	status, body = doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/search", map[string]any{"query": "x", "workspace_ids": []string{"ws_1"}}, nil))
	if status != 400 || body["error_code"] != "invalid_input" {
		t.Fatalf("bad workspace_ids: status=%d body=%v, want 400 invalid_input", status, body)
	}
	status, _ = doJSON(t, ts.Client(), jsonReq(t, "GET", ts.URL+"/v1/search", nil, nil))
	if status != 405 {
		t.Fatalf("GET /v1/search: status=%d, want 405", status)
	}
}

// TestSearch_E2E_MultiTenantPinsToTenant: in multi-tenant mode a search
// never leaves the X-Memora-Workspace tenant, even when the body names
// other workspaces.
func TestSearch_E2E_MultiTenantPinsToTenant(t *testing.T) {
	srv := httpserver.New(httpserver.Config{
		Service:      newTestService(t),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
		Mode:         "multi-tenant",
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	wsA, wsB := seedTwoWorkspaces(t, ts, "tenant scoped phrase")

	req := jsonReq(t, "POST", ts.URL+"/v1/search",
		map[string]any{"query": "tenant scoped phrase", "mode": "keyword", "workspace_ids": []string{wsB}},
		map[string]string{"X-Memora-Workspace": wsA})
	status, body := doJSON(t, ts.Client(), req)
	if got := resultWorkspaces(t, body); status != 200 || len(got) != 1 || !got[wsA] {
		t.Fatalf("multi-tenant search: status=%d workspaces=%v, want only tenant %s", status, got, wsA)
	}
}

// TestSearch_E2E_Client: the Go client's Search (used by memora-cli
// search) round-trips through the real server.
func TestSearch_E2E_Client(t *testing.T) {
	ts := newTestServer(t)
	wsA, wsB := seedTwoWorkspaces(t, ts, "client side phrase")

	resp, err := client.New(ts.URL, "").Search(context.Background(), api.SearchRequest{Query: "client side phrase", Mode: api.RecallModeKeyword, WorkspaceIDs: []string{wsB}})
	if err != nil {
		t.Fatalf("Client.Search: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].WorkspaceID != wsB {
		t.Fatalf("Client.Search results = %+v, want one hit in %s (not %s)", resp.Results, wsB, wsA)
	}
}

// TestSearch_E2E_MultiTenantFailsClosed (#6159): in multi-tenant mode a
// search without the tenant header is refused rather than widened to
// every tenant, and a non-UUID tenant header is invalid input.
func TestSearch_E2E_MultiTenantFailsClosed(t *testing.T) {
	srv := httpserver.New(httpserver.Config{
		Service:      newTestService(t),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
		Mode:         "multi-tenant",
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	seedTwoWorkspaces(t, ts, "tenant data")

	status, body := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/search", map[string]any{"query": "tenant data"}, nil))
	if status != 400 || body["error_code"] != "missing_workspace" {
		t.Fatalf("no tenant header: status=%d body=%v, want 400 missing_workspace", status, body)
	}
	status, body = doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/search", map[string]any{"query": "tenant data"},
		map[string]string{"X-Memora-Workspace": "tenant-a"}))
	if status != 400 || body["error_code"] != "invalid_input" {
		t.Fatalf("non-UUID tenant header: status=%d body=%v, want 400 invalid_input", status, body)
	}
}
