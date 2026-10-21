package http_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/identity"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// meshNode is one real node in a federation test mesh.
type meshNode struct {
	name   string
	id     string
	ts     *httptest.Server
	svc    *service.Service
	delay  time.Duration // added before every peer-route answer
	mu     sync.Mutex
	paths  []string // X-Memora-Federation-Path values received
	feders []config.PeerConfig
	peers  *federation.Manager // the node's runtime peer registry
}

func (n *meshNode) receivedPaths() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.paths...)
}

// newMesh starts one node per name (SQLite, sqlite-vec, hash:<dim>
// embedder, dims differ per node). The first node calls every other node
// with its own key, and each other node holds only that key's hash.
// timeouts sets a peer's timeout_ms in the first node's config.
func newMesh(t *testing.T, names []string, timeouts map[string]int) []*meshNode {
	t.Helper()
	nodes := make([]*meshNode, len(names))
	for i, name := range names {
		n := &meshNode{name: name, id: types.NewID()}
		n.ts = httptest.NewUnstartedServer(nil)
		nodes[i] = n
	}
	caller := nodes[0]
	keys := map[string]string{}
	for _, n := range nodes[1:] {
		keys[n.name] = "key-" + caller.name + "-to-" + n.name + "-0123456789"
		caller.feders = append(caller.feders, config.PeerConfig{
			NodeID: n.id, Name: n.name, URL: "http://" + n.ts.Listener.Addr().String(),
			APIKey: keys[n.name], TimeoutMS: timeouts[n.name],
		})
		sum := sha256.Sum256([]byte(keys[n.name]))
		n.feders = []config.PeerConfig{{
			NodeID: caller.id, Name: caller.name, URL: "http://" + caller.ts.Listener.Addr().String(),
			InboundKeySHA256: hex.EncodeToString(sum[:]),
		}}
	}

	for i, n := range nodes {
		ctx := context.Background()
		dir := t.TempDir()
		dim := 64 * (i + 1)
		meta := &storesqlite.Store{}
		if err := meta.Open(ctx, adapter.MetadataConfig{DSN: filepath.Join(dir, "meta.db")}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = meta.Close() })
		vec := &sqlitevec.Store{}
		if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vec.db"), Dim: dim}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = vec.Close() })
		emb, err := embedding.Open(fmt.Sprintf("hash:%d", dim))
		if err != nil {
			t.Fatal(err)
		}
		n.svc = &service.Service{
			Metadata: meta, Vector: vec, Embedder: emb,
			Identity: map[string]adapter.IdentityProvider{string(types.IdentityProviderOpaque): identity.Opaque{}},
			NodeID:   n.id, NodeName: n.name,
		}
		// As at boot: config peers seed the store, which feeds the registry.
		reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true}, n.id)
		if err != nil {
			t.Fatal(err)
		}
		secret := make([]byte, 32)
		_, _ = rand.Read(secret)
		sealer, err := federation.NewSealer(secret)
		if err != nil {
			t.Fatal(err)
		}
		n.peers = federation.NewManager(reg, meta, sealer)
		if _, err := n.peers.Seed(ctx, n.feders); err != nil {
			t.Fatal(err)
		}
		if err := n.peers.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		if n.svc.Federation, err = federation.NewSearcher(reg); err != nil {
			t.Fatal(err)
		}
		h := httpserver.New(httpserver.Config{
			Service: n.svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			MaxBodyBytes: httpserver.DefaultMaxBodyBytes, Federation: reg, FederationPeers: n.peers,
		}).Handler()
		node := n
		n.ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if federation.IsPeerRoute(r.URL.Path) {
				node.mu.Lock()
				node.paths = append(node.paths, r.Header.Get(federation.HeaderFederationPath))
				node.mu.Unlock()
				select {
				case <-time.After(node.delay):
				case <-r.Context().Done():
					return
				}
			}
			h.ServeHTTP(w, r)
		})
		n.ts.Start()
		t.Cleanup(n.ts.Close)
	}
	return nodes
}

