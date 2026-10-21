package http_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	ledgersqlite "github.com/axiom-studio/memora/internal/ledger/sqlite"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/identity"
	"github.com/axiom-studio/memora/pkg/types"
)

const fedPeerKey = "federation-search-peer-key-0123"

type fedNode struct {
	ts     *httptest.Server
	svc    *service.Service
	nodeID string
	peerID string // the node ID the test calls in as
}

type fedNodeOpts struct {
	dim        int     // embedder and vector store dimension
	inboundQPS float64 // 0 = default
	mode       string
}

// newFedNode starts a real node (SQLite metadata, sqlite-vec, SQLite
// ledger, hash embedder of the given dimension) with federation on and
// one peer, which the test plays by sending that peer's credentials.
func newFedNode(t *testing.T, o fedNodeOpts) *fedNode {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "meta.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vec.db"), Dim: o.dim}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	led := &ledgersqlite.Store{}
	if err := led.Open(ctx, adapter.LedgerConfig{Driver: "sqlite", DSN: filepath.Join(dir, "ledger.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = led.Close() })
	emb, err := embedding.Open(fmt.Sprintf("hash:%d", o.dim))
	if err != nil {
		t.Fatal(err)
	}

	n := &fedNode{nodeID: types.NewID(), peerID: types.NewID()}
	n.svc = &service.Service{
		Metadata: meta, Vector: vec, Ledger: led, Embedder: emb,
		Identity: map[string]adapter.IdentityProvider{string(types.IdentityProviderOpaque): identity.Opaque{}},
		NodeID:   n.nodeID, NodeName: "callee",
	}
	sum := sha256.Sum256([]byte(fedPeerKey))
	reg, err := federation.NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{{
			NodeID: n.peerID, Name: "caller", URL: "https://caller.example.com",
			InboundKeySHA256: hex.EncodeToString(sum[:]),
		}},
	}, n.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httpserver.New(httpserver.Config{
		Service:              n.svc,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes:         httpserver.DefaultMaxBodyBytes,
		Mode:                 o.mode,
		Federation:           reg,
		FederationInboundQPS: o.inboundQPS,
	})
	n.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(n.ts.Close)
	return n
}

// peerSearch calls POST /v1/federation/search as the configured peer.
func (n *fedNode) peerSearch(t *testing.T, body map[string]any, path string) (*http.Response, map[string]any) {
	t.Helper()
	h := map[string]string{
		federation.HeaderLocalInstanceID: n.peerID,
		federation.HeaderFederationAuth:  "Bearer " + fedPeerKey,
	}
	if path != "" {
		h[federation.HeaderFederationPath] = path
	}
	req := jsonReq(t, http.MethodPost, n.ts.URL+"/v1/federation/search", body, h)
	resp, err := n.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = jsonDecode(resp.Body, &m)
	return resp, m
}

func (n *fedNode) rejections(t *testing.T) []string {
	t.Helper()
	entries, _, err := n.svc.Ledger.Query(context.Background(), adapter.LedgerQuery{Op: []string{"federation_rejected"}})
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range entries {
		if e.AgentID != n.peerID {
			t.Errorf("rejection entry agent_id = %q, want the peer %s", e.AgentID, n.peerID)
		}
		reasons = append(reasons, fmt.Sprint(e.Metadata["reason"]))
	}
	return reasons
}

// TestFederationSearch_E2E: an authenticated peer searches the whole
// node and gets hits from both workspaces, attributed to this node. The
// node runs a 128-dimension embedder, so vector and hybrid modes prove
// the text query is embedded here, not by the caller.
func TestFederationSearch_E2E(t *testing.T) {
	n := newFedNode(t, fedNodeOpts{dim: 128})
	wsA, wsB := seedTwoWorkspaces(t, n.ts, "telescope calibration log")

	for _, mode := range []string{"keyword", "vector", "hybrid"} {
		resp, body := n.peerSearch(t, map[string]any{"query": "telescope calibration", "mode": mode, "k": 5, "include_content": true}, "")
		got := resultWorkspaces(t, body)
		if resp.StatusCode != 200 || !got[wsA] || !got[wsB] {
			t.Fatalf("%s: status=%d workspaces=%v body=%v, want 200 with %s and %s", mode, resp.StatusCode, got, body, wsA, wsB)
		}
		for _, r := range body["results"].([]any) {
			hit := r.(map[string]any)
			if hit["node_id"] != n.nodeID || hit["node_name"] != "callee" {
				t.Fatalf("%s: hit node = %v/%v, want %s/callee", mode, hit["node_id"], hit["node_name"], n.nodeID)
			}
			if hit["content"] != "telescope calibration log" {
				t.Fatalf("%s: content = %v, want the memory content", mode, hit["content"])
			}
		}
	}

	// A peer cannot scope or widen the search: workspace fields are refused.
	resp, body := n.peerSearch(t, map[string]any{"query": "telescope", "workspace_ids": []string{wsA}}, "")
	if resp.StatusCode != 400 || body["error_code"] != "invalid_input" {
		t.Fatalf("workspace_ids: status=%d body=%v, want 400 invalid_input", resp.StatusCode, body)
	}
	if got := n.rejections(t); len(got) != 0 {
		t.Fatalf("rejections = %v, want none", got)
	}
}

// TestFederationSearch_LoopRejected: a path that already holds this node
// is refused with 409 and ledgered.
func TestFederationSearch_LoopRejected(t *testing.T) {
	n := newFedNode(t, fedNodeOpts{dim: 128})
	resp, body := n.peerSearch(t, map[string]any{"query": "x"}, n.nodeID)
	if resp.StatusCode != 409 || body["error_code"] != "federation_loop" {
		t.Fatalf("status=%d body=%v, want 409 federation_loop", resp.StatusCode, body)
	}
	if got := n.rejections(t); len(got) != 1 || got[0] != "loop" {
		t.Fatalf("rejections = %v, want [loop]", got)
	}
}

// TestFederationSearch_RateLimited: past the inbound limit a peer gets
// 429 with Retry-After, and the refusal is ledgered.
func TestFederationSearch_RateLimited(t *testing.T) {
	n := newFedNode(t, fedNodeOpts{dim: 128, inboundQPS: 0.5})
	if resp, body := n.peerSearch(t, map[string]any{"query": "x", "mode": "keyword"}, ""); resp.StatusCode != 200 {
		t.Fatalf("first call: status=%d body=%v, want 200", resp.StatusCode, body)
	}
	resp, body := n.peerSearch(t, map[string]any{"query": "x", "mode": "keyword"}, "")
	if resp.StatusCode != 429 || body["error_code"] != "rate_limited" {
		t.Fatalf("second call: status=%d body=%v, want 429 rate_limited", resp.StatusCode, body)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "2" {
		t.Fatalf("Retry-After = %q, want 2 at 0.5 QPS", ra)
	}
	if got := n.rejections(t); len(got) != 1 || got[0] != "rate_limited" {
		t.Fatalf("rejections = %v, want [rate_limited]", got)
	}
}

// TestFederationSearch_MultiTenantFailsClosed: peers are trusted per
// node, so a multi-tenant node refuses a node-wide peer search.
func TestFederationSearch_MultiTenantFailsClosed(t *testing.T) {
	n := newFedNode(t, fedNodeOpts{dim: 128, mode: "multi-tenant"})
	resp, body := n.peerSearch(t, map[string]any{"query": "x"}, "")
	if resp.StatusCode != 403 || body["error_code"] != "federation_multi_tenant" {
		t.Fatalf("status=%d body=%v, want 403 federation_multi_tenant", resp.StatusCode, body)
	}
}

// TestFederationSearch_NeedsPeerCredentials: without the peer key the
// route is closed even though this node runs with no API key.
func TestFederationSearch_NeedsPeerCredentials(t *testing.T) {
	n := newFedNode(t, fedNodeOpts{dim: 128})
	req := jsonReq(t, http.MethodPost, n.ts.URL+"/v1/federation/search", map[string]any{"query": "x"},
		map[string]string{federation.HeaderLocalInstanceID: n.peerID})
	status, body := doJSON(t, n.ts.Client(), req)
	if status != 401 || body["error_code"] != "federation_unauthorized" {
		t.Fatalf("status=%d body=%v, want 401 federation_unauthorized", status, body)
	}
}

func jsonDecode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }
