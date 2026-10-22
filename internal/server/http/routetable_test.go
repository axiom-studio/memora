package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	ledgersqlite "github.com/axiom-studio/memora/internal/ledger/sqlite"
	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/identity"
	"github.com/axiom-studio/memora/pkg/types"
)

// baseBodies are the smallest valid bodies, keyed "METHOD pattern", that
// the rejection test starts from before planting one bad ID. A route
// whose body needs more than its ID fields must be listed here.
func baseBodies(memID, mem2ID string) map[string]map[string]any {
	return map[string]map[string]any{
		"POST /v1/search":                                                {"query": "x"},
		"POST /v1/federation/peers":                                      {"name": "x", "url": "https://x.example.com"},
		"POST /v1/workspaces":                                            {"name": "x"},
		"PUT /v1/workspaces/{workspace_id}":                              {"name": "x"},
		"POST /v1/workspaces/{workspace_id}/collections":                 {"name": "x"},
		"POST /v1/workspaces/{workspace_id}/memories":                    {"content": "x"},
		"PUT /v1/workspaces/{workspace_id}/memories/{memory_id}":         {"content": "x"},
		"POST /v1/workspaces/{workspace_id}/memories/{memory_id}:append": {"content": "x"},
		"POST /v1/workspaces/{workspace_id}/memories/{memory_id}/edges":  {"edge_type": "references", "target_memory_id": mem2ID},
		"POST /v1/workspaces/{workspace_id}/edges/:batch":                {"edges": []any{map[string]any{"source_memory_id": memID, "target_memory_id": mem2ID, "edge_type": "references"}}},
		"POST /v1/workspaces/{workspace_id}/graph/neighbors":             {"memory_id": memID},
		"POST /v1/workspaces/{workspace_id}/graph/traverse":              {"seed_memory_id": memID},
		"POST /v1/workspaces/{workspace_id}/agents":                      {"agent_id": types.NewID()},
		"POST /v1/workspaces/{workspace_id}/recall":                      {"query": "x"},
		"POST /v1/workspaces/{workspace_id}/recall/pins":                 {"query": "x"},
	}
}

// idFieldPaths lists the ID fields of a request type, by JSON path:
// "collection_id", "filters.agent_id", "edges[].source_memory_id".
func idFieldPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			out = append(out, idFieldPaths(f.Type, prefix)...)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if isID, _ := types.IDField(name); isID {
			out = append(out, prefix+name)
			continue
		}
		ft := f.Type
		switch {
		case ft.Kind() == reflect.Struct:
			out = append(out, idFieldPaths(ft, prefix+name+".")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			out = append(out, idFieldPaths(ft.Elem(), prefix+name+"[].")...)
		}
	}
	return out
}

// plant sets the field at path in body (copied) to bad, creating
// objects along the way; "[]" addresses the first list element.
func plant(body map[string]any, path string, bad any) map[string]any {
	raw, _ := json.Marshal(body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	cur := out
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		if name, ok := strings.CutSuffix(p, "[]"); ok {
			list, _ := cur[name].([]any)
			if len(list) == 0 {
				list = []any{map[string]any{}}
			}
			cur[name] = list
			cur = list[0].(map[string]any)
			continue
		}
		next, _ := cur[p].(map[string]any)
		if next == nil {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = bad
	return out
}

// The route test server's one peer, whose credentials the walk presents
// on peer routes so they reach their ID checks.
const routeTestPeerKey = "route-test-peer-key-0123456789"

var routeTestPeer = types.NewID()

func routeTestServer(t *testing.T) (*httptest.Server, string, string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "m.db")
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "v.db"), Dim: 384}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	graph := &storesqlite.GraphStore{}
	if err := graph.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	led := &ledgersqlite.Store{}
	if err := led.Open(ctx, adapter.LedgerConfig{DSN: filepath.Join(dir, "l.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = led.Close() })
	emb, _ := embedding.Open("hash:default")
	self := types.NewID()
	sum := sha256.Sum256([]byte(routeTestPeerKey))
	reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true, PrivatePeerHosts: []string{"127.0.0.1"}, Peers: []config.PeerConfig{{
		NodeID: routeTestPeer, Name: "peer", URL: "http://127.0.0.1:1", InboundKeySHA256: hex.EncodeToString(sum[:]),
	}}}, self)
	if err != nil {
		t.Fatal(err)
	}
	fed, err := federation.NewSearcher(reg)
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.Service{
		Metadata: meta, Vector: vec, Graph: graph, Ledger: led, Embedder: emb, Federation: fed,
		Identity: map[string]adapter.IdentityProvider{string(types.IdentityProviderOpaque): identity.Opaque{}},
		NodeID:   self,
	}
	ws := &types.Workspace{Name: "routes"}
	if err := meta.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	agent := "2b4224a8-95b4-4171-9e6b-689847d53ac7"
	var mems []string
	for _, c := range []string{"one", "two"} {
		m := &types.Memory{WorkspaceID: ws.ID, Content: c, WrittenByAgentID: agent}
		if _, err := meta.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
		mems = append(mems, m.ID)
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	sealer, err := federation.NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	peers := federation.NewManager(reg, meta, sealer)
	if _, err := peers.Seed(ctx, []config.PeerConfig{{NodeID: routeTestPeer, Name: "peer", URL: "http://127.0.0.1:1", InboundKeySHA256: hex.EncodeToString(sum[:])}}); err != nil {
		t.Fatal(err)
	}
	if err := peers.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Service: svc, Logger: testLogger(), MaxBodyBytes: DefaultMaxBodyBytes, Federation: reg, FederationPeers: peers})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, ws.ID, mems[0], mems[1]
}

