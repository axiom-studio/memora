package http_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func (n *meshNode) peersAPI(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, n.ts.Client(), jsonReq(t, method, n.ts.URL+path, body, nil))
}

// TestPeers_E2E_RuntimeRegistry (F-7): peers added over the API are
// searched on the next request; a deleted peer is skipped and its
// inbound calls refused; a revoked one gets 403; no key ever comes back.
func TestPeers_E2E_RuntimeRegistry(t *testing.T) {
	// Two nodes that do not know each other.
	nodes := newMesh(t, []string{"a"}, nil)
	a := nodes[0]
	b := newMesh(t, []string{"b"}, nil)[0]
	memB := b.seed(t, "nebula survey notes")

	const aToB = "runtime-key-a-to-b-0123456789"
	if status, body := a.peersAPI(t, "POST", "/v1/federation/peers", map[string]any{
		"node_id": b.id, "name": "b", "url": b.ts.URL, "api_key": aToB, "timeout_ms": 2000,
	}); status != 200 || body["has_api_key"] != true || body["has_inbound_key"] != false {
		t.Fatalf("add b on a: %d %v", status, body)
	}
	if status, body := b.peersAPI(t, "POST", "/v1/federation/peers", map[string]any{
		"node_id": a.id, "name": "a", "url": a.ts.URL, "inbound_key": aToB,
	}); status != 200 || body["has_inbound_key"] != true {
		t.Fatalf("add a on b: %d %v", status, body)
	}

	search := func() map[string]any {
		_, body := a.search(t, map[string]any{"query": "nebula", "mode": "keyword", "federated": true})
		return body
	}
	if got := hitNodes(search()); got[memB] != b.id {
		t.Fatalf("after adding b: hits %v, want b's memory", got)
	}

	// Keys are write-only: never in a list or an upsert answer.
	for _, n := range []*meshNode{a, b} {
		_, list := n.peersAPI(t, "GET", "/v1/federation/peers", nil)
		raw := toJSON(t, list)
		if strings.Contains(raw, aToB) || strings.Contains(raw, "sealed") {
			t.Fatalf("%s's peer list exposes a key: %s", n.name, raw)
		}
		if peers, _ := list["peers"].([]any); len(peers) != 1 {
			t.Fatalf("%s's peer list = %s", n.name, raw)
		}
	}

	// Revoke a on b: a's next search gets 403 from b, reported as failed.
	if status, _ := b.peersAPI(t, "POST", "/v1/federation/peers", map[string]any{
		"node_id": a.id, "name": "a", "url": a.ts.URL, "revoked": true,
	}); status != 200 {
		t.Fatalf("revoke: %d", status)
	}
	body := search()
	if failed, _ := body["failed_peers"].([]any); len(failed) != 1 || failed[0].(map[string]any)["error_code"] != "federation_peer_revoked" {
		t.Fatalf("after revoking a on b: failed_peers %v, want federation_peer_revoked", body["failed_peers"])
	}

	// Delete a on b: b refuses a's calls with 401 from the next request.
	if status, _ := b.peersAPI(t, "DELETE", "/v1/federation/peers/"+a.id, nil); status != 204 {
		t.Fatalf("delete a on b: %d", status)
	}
	if failed, _ := search()["failed_peers"].([]any); len(failed) != 1 || failed[0].(map[string]any)["error_code"] != "federation_unauthorized" {
		t.Fatalf("after deleting a on b: failed_peers %v, want federation_unauthorized", failed)
	}

	// Delete b on a: a no longer asks b at all.
	before := len(b.receivedPaths())
	if status, _ := a.peersAPI(t, "DELETE", "/v1/federation/peers/"+b.id, nil); status != 204 {
		t.Fatalf("delete b on a: %d", status)
	}
	body = search()
	if lat, _ := body["peer_latency_ms"].(map[string]any); lat[b.id] != nil || body["failed_peers"] != nil || len(b.receivedPaths()) != before {
		t.Fatalf("after deleting b on a: %v; b received %d new calls", body, len(b.receivedPaths())-before)
	}
	if status, body := a.peersAPI(t, "DELETE", "/v1/federation/peers/"+b.id, nil); status != 404 {
		t.Fatalf("second delete: %d %v, want 404", status, body)
	}
}

