package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestIntegration_ThreePeerRecall simulates a full federation recall
// across 3 peers (2 remote + local) with dedup and aggregation.
func TestIntegration_ThreePeerRecall(t *testing.T) {
	peer1Resp := api.RecallResponse{
		Results: []api.RecallHit{
			{MemoryID: "mem_shared", Score: 0.85, Text: "shared content"},
			{MemoryID: "mem_peer1_only", Score: 0.75, Text: "peer1 unique"},
		},
		TotalCandidatesScanned: 100,
		LatencyMS:              10,
	}
	peer2Resp := api.RecallResponse{
		Results: []api.RecallHit{
			{MemoryID: "mem_shared", Score: 0.9, Text: "shared content v2"},
			{MemoryID: "mem_peer2_only", Score: 0.7, Text: "peer2 unique"},
		},
		TotalCandidatesScanned: 80,
		LatencyMS:              15,
	}

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFederationID) == "" {
			t.Error("peer1: missing federation ID header")
		}
		json.NewEncoder(w).Encode(peer1Resp)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFederationPath) != "fed_local" {
			t.Errorf("peer2: unexpected path header: %s", r.Header.Get(HeaderFederationPath))
		}
		json.NewEncoder(w).Encode(peer2Resp)
	}))
	defer srv2.Close()

	localResult := &api.RecallResponse{
		Results: []api.RecallHit{
			{MemoryID: "mem_local", Score: 0.95, Text: "local hit", Via: "seed"},
			{MemoryID: "mem_shared", Score: 0.8, Text: "shared content local", Via: "seed"},
		},
		TotalCandidatesScanned: 50,
		LatencyMS:              2,
	}

	peers := []*PeerClient{
		testPeerClient(srv1, "us-west"),
		testPeerClient(srv2, "eu-central"),
	}

	fr, err := FanoutRecall(
		context.Background(),
		localResult,
		peers,
		"ws_test",
		api.RecallRequest{Query: "test query", K: 10},
		"fed_local",
	)
	if err != nil {
		t.Fatal(err)
	}

	if fr.PartialSuccess {
		t.Error("expected no failures")
	}
	if len(fr.FailedPeers) != 0 {
		t.Errorf("expected 0 failed peers, got %v", fr.FailedPeers)
	}
	if fr.TotalScanned != 230 {
		t.Errorf("expected 230 total scanned (50+100+80), got %d", fr.TotalScanned)
	}

	resp := Aggregate(fr, AggregateOpts{K: 10, LocalInstanceID: "fed_local"})

	memIDs := map[string]bool{}
	for _, r := range resp.Results {
		memIDs[r.MemoryID] = true
	}
	if !memIDs["mem_local"] {
		t.Error("missing mem_local")
	}
	if !memIDs["mem_peer1_only"] {
		t.Error("missing mem_peer1_only")
	}
	if !memIDs["mem_peer2_only"] {
		t.Error("missing mem_peer2_only")
	}
	if !memIDs["mem_shared"] {
		t.Error("missing mem_shared (should be deduped, not removed)")
	}

	sharedCount := 0
	for _, r := range resp.Results {
		if r.MemoryID == "mem_shared" {
			sharedCount++
		}
	}
	if sharedCount != 1 {
		t.Errorf("expected 1 deduped mem_shared, got %d", sharedCount)
	}

	if resp.Results[0].Score < resp.Results[len(resp.Results)-1].Score {
		t.Error("results not sorted descending")
	}
}

// TestIntegration_OnePeerDown verifies partial success when one peer
// fails while another succeeds.
func TestIntegration_OnePeerDown(t *testing.T) {
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.RecallResponse{
			Results: []api.RecallHit{{MemoryID: "mem_good", Score: 0.8}},
		})
	}))
	defer goodSrv.Close()

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer badSrv.Close()

	peers := []*PeerClient{
		testPeerClient(goodSrv, "healthy"),
		testPeerClient(badSrv, "crashed"),
	}

	fr, err := FanoutRecall(context.Background(), nil, peers, "ws", api.RecallRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !fr.PartialSuccess {
		t.Error("expected partial success")
	}
	if len(fr.FailedPeers) != 1 {
		t.Fatalf("expected 1 failed peer, got %d", len(fr.FailedPeers))
	}

	resp := Aggregate(fr, AggregateOpts{K: 10})
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

// TestIntegration_LookupFanout simulates lookup across 2 peers where
// the second has the memory.
func TestIntegration_LookupFanout(t *testing.T) {
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()

	srvFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.MemoryEnvelope{
			Memory: &types.Memory{ID: "mem_federated", WorkspaceID: "ws_test"},
		})
	}))
	defer srvFound.Close()

	peers := []*PeerClient{
		testPeerClient(srv404, "peer-empty"),
		testPeerClient(srvFound, "peer-has-it"),
	}

	result, err := FanoutLookup(context.Background(), peers, "ws_test", "mem_federated", "fed_local")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected lookup result")
	}
	if result.Memory.ID != "mem_federated" {
		t.Errorf("expected mem_federated, got %s", result.Memory.ID)
	}
}

// TestIntegration_RegistryToPeers verifies the full path from config →
// registry → client pool → fanout.
func TestIntegration_RegistryToPeers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.RecallResponse{
			Results: []api.RecallHit{{MemoryID: "mem_reg", Score: 0.8}},
		})
	}))
	defer srv.Close()

	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_main",
		Peers: []config.PeerConfig{
			{Name: "peer1", URL: srv.URL, APIKey: "key1"},
		},
	}

	reg, err := NewRegistry(cfg, "fed_main")
	if err != nil {
		t.Fatal(err)
	}

	pool, err := NewClientPool(reg)
	if err != nil {
		t.Fatal(err)
	}

	c := pool.Client("peer1")
	if c == nil {
		t.Fatal("expected client for peer1")
	}

	// Override the http client to use the test server's client (handles TLS)
	c.httpClient = srv.Client()

	resp, err := c.Recall(context.Background(), "ws_test", api.RecallRequest{Query: "test"}, "fed_main")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

// TestIntegration_AllPeersFailed_Error verifies ErrAllPeersFailed when
// every peer and local fails.
func TestIntegration_AllPeersFailed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	peers := []*PeerClient{
		testPeerClient(srv, "dead1"),
		testPeerClient(srv, "dead2"),
	}

	_, err := FanoutRecall(context.Background(), nil, peers, "ws", api.RecallRequest{}, "")
	if !errors.Is(err, ErrAllPeersFailed) {
		t.Errorf("expected ErrAllPeersFailed, got %v", err)
	}
}

// TestIntegration_AggregateTopK_AcrossPeers verifies that K is enforced
// across the merged result set.
func TestIntegration_AggregateTopK_AcrossPeers(t *testing.T) {
	makeHits := func(prefix string, n int) []api.RecallHit {
		hits := make([]api.RecallHit, n)
		for i := range hits {
			hits[i] = api.RecallHit{
				MemoryID: prefix + string(rune('a'+i)),
				Score:    0.9 - float64(i)*0.05,
			}
		}
		return hits
	}

	fr := &FanoutResult{
		Results: append(
			func() []api.RecallHit {
				h := makeHits("local_", 5)
				for i := range h {
					h[i].Via = "seed"
				}
				return h
			}(),
			func() []api.RecallHit {
				h := makeHits("peer_", 5)
				for i := range h {
					h[i].Via = "federation:peer1"
				}
				return h
			}()...,
		),
		TotalScanned: 100,
	}

	resp := Aggregate(fr, AggregateOpts{K: 3})
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results (K=3), got %d", len(resp.Results))
	}
}
