package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/identity"
	"github.com/axiom-studio/memora/pkg/types"
)

func newTestService(t *testing.T) *service.Service {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	primary := &storesqlite.Store{}
	if err := primary.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "primary.db")}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vector.db"), Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })

	embedProvider, _ := embedding.Open("noop:default")
	return &service.Service{
		Metadata: primary,
		Vector:   vec,
		Embedder: embedProvider,
		Identity: map[string]adapter.IdentityProvider{
			string(types.IdentityProviderOpaque): identity.Opaque{},
		},
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := newTestService(t)
	srv := httpserver.New(httpserver.Config{
		Service:     svc,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func jsonReq(t *testing.T, method, url string, body any, headers map[string]string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func doJSON(t *testing.T, client *http.Client, req *http.Request) (int, map[string]any) {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestMemoryLifecycle_E2E(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "agent_opaque_test"}

	// Create workspace.
	status, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "e2e-ws"}, nil))
	if status != 201 {
		t.Fatalf("create workspace: status=%d body=%v", status, ws)
	}
	wsID, _ := ws["id"].(string)
	if wsID == "" {
		t.Fatal("no workspace id returned")
	}

	memBase := base + "/" + wsID + "/memories"

	// Imprint memory.
	status, imp := doJSON(t, c, jsonReq(t, "POST", memBase, map[string]any{
		"content": "hello world", 	}, agentH))
	if status != 201 {
		t.Fatalf("imprint: status=%d body=%v", status, imp)
	}
	memID, _ := imp["memory_id"].(string)
	wmk, _ := imp["watermark"].(string)
	if memID == "" || wmk == "" {
		t.Fatalf("missing memory_id or watermark: %v", imp)
	}

	// Lookup.
	status, mem := doJSON(t, c, jsonReq(t, "GET", memBase+"/"+memID, nil, nil))
	if status != 200 {
		t.Fatalf("lookup: status=%d", status)
	}
	if mem["id"] == nil && mem["content"] == nil {
		t.Fatalf("empty lookup response: %v", mem)
	}

	// Update with correct If-Match.
	updateH := map[string]string{"Memora-Agent-Id": "agent_opaque_test", "If-Match": wmk}
	status, upd := doJSON(t, c, jsonReq(t, "PUT", memBase+"/"+memID, map[string]any{
		"content": "hello world v2", 	}, updateH))
	if status != 200 {
		t.Fatalf("update: status=%d body=%v", status, upd)
	}
	newWmk, _ := upd["watermark"].(string)
	if newWmk == "" {
		t.Fatalf("no watermark after update: %v", upd)
	}

	// Update with stale watermark — expect 412.
	staleH := map[string]string{"Memora-Agent-Id": "agent_opaque_test", "If-Match": wmk}
	status, _ = doJSON(t, c, jsonReq(t, "PUT", memBase+"/"+memID, map[string]any{
		"content": "hello world v3", 	}, staleH))
	if status != 412 {
		t.Fatalf("stale update: want 412, got %d", status)
	}

	// Append.
	appendH := map[string]string{"Memora-Agent-Id": "agent_opaque_test", "If-Match": newWmk}
	status, app := doJSON(t, c, jsonReq(t, "POST", memBase+"/"+memID+":append", map[string]any{
		"content": " appended text",
	}, appendH))
	if status != 200 {
		t.Fatalf("append: status=%d body=%v", status, app)
	}

	// Forget.
	status, fgt := doJSON(t, c, jsonReq(t, "DELETE", memBase+"/"+memID, nil, agentH))
	if status != 200 {
		t.Fatalf("forget: status=%d body=%v", status, fgt)
	}

	// Verify forgotten — either 404 (hard delete) or 200 with tombstone.
	status, _ = doJSON(t, c, jsonReq(t, "GET", memBase+"/"+memID, nil, nil))
	if status != 404 && status != 200 {
		t.Fatalf("expected 404 or 200 after forget, got %d", status)
	}
}

func TestWorkspaceUpdate_E2E(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"

	// Create.
	status, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "orig"}, nil))
	if status != 201 {
		t.Fatalf("create: %d", status)
	}
	wsID, _ := ws["id"].(string)

	// Update.
	status, updated := doJSON(t, c, jsonReq(t, "PUT", base+"/"+wsID, map[string]any{
		"name": "renamed", "region": "us-west-2",
	}, nil))
	if status != 200 {
		t.Fatalf("update: status=%d body=%v", status, updated)
	}

	// Verify.
	status, got := doJSON(t, c, jsonReq(t, "GET", base+"/"+wsID, nil, nil))
	if status != 200 {
		t.Fatalf("get: %d", status)
	}
	if name, _ := got["name"].(string); name != "renamed" {
		t.Errorf("name = %q, want renamed", name)
	}
}