// TestPeers_E2E_Validation: the peer API refuses malformed input,
// config-only fields and this node's own ID, and is behind the API key.
func TestPeers_E2E_Validation(t *testing.T) {
	a := newMesh(t, []string{"a"}, nil)[0]
	for _, body := range []map[string]any{
		{"node_id": "node_1", "name": "x", "url": "https://x"},
		{"node_id": types.NewID(), "url": "https://x"},
		{"node_id": types.NewID(), "name": "x", "url": "ftp://x"},
		{"node_id": types.NewID(), "name": "x", "url": "https://x", "tls_cert": "/etc/passwd"},
		{"node_id": a.id, "name": "self", "url": "https://x"},
		{"node_id": types.NewID(), "name": "x", "url": "https://x", "inbound_key": "k", "inbound_key_sha256": strings.Repeat("ab", 32)},
		{"node_id": types.NewID(), "name": "x", "url": "https://x", "client_cert_sha256": strings.Repeat("ab", 32)},
	} {
		if status, resp := a.peersAPI(t, "POST", "/v1/federation/peers", body); status != 400 {
			t.Fatalf("%v: %d %v, want 400", body, status, resp)
		}
	}
	if _, list := a.peersAPI(t, "GET", "/v1/federation/peers", nil); len(list["peers"].([]any)) != 0 {
		t.Fatalf("a rejected request left a peer: %v", list)
	}
}

// TestPeers_SeedNeverClobbers: config peers are imported once; a
// runtime edit or removal survives the next start with the same config.
func TestPeers_SeedNeverClobbers(t *testing.T) {
	a := newMesh(t, []string{"a"}, nil)[0]
	ctx := context.Background()
	seedID := types.NewID()
	cfg := []config.PeerConfig{{NodeID: seedID, Name: "from-config", URL: "https://cfg.example.com", APIKey: "cfg-key"}}
	if n, err := a.peers.Seed(ctx, cfg); err != nil || n != 1 {
		t.Fatalf("first seed = %d, %v", n, err)
	}
	if _, err := a.peers.Upsert(ctx, api.PeerRequest{NodeID: seedID, Name: "edited", URL: "https://edited.example.com"}); err != nil {
		t.Fatal(err)
	}
	restart := func() []api.PeerView {
		t.Helper()
		if n, err := a.peers.Seed(ctx, cfg); err != nil || n != 0 {
			t.Fatalf("re-seed = %d, %v; want 0 imported", n, err)
		}
		if err := a.peers.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		peers, err := a.peers.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return peers
	}
	if peers := restart(); len(peers) != 1 || peers[0].Name != "edited" || !peers[0].HasAPIKey {
		t.Fatalf("after restart = %+v, want the edit kept (and the seeded key)", peers)
	}
	if err := a.peers.Delete(ctx, seedID); err != nil {
		t.Fatal(err)
	}
	if peers := restart(); len(peers) != 0 {
		t.Fatalf("after delete and restart = %+v, want it to stay deleted", peers)
	}
}

// TestPeers_ConcurrentSearchAndEdit runs federated searches while peers
// are added and deleted; run with -race.
func TestPeers_ConcurrentSearchAndEdit(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	b.seed(t, "quasar timing data")
	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := a.svc.Search(ctx, api.SearchRequest{Query: "quasar", Mode: api.RecallModeKeyword, Federated: true}); err != nil {
					t.Errorf("search: %v", err)
					return
				}
				_, _ = a.svc.FetchMemoryFrom(ctx, b.id, types.NewID())
			}
		}()
	}
	for i := 0; i < 30; i++ {
		id := types.NewID()
		if _, err := a.peers.Upsert(ctx, api.PeerRequest{NodeID: id, Name: "churn", URL: "http://127.0.0.1:1", APIKey: "k"}); err != nil {
			t.Error(err)
		}
		if err := a.peers.Delete(ctx, id); err != nil {
			t.Error(err)
		}
		if _, err := a.peers.Upsert(ctx, api.PeerRequest{NodeID: b.id, Name: "b", URL: b.ts.URL, Revoked: i%2 == 0}); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
