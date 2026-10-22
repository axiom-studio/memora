// Package federation_e2e runs the PRD's federation acceptance scenarios
// (F-10, "Done when" 3 and 4) against real in-process memora nodes:
// separate data dirs, real SQLite stores, different embedders, peers
// registered at runtime over the API. Run with -race.
package federation_e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/internal/testmesh"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// pair registers b as a peer of a over the API: a gets the key to call
// b, b gets the key's hash for a. Returns the key.
func pair(t *testing.T, a, b *testmesh.Node) string {
	t.Helper()
	key := "e2e-" + a.Name + "-to-" + b.Name + "-" + types.NewID()
	if status, body := a.Do(t, "POST", "/v1/federation/peers", map[string]any{
		"node_id": b.ID, "name": b.Name, "url": b.TS.URL, "api_key": key,
	}, nil); status != 200 {
		t.Fatalf("register %s on %s: %d %v", b.Name, a.Name, status, body)
	}
	if status, body := b.Do(t, "POST", "/v1/federation/peers", map[string]any{
		"node_id": a.ID, "name": a.Name, "url": a.TS.URL, "inbound_key": key,
	}, nil); status != 200 {
		t.Fatalf("register %s on %s: %d %v", a.Name, b.Name, status, body)
	}
	return key
}

// standalone starts nodes that do not know each other, each with its
// own embedding dimension (64, 128, …).
func standalone(t *testing.T, names ...string) []*testmesh.Node {
	t.Helper()
	var out []*testmesh.Node
	for i, n := range names {
		out = append(out, testmesh.New(t, []string{n}, testmesh.Options{FirstDim: 64 * (i + 1)})...)
	}
	return out
}