// seed imprints content into a fresh workspace on n and returns the
// memory ID.
func (n *meshNode) seed(t *testing.T, content string) string {
	t.Helper()
	c := n.ts.Client()
	_, ws := doJSON(t, c, jsonReq(t, "POST", n.ts.URL+"/v1/workspaces", map[string]any{"name": n.name}, nil))
	status, m := doJSON(t, c, jsonReq(t, "POST", n.ts.URL+"/v1/workspaces/"+ws["id"].(string)+"/memories",
		map[string]any{"content": content}, map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}))
	if status != 201 {
		t.Fatalf("seed %s: %d %v", n.name, status, m)
	}
	return m["id"].(string)
}

func (n *meshNode) search(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, n.ts.Client(), jsonReq(t, "POST", n.ts.URL+"/v1/search", body, nil))
}

// hitNodes maps memory_id → node_id over a search response.
func hitNodes(body map[string]any) map[string]string {
	out := map[string]string{}
	results, _ := body["results"].([]any)
	for _, r := range results {
		h := r.(map[string]any)
		node, _ := h["node_id"].(string)
		out[h["memory_id"].(string)] = node
	}
	return out
}

// TestFederatedSearch_E2E_PeersContribute: one federated search on A
// returns A's, B's and C's memories, each attributed to its node, with
// every peer's latency, and each peer saw the path "A".
func TestFederatedSearch_E2E_PeersContribute(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b", "c"}, nil)
	a, b, c := nodes[0], nodes[1], nodes[2]
	memA, memB, memC := a.seed(t, "aurora borealis over tromso"), b.seed(t, "aurora sighting log"), c.seed(t, "aurora camera settings")

	for _, mode := range []string{"keyword", "vector", "hybrid"} {
		status, body := a.search(t, map[string]any{"query": "aurora", "mode": mode, "k": 10, "federated": true})
		got := hitNodes(body)
		if status != 200 || got[memA] != a.id || got[memB] != b.id || got[memC] != c.id {
			t.Fatalf("%s: status=%d hits=%v body=%v\nwant %s@a %s@b %s@c", mode, status, got, body, memA, memB, memC)
		}
		if body["partial_success"] == true {
			t.Fatalf("%s: partial_success with every peer up: %v", mode, body["failed_peers"])
		}
		lat, _ := body["peer_latency_ms"].(map[string]any)
		if _, ok := lat[a.id]; !ok {
			t.Fatalf("%s: peer_latency_ms = %v, want an entry for a (this node)", mode, lat)
		}
		for _, r := range body["results"].([]any) {
			h := r.(map[string]any)
			if h["node_id"] != a.id && h["via"] != "federation:"+h["node_id"].(string) {
				t.Fatalf("%s: peer hit via = %v, want federation:<node_id>", mode, h["via"])
			}
		}
		if _, ok := lat[b.id]; !ok {
			t.Fatalf("%s: peer_latency_ms = %v, want an entry for b", mode, lat)
		}
		if _, ok := lat[c.id]; !ok {
			t.Fatalf("%s: peer_latency_ms = %v, want an entry for c", mode, lat)
		}
	}
	for _, peer := range []*meshNode{b, c} {
		for _, p := range peer.receivedPaths() {
			if p != a.id {
				t.Fatalf("%s received X-Memora-Federation-Path %q, want a's node ID %s", peer.name, p, a.id)
			}
		}
	}

	// rerank: true goes through the default (no-op) re-scorer: same hits.
	status, body := a.search(t, map[string]any{"query": "aurora", "mode": "keyword", "k": 10, "federated": true, "rerank": true})
	if got := hitNodes(body); status != 200 || len(got) != 3 {
		t.Fatalf("rerank: status=%d hits=%v, want the three memories", status, got)
	}

	// Without federated: true, A answers alone.
	_, body = a.search(t, map[string]any{"query": "aurora", "mode": "keyword"})
	if got, ok := hitNodes(body)[memA]; !ok || len(hitNodes(body)) != 1 || got != "" {
		t.Fatalf("local search hits = %v, want only a's memory, unattributed", hitNodes(body))
	}
}