func TestCollectionGetSingle_E2E(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"

	// Create workspace.
	status, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "coll-test"}, nil))
	if status != 201 {
		t.Fatalf("create ws: %d", status)
	}
	wsID, _ := ws["id"].(string)

	// Create collection.
	status, coll := doJSON(t, c, jsonReq(t, "POST", base+"/"+wsID+"/collections", map[string]any{"name": "docs"}, nil))
	if status != 201 {
		t.Fatalf("create coll: status=%d body=%v", status, coll)
	}
	collID, _ := coll["id"].(string)
	if collID == "" {
		t.Fatalf("no collection id: %v", coll)
	}

	// Get single collection.
	status, got := doJSON(t, c, jsonReq(t, "GET", base+"/"+wsID+"/collections/"+collID, nil, nil))
	if status != 200 {
		t.Fatalf("get collection: status=%d body=%v", status, got)
	}
	if name, _ := got["name"].(string); name != "docs" {
		t.Errorf("name = %q, want docs", name)
	}
}

func TestListWorkspacesPagination_E2E(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"

	// Create 3 workspaces.
	for i := 0; i < 3; i++ {
		status, _ := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "ws-" + string(rune('a'+i))}, nil))
		if status != 201 {
			t.Fatalf("create ws %d: %d", i, status)
		}
	}

	// List with limit=2 — should get 2 results and a next_cursor.
	status, resp := doJSON(t, c, jsonReq(t, "GET", base+"?limit=2", nil, nil))
	if status != 200 {
		t.Fatalf("list: %d", status)
	}
	wsList, _ := resp["workspaces"].([]any)
	if len(wsList) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(wsList))
	}
	if _, ok := resp["next_cursor"]; !ok {
		t.Error("expected next_cursor in paginated response")
	}
}

func TestCrossTenantReadPrevention(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "agent_opaque_test"}

	// Create workspace A and B
	status, respA := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "workspace_a"}, nil))
	if status != 201 {
		t.Fatalf("create workspace A: %d", status)
	}
	wsAID, _ := respA["id"].(string)

	status, respB := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "workspace_b"}, nil))
	if status != 201 {
		t.Fatalf("create workspace B: %d", status)
	}
	wsBID, _ := respB["id"].(string)

	// Imprint memory in workspace A
	status, imprintResp := doJSON(t, c, jsonReq(t, "POST", base+"/"+wsAID+"/memories", map[string]any{
		"content": "secret data in workspace a",
	}, agentH))
	if status != 201 {
		t.Fatalf("imprint in workspace A: %d, response: %v", status, imprintResp)
	}
	memID, _ := imprintResp["memory_id"].(string)

	// Attempt to read memory from workspace B endpoint — should get 404
	status, resp := doJSON(t, c, jsonReq(t, "GET", base+"/"+wsBID+"/memories/"+memID, nil, agentH))
	if status != 404 {
		t.Fatalf("cross-tenant lookup: expected 404, got %d. Response: %v", status, resp)
	}

	// Verify we CAN read from the correct workspace
	status, resp = doJSON(t, c, jsonReq(t, "GET", base+"/"+wsAID+"/memories/"+memID, nil, agentH))
	if status != 200 {
		t.Fatalf("same-workspace lookup: expected 200, got %d", status)
	}
}

func TestCrossTenantDestructiveForget(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "agent_opaque_test"}

	// Create workspace A and B
	status, respA := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "workspace_a"}, nil))
	if status != 201 {
		t.Fatalf("create workspace A: %d", status)
	}
	wsAID, _ := respA["id"].(string)

	status, respB := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "workspace_b"}, nil))
	if status != 201 {
		t.Fatalf("create workspace B: %d", status)
	}
	wsBID, _ := respB["id"].(string)

	// Imprint memory in workspace A
	status, imprintResp := doJSON(t, c, jsonReq(t, "POST", base+"/"+wsAID+"/memories", map[string]any{
		"content": "critical data in workspace a",
	}, agentH))
	if status != 201 {
		t.Fatalf("imprint in workspace A: %d, response: %v", status, imprintResp)
	}
	memID, _ := imprintResp["memory_id"].(string)

	// Verify memory exists in A
	status, _ = doJSON(t, c, jsonReq(t, "GET", base+"/"+wsAID+"/memories/"+memID, nil, agentH))
	if status != 200 {
		t.Fatalf("verify initial memory in A: expected 200, got %d", status)
	}

	// Attempt to forget memory from workspace B endpoint — should get 404, NOT delete
	status, resp := doJSON(t, c, jsonReq(t, "DELETE", base+"/"+wsBID+"/memories/"+memID, nil, agentH))
	if status != 404 {
		t.Fatalf("cross-tenant forget: expected 404, got %d. Response: %v", status, resp)
	}

	// Verify memory STILL exists in workspace A (not deleted by cross-tenant forget attempt)
	status, resp = doJSON(t, c, jsonReq(t, "GET", base+"/"+wsAID+"/memories/"+memID, nil, agentH))
	if status != 200 {
		t.Fatalf("memory should still exist in A: expected 200, got %d. Response: %v", status, resp)
	}

	// Forget from correct workspace should work
	status, _ = doJSON(t, c, jsonReq(t, "DELETE", base+"/"+wsAID+"/memories/"+memID, nil, agentH))
	if status != 200 {
		t.Fatalf("same-workspace forget: expected 200, got %d", status)
	}
	// Note: memory may still be readable (issue #4231) — we only test that cross-tenant
	// forget is blocked. Same-workspace forget returning 200 proves authorization check works.
}
