// Package testmesh starts meshes of real Memora nodes for federation
// tests: each node is a full HTTP server (SQLite metadata, sqlite-vec,
// an offline hash embedder) whose peer registry is built the way
// memora-core serve builds it (config seed → store → registry). It is
// imported only by tests.
package testmesh

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// AgentID writes the memories Seed creates.
const AgentID = "2b4224a8-95b4-4171-9e6b-689847d53ac7"

// Node is one running node of a mesh.
type Node struct {
	Name  string
	ID    string // node ID
	TS    *httptest.Server
	Svc   *service.Service
	Peers *federation.Manager // the node's runtime peer registry
	// Config is the peer config the node was seeded with.
	Config []config.PeerConfig

	delay atomic.Int64 // ns added before every peer-route answer
	mu    sync.Mutex
	paths []string // X-Memora-Federation-Path values received on peer routes
}

// Options shape a mesh.
type Options struct {
	// Timeouts sets a peer's timeout_ms in the first node's config.
	Timeouts map[string]int
	// Mesh makes every pair of nodes peers (both directions); otherwise
	// only the first node calls the others.
	Mesh bool
	// FirstDim is node 0's embedding dimension (default 64); node i
	// gets FirstDim·(i+1).
	FirstDim int
}

// New starts one node per name. Node i embeds with
// hash:<FirstDim·(i+1)>, so no two nodes of a mesh share a vector space. Keys are per pair and direction:
// the caller holds the key, the callee only its SHA-256.

// RejectWindow is the meshes' federation.RejectionAggregator window:
// short, so a test sees unauthenticated refusals in the ledger quickly.
const RejectWindow = 50 * time.Millisecond

func New(t testing.TB, names []string, o Options) []*Node {
	t.Helper()
	nodes := make([]*Node, len(names))
	for i, name := range names {
		nodes[i] = &Node{Name: name, ID: types.NewID(), TS: httptest.NewUnstartedServer(nil)}
	}
	link := func(from, to *Node) {
		key := "key-" + from.Name + "-to-" + to.Name + "-0123456789"
		from.Config = append(from.Config, config.PeerConfig{
			NodeID: to.ID, Name: to.Name, URL: "http://" + to.TS.Listener.Addr().String(),
			APIKey: key, TimeoutMS: o.Timeouts[to.Name],
		})
		sum := sha256.Sum256([]byte(key))
		to.Config = append(to.Config, config.PeerConfig{
			NodeID: from.ID, Name: from.Name, URL: "http://" + from.TS.Listener.Addr().String(),
			InboundKeySHA256: hex.EncodeToString(sum[:]),
		})
	}
	for i, from := range nodes {
		for j, to := range nodes {
			if i != j && (i == 0 || o.Mesh) && (j > i || o.Mesh) {
				link(from, to)
			}
		}
	}
	// Mesh links each direction once; merge a node's two entries for the
	// same peer (outbound key + inbound hash) into one.
	for _, n := range nodes {
		n.Config = mergePeers(n.Config)
	}
	first := o.FirstDim
	if first <= 0 {
		first = 64
	}
	for i, n := range nodes {
		n.start(t, first*(i+1))
	}
	return nodes
}

func mergePeers(in []config.PeerConfig) []config.PeerConfig {
	byID := map[string]int{}
	var out []config.PeerConfig
	for _, p := range in {
		if j, ok := byID[p.NodeID]; ok {
			if p.APIKey != "" {
				out[j].APIKey, out[j].TimeoutMS = p.APIKey, p.TimeoutMS
			}
			if p.InboundKeySHA256 != "" {
				out[j].InboundKeySHA256 = p.InboundKeySHA256
			}
			continue
		}
		byID[p.NodeID] = len(out)
		out = append(out, p)
	}
	return out
}

func (n *Node) start(t testing.TB, dim int) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{DSN: filepath.Join(dir, "meta.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	graph := &storesqlite.GraphStore{}
	if err := graph.Open(ctx, adapter.GraphConfig{Driver: "sqlite_graph", DSN: filepath.Join(dir, "meta.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vec.db"), Dim: dim}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	led := &ledgersqlite.Store{}
	if err := led.Open(ctx, adapter.LedgerConfig{DSN: filepath.Join(dir, "ledger.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = led.Close() })
	emb, err := embedding.Open(fmt.Sprintf("hash:%d", dim))
	if err != nil {
		t.Fatal(err)
	}
	n.Svc = &service.Service{
		Metadata: meta, Vector: vec, Ledger: led, Embedder: emb, Graph: graph,
		Identity: map[string]adapter.IdentityProvider{string(types.IdentityProviderOpaque): identity.Opaque{}},
		NodeID:   n.ID, NodeName: n.Name,
	}
	// As at boot: config peers seed the store, which feeds the registry.
	reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true, PrivatePeerHosts: []string{"127.0.0.1"}}, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	sealer, err := federation.NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	n.Peers = federation.NewManager(reg, meta, sealer)
	n.Peers.Audit = n.Svc.AppendLedger
	if _, err := n.Peers.Seed(ctx, n.Config); err != nil {
		t.Fatal(err)
	}
	if err := n.Peers.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if n.Svc.Federation, err = federation.NewSearcher(reg); err != nil {
		t.Fatal(err)
	}
	h := httpserver.New(httpserver.Config{
		Service: n.Svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: httpserver.DefaultMaxBodyBytes, Federation: reg, FederationPeers: n.Peers,
		FederationRejectWindow: RejectWindow,
	}).Handler()
	n.TS.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if federation.IsPeerRoute(r.URL.Path) {
			n.mu.Lock()
			n.paths = append(n.paths, r.Header.Get(federation.HeaderFederationPath))
			n.mu.Unlock()
			select {
			case <-time.After(time.Duration(n.delay.Load())):
			case <-r.Context().Done():
				return
			}
		}
		h.ServeHTTP(w, r)
	})
	n.TS.Start()
	t.Cleanup(n.TS.Close)
}

// SetDelay makes the node wait d before answering each peer-route call.
func (n *Node) SetDelay(d time.Duration) { n.delay.Store(int64(d)) }

// ReceivedPaths returns the X-Memora-Federation-Path of every peer-route
// call the node received, in order.
func (n *Node) ReceivedPaths() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.paths...)
}

// Do sends a JSON request to the node and decodes the JSON answer.
func (n *Node) Do(t testing.TB, method, path string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, n.TS.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.TS.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

// Seed imprints content into a fresh workspace on the node and returns
// the memory ID.
func (n *Node) Seed(t testing.TB, content string) string {
	t.Helper()
	_, ws := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": n.Name}, nil)
	status, m := n.Do(t, "POST", "/v1/workspaces/"+ws["id"].(string)+"/memories",
		map[string]any{"content": content}, map[string]string{"Memora-Agent-Id": AgentID})
	if status != 201 {
		t.Fatalf("seed %s: %d %v", n.Name, status, m)
	}
	return m["id"].(string)
}

// Search posts body to /v1/search on the node.
func (n *Node) Search(t testing.TB, body map[string]any) (int, map[string]any) {
	t.Helper()
	return n.Do(t, "POST", "/v1/search", body, nil)
}

// HitNodes maps memory_id → node_id over a search response.
func HitNodes(body map[string]any) map[string]string {
	out := map[string]string{}
	results, _ := body["results"].([]any)
	for _, r := range results {
		h := r.(map[string]any)
		node, _ := h["node_id"].(string)
		out[h["memory_id"].(string)] = node
	}
	return out
}