// TestFederatedSearch_E2E_SlowPeerFails: a peer slower than timeout_ms
// is reported in failed_peers while the others' hits return, and the
// search does not wait for it.
func TestFederatedSearch_E2E_SlowPeerFails(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b", "c"}, nil)
	a, b, c := nodes[0], nodes[1], nodes[2]
	memB := b.seed(t, "glacier survey")
	c.seed(t, "glacier melt rate")
	c.delay = 1500 * time.Millisecond

	start := time.Now()
	status, body := a.search(t, map[string]any{"query": "glacier", "mode": "keyword", "federated": true, "timeout_ms": 300})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("search took %s, want it bounded by timeout_ms", elapsed)
	}
	got := hitNodes(body)
	failed, _ := body["failed_peers"].([]any)
	if status != 200 || got[memB] != b.id || len(got) != 1 {
		t.Fatalf("status=%d hits=%v, want only b's memory", status, got)
	}
	if body["partial_success"] != true || len(failed) != 1 {
		t.Fatalf("partial_success=%v failed_peers=%v, want true and one failed peer", body["partial_success"], failed)
	}
	if fp := failed[0].(map[string]any); fp["node_id"] != c.id || fp["name"] != "c" || fp["error_code"] != "timeout" {
		t.Fatalf("failed peer = %v, want c (%s) with error_code timeout", fp, c.id)
	}
}

// TestFederatedSearch_E2E_PeersRestrict: peers:[b] asks only b.
func TestFederatedSearch_E2E_PeersRestrict(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b", "c"}, nil)
	a, b, c := nodes[0], nodes[1], nodes[2]
	memB := b.seed(t, "harbor tide table")
	c.seed(t, "harbor dredging plan")

	status, body := a.search(t, map[string]any{"query": "harbor", "mode": "keyword", "federated": true, "peers": []string{b.id}})
	got := hitNodes(body)
	if status != 200 || len(got) != 1 || got[memB] != b.id {
		t.Fatalf("status=%d hits=%v, want only b's memory", status, got)
	}
	if n := len(c.receivedPaths()); n != 0 {
		t.Fatalf("c was called %d times, want 0", n)
	}
}

// TestFederatedSearch_E2E_PerPeerTimeout: a peer's own timeout_ms applies
// when the request sets none, and a request timeout_ms overrides it.
func TestFederatedSearch_E2E_PerPeerTimeout(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, map[string]int{"b": 150})
	a, b := nodes[0], nodes[1]
	memB := b.seed(t, "orchard frost alarm")
	b.delay = 600 * time.Millisecond

	_, body := a.search(t, map[string]any{"query": "orchard", "mode": "keyword", "federated": true})
	if failed, _ := body["failed_peers"].([]any); len(failed) != 1 || failed[0].(map[string]any)["name"] != "b" {
		t.Fatalf("with b's 150 ms timeout: failed_peers=%v, want [b]", body["failed_peers"])
	}
	_, body = a.search(t, map[string]any{"query": "orchard", "mode": "keyword", "federated": true, "timeout_ms": 3000})
	if got := hitNodes(body); got[memB] != b.id || body["partial_success"] == true {
		t.Fatalf("with timeout_ms 3000: hits=%v partial=%v, want b's memory", got, body["partial_success"])
	}
}

// TestFederatedSearch_E2E_Validation: malformed federated requests are
// 400, and a multi-tenant node refuses federated search.
func TestFederatedSearch_E2E_Validation(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a := nodes[0]
	cases := []map[string]any{
		{"query": "x", "federated": true, "peers": []string{"node_1"}},
		{"query": "x", "federated": true, "peers": []string{types.NewID()}},
		{"query": "x", "peers": []string{nodes[1].id}},
		{"query": "x", "federated": true, "workspace_ids": []string{types.NewID()}},
		{"query": "x", "federated": true, "timeout_ms": -1},
	}
	for _, body := range cases {
		if status, resp := a.search(t, body); status != 400 || resp["error_code"] != "invalid_input" {
			t.Fatalf("%v: status=%d body=%v, want 400 invalid_input", body, status, resp)
		}
	}

	solo := newTestServer(t)
	status, resp := doJSON(t, solo.Client(), jsonReq(t, "POST", solo.URL+"/v1/search", map[string]any{"query": "x", "federated": true}, nil))
	if status != 400 || resp["error_code"] != "invalid_input" {
		t.Fatalf("federation off: status=%d body=%v, want 400", status, resp)
	}

	mt := httptest.NewServer(httpserver.New(httpserver.Config{
		Service: a.svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes, Mode: "multi-tenant",
	}).Handler())
	defer mt.Close()
	status, resp = doJSON(t, mt.Client(), jsonReq(t, "POST", mt.URL+"/v1/search", map[string]any{"query": "x", "federated": true},
		map[string]string{"X-Memora-Workspace": types.NewID()}))
	if status != 403 || resp["error_code"] != "federation_multi_tenant" {
		t.Fatalf("multi-tenant: status=%d body=%v, want 403 federation_multi_tenant", status, resp)
	}
}

