package http_test

import (
	"context"
	"strings"
	"testing"
)

// TestPeers_KeyUnavailable (#6177): a peer whose stored api_key no
// longer opens (the federation secret changed) is reported, not silently
// skipped: GET peers says unreadable, a federated search lists it in
// failed_peers as key_unavailable with partial_success, a fetch from it
// is 502 key_unavailable, and setting the key again restores it.
func TestPeers_KeyUnavailable(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	memB := b.seed(t, "basalt column survey")
	search := func(body map[string]any) map[string]any {
		t.Helper()
		_, resp := a.search(t, body)
		return resp
	}
	if got := hitNodes(search(map[string]any{"query": "basalt", "mode": "keyword", "federated": true})); got[memB] != b.ID {
		t.Fatalf("before: hits %v", got)
	}

	// What a lost federation secret leaves: the sealed key no longer opens.
	ctx := context.Background()
	recs, err := a.Svc.Metadata.ListPeers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.NodeID == b.ID {
			r.SealedAPIKey = []byte(strings.Repeat("x", 48))
			if err := a.Svc.Metadata.UpsertPeer(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.Peers.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	_, list := a.peersAPI(t, "GET", "/v1/federation/peers", nil)
	for _, p := range list["peers"].([]any) {
		p := p.(map[string]any)
		if p["node_id"] == b.ID && (p["has_api_key"] != false || p["api_key_status"] != "unreadable") {
			t.Fatalf("GET peers shows %v; want has_api_key false, api_key_status unreadable", p)
		}
	}
	for _, body := range []map[string]any{
		{"query": "basalt", "mode": "keyword", "federated": true},
		{"query": "basalt", "mode": "keyword", "federated": true, "peers": []string{b.ID}},
	} {
		resp := search(body)
		failed, _ := resp["failed_peers"].([]any)
		if resp["partial_success"] != true || len(failed) != 1 || failed[0].(map[string]any)["error_code"] != "key_unavailable" || failed[0].(map[string]any)["node_id"] != b.ID {
			t.Fatalf("%v: %v, want partial_success and b failed with key_unavailable", body, resp)
		}
	}
	if status, body := a.Do(t, "GET", "/v1/memories/"+memB+"?node="+b.ID, nil, nil); status != 502 || body["error_code"] != "key_unavailable" {
		t.Fatalf("fetch from b: %d %v, want 502 key_unavailable", status, body)
	}
	if m := a.metrics(t); !strings.Contains(m, `outcome="key_unavailable"`) {
		t.Fatalf("/metrics lacks key_unavailable:\n%s", m)
	}

	// Setting the key again restores the peer.
	var url string
	for _, p := range list["peers"].([]any) {
		if p := p.(map[string]any); p["node_id"] == b.ID {
			url = p["url"].(string)
		}
	}
	if status, body := a.peersAPI(t, "POST", "/v1/federation/peers", map[string]any{"node_id": b.ID, "name": "b", "url": url, "api_key": "key-a-to-b-0123456789"}); status != 200 || body["has_api_key"] != true {
		t.Fatalf("re-set key: %d %v", status, body)
	}
	if got := hitNodes(search(map[string]any{"query": "basalt", "mode": "keyword", "federated": true})); got[memB] != b.ID {
		t.Fatalf("after re-setting the key: hits %v", got)
	}
}