// TestRouteTable_RejectsNonUUIDIDs (ID-9) is driven by routeTable, which
// the middleware enforces, so every reachable /v1 route is covered. For
// each route and method it plants a legacy prefixed ID, an uppercase UUID
// and garbage into each ID path parameter, each ID field of the declared
// request body (found by name, types.IDField) and each ID query
// parameter, and expects 400 invalid_input naming that field.
func TestRouteTable_RejectsNonUUIDIDs(t *testing.T) {
	ts, wsID, memID, mem2ID := routeTestServer(t)
	valid := map[string]string{"workspace_id": wsID, "memory_id": memID}
	bases := baseBodies(memID, mem2ID)
	bads := []string{"mem_01HXYZABCDEFGHJKMNPQRSTVWX", strings.ToUpper(types.NewID()), "not-an-id"}
	headers := map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7", "Content-Type": "application/json"}

	call := func(t *testing.T, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if federation.IsPeerRoute(path) {
			req.Header.Set(federation.HeaderLocalInstanceID, routeTestPeer)
			req.Header.Set(federation.HeaderFederationAuth, "Bearer "+routeTestPeerKey)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	expect := func(t *testing.T, status int, body map[string]any, field string) {
		t.Helper()
		leaf := field[strings.LastIndexAny(field, ".]")+1:]
		if status != 400 || body["error_code"] != "invalid_input" {
			t.Fatalf("status=%d body=%v, want 400 invalid_input", status, body)
		}
		if msg, _ := body["message"].(string); !strings.Contains(msg, leaf+" must be a UUID") {
			t.Fatalf("message %q does not name %s", msg, leaf)
		}
	}
	// fill renders pattern with valid IDs, and bad in place of target.
	fill := func(pattern, target, bad string) string {
		var b strings.Builder
		for i := 0; i < len(pattern); i++ {
			if pattern[i] != '{' {
				b.WriteByte(pattern[i])
				continue
			}
			end := strings.IndexByte(pattern[i:], '}') + i
			name := pattern[i+1 : end]
			switch {
			case name == target:
				b.WriteString(bad)
			case valid[name] != "":
				b.WriteString(valid[name])
			case isIDParam(name):
				b.WriteString(types.NewID())
			default:
				b.WriteString("k")
			}
			i = end
		}
		return b.String()
	}

	cases := 0
	for _, rt := range routeTable {
		var pathParams []string
		for _, seg := range strings.Split(rt.Pattern, "/") {
			for _, part := range strings.FieldsFunc(seg, func(c rune) bool { return c == '{' || c == '}' || c == '@' || c == ':' }) {
				if strings.Contains(seg, "{"+part+"}") && isIDParam(part) {
					pathParams = append(pathParams, part)
				}
			}
		}
		for _, method := range rt.Methods {
			key := method + " " + rt.Pattern
			base := bases[key]
			for _, p := range pathParams {
				for _, bad := range bads {
					t.Run(fmt.Sprintf("%s path %s=%s", key, p, bad), func(t *testing.T) {
						status, body := call(t, method, fill(rt.Pattern, p, bad), base)
						expect(t, status, body, p)
					})
					cases++
				}
			}
			if typ, ok := rt.Bodies[method]; ok {
				for _, f := range idFieldPaths(reflect.TypeOf(typ), "") {
					_, isList := types.IDField(f[strings.LastIndexAny(f, ".]")+1:])
					for _, bad := range bads {
						var v any = bad
						if isList {
							v = []string{bad}
						}
						body := plant(base, f, v)
						if f == "peers" {
							body["federated"] = true
						}
						t.Run(fmt.Sprintf("%s body %s=%s", key, f, bad), func(t *testing.T) {
							status, resp := call(t, method, fill(rt.Pattern, "", ""), body)
							expect(t, status, resp, f)
						})
						cases++
					}
				}
			}
			if method == "GET" {
				for _, q := range rt.QueryIDs {
					for _, bad := range bads {
						t.Run(fmt.Sprintf("%s query %s=%s", key, q, bad), func(t *testing.T) {
							status, body := call(t, method, fill(rt.Pattern, "", "")+"?"+q+"="+bad, nil)
							expect(t, status, body, q)
						})
						cases++
					}
				}
			}
		}
	}
	if cases < 100 {
		t.Fatalf("only %d cases generated; the route table walk is broken", cases)
	}
}

// TestRouteTable_Enforced: a /v1 path outside the table is 404 and a
// method the table does not list is 405 with Allow.
func TestRouteTable_Enforced(t *testing.T) {
	ts, wsID, _, _ := routeTestServer(t)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/v1/nothing", 404},
		{"GET", "/v1/workspaces/" + wsID + "/unknown", 404},
		{"GET", "/v1/workspaces/" + wsID + "/memories/" + types.NewID() + "/unknown", 404},
		{"PATCH", "/v1/workspaces", 405},
		{"GET", "/v1/workspaces/" + wsID + "/recall", 405},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
		if tc.want == 405 && resp.Header.Get("Allow") == "" {
			t.Errorf("%s %s: 405 without Allow", tc.method, tc.path)
		}
	}
}