// reverseReranker is a Reranker that reverses the fused order, so the
// test can see that rerank: true routes through the node's re-scorer.
type reverseReranker struct{ err error }

func (r reverseReranker) Rerank(_ context.Context, _ string, hits []api.RecallHit) ([]api.RecallHit, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make([]api.RecallHit, len(hits))
	for i, h := range hits {
		out[len(hits)-1-i] = h
	}
	return out, nil
}

// TestFederatedSearch_E2E_RerankHook: with rerank: true the node's
// Reranker reorders the merged hits; a failing Reranker keeps the fused
// order instead of failing the search.
func TestFederatedSearch_E2E_RerankHook(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	a.seed(t, "comet tail photo")
	b.seed(t, "comet orbit notes")

	order := func(body map[string]any) []string {
		var ids []string
		for _, r := range body["results"].([]any) {
			ids = append(ids, r.(map[string]any)["memory_id"].(string))
		}
		return ids
	}
	req := map[string]any{"query": "comet", "mode": "keyword", "k": 10, "federated": true}
	_, fused := a.search(t, req)
	req["rerank"] = true
	a.svc.Reranker = reverseReranker{}
	_, reranked := a.search(t, req)
	f, r := order(fused), order(reranked)
	if len(f) != 2 || len(r) != 2 || f[0] != r[1] || f[1] != r[0] {
		t.Fatalf("fused %v, reranked %v: want the reverse order", f, r)
	}
	a.svc.Reranker = reverseReranker{err: fmt.Errorf("scorer down")}
	status, failed := a.search(t, req)
	if status != 200 || fmt.Sprint(order(failed)) != fmt.Sprint(f) {
		t.Fatalf("failing reranker: status=%d order=%v, want 200 and the fused order %v", status, order(failed), f)
	}
}

func (n *meshNode) get(t *testing.T, path string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	return doJSON(t, n.ts.Client(), jsonReq(t, "GET", n.ts.URL+path, nil, headers))
}

