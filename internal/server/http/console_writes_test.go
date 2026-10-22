package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	httpserver "github.com/axiom-studio/memora/internal/server/http"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestConsoleWrites_ThroughServer (#6183): a logged-in console session
// writes memories and edges through the real HTTP server stack, without
// any Memora-Agent-Id header, and each write is attributed to the
// console agent. The REST routes still require the header.
func TestConsoleWrites_ThroughServer(t *testing.T) {
	const apiKey = "console-test-key"
	svc := newTestService(t)
	// The console links and unlinks through the graph store, which shares
	// the metadata database.
	dir := t.TempDir()
	meta := &storesqlite.Store{}
	if err := meta.Open(context.Background(), adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "m.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	graph := &storesqlite.GraphStore{}
	if err := graph.Open(context.Background(), adapter.GraphConfig{Driver: "sqlite_graph", DSN: filepath.Join(dir, "m.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	svc.Metadata, svc.Graph = meta, graph
	ts := httptest.NewServer(httpserver.New(httpserver.Config{
		Service: svc, APIKey: apiKey, UICookieSecure: "auto",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
	}).Handler())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	console := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		resp, err := console.PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if status, body := post("/ui/login", url.Values{"api_key": {apiKey}}); status >= 400 {
		t.Fatalf("login: %d %s", status, body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + apiKey}
	api := func(method, path string, body any, headers map[string]string) (int, map[string]any) {
		t.Helper()
		h := map[string]string{}
		for k, v := range bearer {
			h[k] = v
		}
		for k, v := range headers {
			h[k] = v
		}
		return doJSON(t, ts.Client(), jsonReq(t, method, ts.URL+path, body, h))
	}
	_, ws := api("POST", "/v1/workspaces", map[string]any{"name": "console"}, nil)
	wsID := ws["id"].(string)
	memories := func() []map[string]any {
		t.Helper()
		_, list := api("GET", "/v1/workspaces/"+wsID+"/memories", nil, nil)
		var out []map[string]any
		for _, m := range list["memories"].([]any) {
			out = append(out, m.(map[string]any))
		}
		return out
	}
	ok := func(op string, status int, body string) {
		t.Helper()
		if status >= 400 || strings.Contains(body, "missing_agent_id") || strings.Contains(body, "form-error") {
			t.Fatalf("console %s: %d %s", op, status, body)
		}
	}

	for _, c := range []string{"first console memory", "second console memory"} {
		status, body := post("/ui/api/memories/imprint", url.Values{"workspace_id": {wsID}, "content": {c}})
		ok("imprint", status, body)
	}
	mems := memories()
	if len(mems) != 2 {
		t.Fatalf("%d memories after two console imprints", len(mems))
	}
	for _, m := range mems {
		if m["written_by_agent_id"] != types.ConsoleAgentID {
			t.Fatalf("imprinted by %v, want the console agent", m["written_by_agent_id"])
		}
	}
	id, other := mems[0]["id"].(string), mems[1]["id"].(string)
	head := func() string {
		t.Helper()
		_, m := api("GET", "/v1/workspaces/"+wsID+"/memories/"+id, nil, nil)
		return m["head_watermark"].(string)
	}
	status, body := post("/ui/api/memories/update", url.Values{"workspace_id": {wsID}, "memory_id": {id}, "content": {"updated by the console"}, "expected_watermark": {head()}})
	ok("update", status, body)
	status, body = post("/ui/api/memories/patch", url.Values{"workspace_id": {wsID}, "memory_id": {id}, "old_string": {"updated"}, "new_string": {"patched"}, "expected_watermark": {head()}})
	ok("patch", status, body)
	status, body = post("/ui/api/memories/append", url.Values{"workspace_id": {wsID}, "memory_id": {id}, "content": {" and appended"}, "expected_watermark": {head()}})
	ok("append", status, body)
	_, m := api("GET", "/v1/workspaces/"+wsID+"/memories/"+id, nil, nil)
	if !strings.Contains(m["content"].(string), "patched by the console") || !strings.Contains(m["content"].(string), "appended") ||
		m["last_modified_by_agent_id"] != types.ConsoleAgentID {
		t.Fatalf("after console edits: %v", m)
	}

	status, body = post("/ui/api/edges/link", url.Values{"workspace_id": {wsID}, "source_memory_id": {id}, "target_memory_id": {other}, "edge_type": {"references"}})
	ok("link", status, body)
	_, edges := api("GET", "/v1/workspaces/"+wsID+"/memories/"+id+"/edges", nil, nil)
	list, _ := edges["edges"].([]any)
	if len(list) != 1 {
		t.Fatalf("edges after console link: %v", edges)
	}
	edgeID := list[0].(map[string]any)["edge_id"].(string)
	status, body = post("/ui/api/edges/unlink", url.Values{"workspace_id": {wsID}, "edge_id": {edgeID}, "confirm": {edgeID}})
	ok("unlink", status, body)
	_, edges = api("GET", "/v1/workspaces/"+wsID+"/memories/"+id+"/edges", nil, nil)
	if left, _ := edges["edges"].([]any); len(left) != 0 {
		t.Fatalf("edge still there after console unlink: %v", edges)
	}

	status, body = post("/ui/api/memories/forget", url.Values{"workspace_id": {wsID}, "memory_id": {other}, "confirm": {other}})
	ok("forget", status, body)
	if len(memories()) != 1 {
		t.Fatal("console forget left the memory")
	}

	// The REST API still requires the agent header on writes.
	if status, body := api("POST", "/v1/workspaces/"+wsID+"/memories", map[string]any{"content": "x"}, nil); status != 400 || body["error_code"] != "missing_agent_id" {
		t.Fatalf("REST imprint without agent: %d %v, want 400 missing_agent_id", status, body)
	}
}
