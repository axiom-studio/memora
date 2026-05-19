package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func testPeerClient(srv *httptest.Server, name string) *PeerClient {
	return &PeerClient{
		peer: Peer{
			ID:               name,
			Name:             name,
			Endpoint:         srv.URL,
			TrustMode:        TrustAPIKey,
			RequestTimeoutMS: 5000,
		},
		httpClient:   srv.Client(),
		federationID: "fed_test",
		instanceID:   "fed_test",
	}
}

func TestFanoutRecall_LocalOnly(t *testing.T) {
	local := &api.RecallResponse{
		Results:                []api.RecallHit{{MemoryID: "mem_local", Score: 0.9}},
		TotalCandidatesScanned: 5,
		LatencyMS:              2,
	}
	result, err := FanoutRecall(context.Background(), local, nil, "ws", api.RecallRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}
	if result.PartialSuccess {
		t.Error("unexpected partial success")
	}
}

func TestFanoutRecall_AllFailed_NoLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "bad-peer")}
	_, err := FanoutRecall(context.Background(), nil, peers, "ws", api.RecallRequest{}, "")
	if !errors.Is(err, ErrAllPeersFailed) {
		t.Fatalf("expected ErrAllPeersFailed, got %v", err)
	}
}

func TestFanoutRecall_MergesResults(t *testing.T) {
	peerResp := api.RecallResponse{
		Results:                []api.RecallHit{{MemoryID: "mem_peer", Score: 0.8}},
		TotalCandidatesScanned: 3,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(peerResp)
	}))
	defer srv.Close()

	local := &api.RecallResponse{
		Results:                []api.RecallHit{{MemoryID: "mem_local", Score: 0.9}},
		TotalCandidatesScanned: 5,
	}
	peers := []*PeerClient{testPeerClient(srv, "peer1")}
	result, err := FanoutRecall(context.Background(), local, peers, "ws", api.RecallRequest{}, "fed_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results (local + peer), got %d", len(result.Results))
	}
	if result.TotalScanned != 8 {
		t.Errorf("expected 8 total scanned, got %d", result.TotalScanned)
	}
	if result.PartialSuccess {
		t.Error("no peers failed, should not be partial")
	}
}

func TestFanoutRecall_PartialSuccess(t *testing.T) {
	goodResp := api.RecallResponse{
		Results: []api.RecallHit{{MemoryID: "mem_good", Score: 0.8}},
	}
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(goodResp)
	}))
	defer goodSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	peers := []*PeerClient{
		testPeerClient(goodSrv, "good-peer"),
		testPeerClient(badSrv, "bad-peer"),
	}
	result, err := FanoutRecall(context.Background(), nil, peers, "ws", api.RecallRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.PartialSuccess {
		t.Error("expected partial success")
	}
	if len(result.FailedPeers) != 1 || result.FailedPeers[0] != "bad-peer" {
		t.Errorf("expected bad-peer in failed list, got %v", result.FailedPeers)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result from good peer, got %d", len(result.Results))
	}
}

func TestFanoutRecall_PeerLatencyTracked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.RecallResponse{
			Results: []api.RecallHit{{MemoryID: "mem_x", Score: 0.5}},
		})
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "fast-peer")}
	result, err := FanoutRecall(context.Background(), nil, peers, "ws", api.RecallRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.PeerLatencyMS["fast-peer"]; !ok {
		t.Error("expected latency entry for fast-peer")
	}
}

func TestFanoutRecall_ContextCancel(t *testing.T) {
	done := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	defer func() {
		close(done)
		slowSrv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	peers := []*PeerClient{testPeerClient(slowSrv, "slow-peer")}
	peers[0].peer.RequestTimeoutMS = 80
	peers[0].httpClient.Timeout = 80 * time.Millisecond
	result, err := FanoutRecall(ctx, nil, peers, "ws", api.RecallRequest{}, "")
	if err == nil && result != nil && !result.PartialSuccess {
		t.Error("expected failure or partial success on timeout")
	}
}

func TestFanoutLookup_FirstWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.MemoryEnvelope{
			Memory: &types.Memory{ID: "mem_found"},
		})
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "peer1")}
	result, err := FanoutLookup(context.Background(), peers, "ws", "mem_found", "")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Memory.ID != "mem_found" {
		t.Error("expected found memory")
	}
}

func TestFanoutLookup_AllNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "peer1")}
	result, err := FanoutLookup(context.Background(), peers, "ws", "mem_missing", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil for all-404")
	}
}

func TestFanoutLookup_Empty(t *testing.T) {
	result, err := FanoutLookup(context.Background(), nil, "ws", "mem", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil for no peers")
	}
}