// TestFetchMemory_E2E (F-6): GET /v1/memories/{id} serves this node's
// memories node-wide and proxies ?node=<peer> to the owning peer with
// that peer's key; failures map to 404 unknown_node, 404 not_found and
// 502; the peer route itself refuses unauthenticated callers.
func TestFetchMemory_E2E(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	memA := a.seed(t, "local lighthouse log\n\nsecond paragraph")
	memB := b.seed(t, "remote tide gauge")

	for _, path := range []string{"/v1/memories/" + memA, "/v1/memories/" + memA + "?node=" + a.id} {
		status, env := a.get(t, path, nil)
		cells, _ := env["cells"].([]any)
		if status != 200 || env["id"] != memA || env["content"] != "local lighthouse log\n\nsecond paragraph" ||
			env["node_id"] != a.id || env["node_name"] != "a" || env["workspace_id"] == "" || len(cells) == 0 {
			t.Fatalf("GET %s: %d %v, want a's memory with cells, attributed to a", path, status, env)
		}
	}

	before := len(b.receivedPaths())
	status, env := a.get(t, "/v1/memories/"+memB+"?node="+b.id, nil)
	if status != 200 || env["id"] != memB || env["content"] != "remote tide gauge" || env["node_id"] != b.id || env["node_name"] != "b" {
		t.Fatalf("remote fetch: %d %v, want b's memory attributed to b", status, env)
	}
	if paths := b.receivedPaths(); len(paths) != before+1 || paths[len(paths)-1] != a.id {
		t.Fatalf("b received paths %v, want one more with a's node ID", paths)
	}

	if status, env := a.get(t, "/v1/memories/"+types.NewID()+"?node="+b.id, nil); status != 404 || env["error_code"] != "not_found" {
		t.Fatalf("memory not on b: %d %v, want 404 not_found", status, env)
	}
	if status, env := a.get(t, "/v1/memories/"+memB, nil); status != 404 || env["error_code"] != "not_found" {
		t.Fatalf("b's memory without node: %d %v, want 404 (not on a)", status, env)
	}
	if status, env := a.get(t, "/v1/memories/"+memB+"?node="+types.NewID(), nil); status != 404 || env["error_code"] != "unknown_node" {
		t.Fatalf("unknown node: %d %v, want 404 unknown_node", status, env)
	}
	if status, env := a.get(t, "/v1/memories/"+memB+"?node=node_1", nil); status != 400 || env["error_code"] != "invalid_input" {
		t.Fatalf("malformed node: %d %v, want 400", status, env)
	}

	// A forgotten memory is gone from the owning node's fetch too.
	m, err := b.svc.Metadata.GetMemory(context.Background(), memB)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.Metadata.ForgetMemory(context.Background(), m.WorkspaceID, memB); err != nil {
		t.Fatal(err)
	}
	if status, env := a.get(t, "/v1/memories/"+memB+"?node="+b.id, nil); status != 404 {
		t.Fatalf("forgotten on b: %d %v, want 404", status, env)
	}

	// The peer route needs b's key for a: none, or a wrong one, is 401.
	for _, h := range []map[string]string{
		nil,
		{federation.HeaderLocalInstanceID: a.id, federation.HeaderFederationAuth: "Bearer wrong"},
	} {
		if status, env := b.get(t, "/v1/federation/memories/"+memB, h); status != 401 || env["error_code"] != "federation_unauthorized" {
			t.Fatalf("direct peer route with %v: %d %v, want 401", h, status, env)
		}
	}

	// b goes down: the fetch is a 502 naming why.
	b.ts.Close()
	if status, env := a.get(t, "/v1/memories/"+types.NewID()+"?node="+b.id, nil); status != 502 || env["error_code"] != "unreachable" {
		t.Fatalf("peer down: %d %v, want 502 unreachable", status, env)
	}
}

// TestFetchMemory_E2E_MultiTenant: a tenant fetches only its own
// memories and never from a peer.
func TestFetchMemory_E2E_MultiTenant(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a := nodes[0]
	memA := a.seed(t, "tenant memory")
	m, err := a.svc.Metadata.GetMemory(context.Background(), memA)
	if err != nil {
		t.Fatal(err)
	}
	mt := httptest.NewServer(httpserver.New(httpserver.Config{
		Service: a.svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes, Mode: "multi-tenant",
	}).Handler())
	defer mt.Close()
	get := func(path string, tenant string) (int, map[string]any) {
		h := map[string]string{}
		if tenant != "" {
			h["X-Memora-Workspace"] = tenant
		}
		return doJSON(t, mt.Client(), jsonReq(t, "GET", mt.URL+path, nil, h))
	}
	if status, env := get("/v1/memories/"+memA, m.WorkspaceID); status != 200 || env["id"] != memA {
		t.Fatalf("own tenant: %d %v, want 200", status, env)
	}
	if status, _ := get("/v1/memories/"+memA, types.NewID()); status != 404 {
		t.Fatalf("other tenant: %d, want 404", status)
	}
	if status, env := get("/v1/memories/"+memA, ""); status != 400 || env["error_code"] != "missing_workspace" {
		t.Fatalf("no tenant: %d %v, want 400 missing_workspace", status, env)
	}
	if status, env := get("/v1/memories/"+memA+"?node="+nodes[1].id, m.WorkspaceID); status != 403 || env["error_code"] != "federation_multi_tenant" {
		t.Fatalf("remote in multi-tenant: %d %v, want 403", status, env)
	}
}