func peerCall(t *testing.T, to *testmesh.Node, asNode, key, path string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", to.TS.URL+"/v1/federation/search", strings.NewReader(`{"query":"x","mode":"keyword"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(federation.HeaderLocalInstanceID, asNode)
	req.Header.Set(federation.HeaderFederationAuth, "Bearer "+key)
	if path != "" {
		req.Header.Set(federation.HeaderFederationPath, path)
	}
	resp, err := to.TS.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func ledger(t *testing.T, n *testmesh.Node, op string) []api.LedgerEntry {
	t.Helper()
	entries, _, err := n.Svc.Ledger.Query(t.Context(), adapter.LedgerQuery{Op: []string{op}, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func failedPeers(body map[string]any) map[string]string {
	out := map[string]string{}
	list, _ := body["failed_peers"].([]any)
	for _, f := range list {
		m := f.(map[string]any)
		out[m["node_id"].(string)] = m["error_code"].(string)
	}
	return out
}

// TestFederation_EndToEnd walks scenarios 1–4 and 6 in order on one pair
// of nodes, as an operator would.
func TestFederation_EndToEnd(t *testing.T) {
	nodes := standalone(t, "a", "b") // hash:64 and hash:128, no peers yet
	a, b := nodes[0], nodes[1]
	if a.Svc.Embedder.Dim() == b.Svc.Embedder.Dim() {
		t.Fatalf("nodes share an embedding dimension (%d)", a.Svc.Embedder.Dim())
	}
	memA := a.Seed(t, "volcano ash advisory for the north sector")
	memB := b.Seed(t, "volcano seismic readings, hourly\n\nfull table follows")

	// 1. Runtime peering; a federated query returns both nodes' hits;
	//    the peer's memory is fetched by (node_id, memory_id).
	key := pair(t, a, b)
	for _, mode := range []string{"keyword", "vector", "hybrid"} {
		status, body := a.Search(t, map[string]any{"query": "volcano", "mode": mode, "k": 10, "federated": true})
		hits := testmesh.HitNodes(body)
		if status != 200 || hits[memA] != a.ID || hits[memB] != b.ID {
			t.Fatalf("1/%s: %d hits %v body %v", mode, status, hits, body)
		}
	}
	status, env := a.Do(t, "GET", "/v1/memories/"+memB+"?node="+b.ID, nil, nil)
	if status != 200 || env["node_id"] != b.ID || !strings.Contains(fmt.Sprint(env["content"]), "full table follows") {
		t.Fatalf("1/fetch: %d %v", status, env)
	}

	// 6. Audit on both nodes so far.
	if s := ledger(t, a, federation.OpSearch); len(s) != 3 {
		t.Fatalf("6: a has %d federation_search entries, want 3", len(s))
	}
	if in := ledger(t, b, federation.OpInbound); len(in) != 3 || in[0].APIKeyID != a.ID {
		t.Fatalf("6: b's federation_inbound = %+v", in)
	}
	if f := ledger(t, a, federation.OpFetch); len(f) != 1 || f[0].APIKeyID != b.ID {
		t.Fatalf("6: a's federation_fetch = %+v", f)
	}
	if f := ledger(t, b, federation.OpFetch); len(f) != 1 || f[0].APIKeyID != a.ID {
		t.Fatalf("6: b's federation_fetch = %+v", f)
	}

	// 3. Unknown caller → 401; revoked caller → 403 (and a sees it).
	if got := peerCall(t, b, types.NewID(), key, ""); got != 401 {
		t.Fatalf("3: unknown caller = %d, want 401", got)
	}
	if got := peerCall(t, b, a.ID, key, ""); got != 200 {
		t.Fatalf("3: a with its key = %d, want 200", got)
	}
	if status, _ := b.Do(t, "POST", "/v1/federation/peers", map[string]any{"node_id": a.ID, "name": "a", "url": a.TS.URL, "revoked": true}, nil); status != 200 {
		t.Fatalf("3: revoke: %d", status)
	}
	if got := peerCall(t, b, a.ID, key, ""); got != 403 {
		t.Fatalf("3: revoked caller = %d, want 403", got)
	}
	_, body := a.Search(t, map[string]any{"query": "volcano", "mode": "keyword", "federated": true})
	if failedPeers(body)[b.ID] != "federation_peer_revoked" {
		t.Fatalf("3: a's view of a revoking peer: %v", body["failed_peers"])
	}
	// The unknown caller is in a summary entry once its window closes.
	var reasons map[string]int
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		reasons = map[string]int{}
		for _, e := range ledger(t, b, federation.OpRejected) {
			reasons[fmt.Sprint(e.Metadata["reason"])]++
		}
		if reasons[federation.ReasonUnknownPeer] == 1 || time.Now().After(deadline) {
			break
		}
	}
	if reasons[federation.ReasonUnknownPeer] != 1 || reasons[federation.ReasonRevoked] < 2 {
		t.Fatalf("6: b's rejections by reason = %v", reasons)
	}
	if status, _ := b.Do(t, "POST", "/v1/federation/peers", map[string]any{"node_id": a.ID, "name": "a", "url": a.TS.URL}, nil); status != 200 {
		t.Fatalf("3: un-revoke: %d", status)
	}

	// 2. Removing the peer stops b's results immediately.
	before := len(b.ReceivedPaths())
	if status, _ := a.Do(t, "DELETE", "/v1/federation/peers/"+b.ID, nil, nil); status != 204 {
		t.Fatalf("2: delete: %d", status)
	}
	_, body = a.Search(t, map[string]any{"query": "volcano", "mode": "keyword", "federated": true})
	if hits := testmesh.HitNodes(body); hits[memB] != "" || hits[memA] != a.ID || len(b.ReceivedPaths()) != before {
		t.Fatalf("2: after removing b: hits %v; b got %d calls", hits, len(b.ReceivedPaths())-before)
	}

	// 4. A stopped peer → partial_success with failed_peers.
	pair(t, a, b)
	b.TS.Close()
	_, body = a.Search(t, map[string]any{"query": "volcano", "mode": "keyword", "federated": true})
	if body["partial_success"] != true || failedPeers(body)[b.ID] != "unreachable" || testmesh.HitNodes(body)[memA] != a.ID {
		t.Fatalf("4: with b stopped: %v", body)
	}
}

// TestFederation_LoopGuard (scenario 5): in a full A↔B↔C mesh, a
// federated search is one hop (b never forwards to c), and a request
// carrying a loop path, A→B→C→A or one that already holds the receiver,
// is refused with 409 and audited.
func TestFederation_LoopGuard(t *testing.T) {
	nodes := testmesh.New(t, []string{"a", "b", "c"}, testmesh.Options{Mesh: true})
	a, b, c := nodes[0], nodes[1], nodes[2]
	b.Seed(t, "loop guard memory")

	_, body := a.Search(t, map[string]any{"query": "loop guard", "mode": "keyword", "federated": true})
	if hits := testmesh.HitNodes(body); len(hits) != 1 {
		t.Fatalf("a's federated search: %v", body)
	}
	for _, p := range append(b.ReceivedPaths(), c.ReceivedPaths()...) {
		if p != a.ID {
			t.Fatalf("a peer was called with path %q: something forwarded", p)
		}
	}

	// c's key for calls from b, and a's key for calls from c.
	keyOf := func(from, to *testmesh.Node) string {
		for _, p := range from.Config {
			if p.NodeID == to.ID {
				return p.APIKey
			}
		}
		t.Fatalf("no key from %s to %s", from.Name, to.Name)
		return ""
	}
	// A→B→C: c is asked by b with path "a,b"; two hops is the limit.
	if got := peerCall(t, c, b.ID, keyOf(b, c), a.ID+","+b.ID); got != 409 {
		t.Fatalf("A→B→C path at c = %d, want 409", got)
	}
	// …→C→A: a is asked by c with a path that already holds a.
	if got := peerCall(t, a, c.ID, keyOf(c, a), a.ID); got != 409 {
		t.Fatalf("path holding a at a = %d, want 409", got)
	}
	// One hop is fine.
	if got := peerCall(t, c, b.ID, keyOf(b, c), b.ID); got != 200 {
		t.Fatalf("one-hop path at c = %d, want 200", got)
	}
	for _, n := range []*testmesh.Node{c, a} {
		var loops int
		for _, e := range ledger(t, n, federation.OpRejected) {
			if e.Metadata["reason"] == federation.ReasonLoop {
				loops++
			}
		}
		if loops != 1 {
			t.Fatalf("%s audited %d loop rejections, want 1", n.Name, loops)
		}
	}
}
