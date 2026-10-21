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

func TestFanoutSearch_LocalOnly(t *testing.T) {
	local := &api.RecallResponse{
		Results:                []api.RecallHit{{MemoryID: "mem_local", Score: 0.9}},
		TotalCandidatesScanned: 5,
		LatencyMS:              2,
	}
	result, err := FanoutSearch(context.Background(), local, "local-node", nil, api.FederationSearchRequest{}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lists) != 1 || len(result.Lists[0].Hits) != 1 {
		t.Fatalf("lists = %+v, want the local list only", result.Lists)
	}
	if result.PartialSuccess() {
		t.Error("unexpected partial success")
	}
}

func TestFanoutSearch_AllFailed_NoLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "bad-peer")}
	_, err := FanoutSearch(context.Background(), nil, "local-node", peers, api.FederationSearchRequest{}, "", 0, nil)
	if !errors.Is(err, ErrAllPeersFailed) {
		t.Fatalf("expected ErrAllPeersFailed, got %v", err)
	}
}

func TestFanoutSearch_MergesResults(t *testing.T) {
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
	result, err := FanoutSearch(context.Background(), local, "local-node", peers, api.FederationSearchRequest{}, "fed_test", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lists) != 2 || result.Lists[0].NodeID != "local-node" || result.Lists[1].Hits[0].NodeID != "peer1" {
		t.Fatalf("lists = %+v, want local then peer1", result.Lists)
	}
	if result.TotalScanned != 8 {
		t.Errorf("expected 8 total scanned, got %d", result.TotalScanned)
	}
	if result.PartialSuccess() {
		t.Error("no peers failed, should not be partial")
	}
}

func TestFanoutSearch_PartialSuccess(t *testing.T) {
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
	result, err := FanoutSearch(context.Background(), nil, "local-node", peers, api.FederationSearchRequest{}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.PartialSuccess() {
		t.Error("expected partial success")
	}
	if fp := result.FailedPeers(); len(fp) != 1 || fp[0].Name != "bad-peer" || fp[0].ErrorCode != "http_500" {
		t.Errorf("failed peers = %+v, want bad-peer with http_500", fp)
	}
	if len(result.Lists) != 1 || len(result.Lists[0].Hits) != 1 {
		t.Fatalf("lists = %+v, want the good peer's one hit", result.Lists)
	}
}

func TestFanoutSearch_PeerLatencyTracked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.RecallResponse{
			Results: []api.RecallHit{{MemoryID: "mem_x", Score: 0.5}},
		})
	}))
	defer srv.Close()

	peers := []*PeerClient{testPeerClient(srv, "fast-peer")}
	result, err := FanoutSearch(context.Background(), nil, "local-node", peers, api.FederationSearchRequest{}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.PeerLatencyMS["fast-peer"]; !ok {
		t.Error("expected latency entry for fast-peer")
	}
}

func TestFanoutSearch_ContextCancel(t *testing.T) {
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
	result, err := FanoutSearch(ctx, nil, "local-node", peers, api.FederationSearchRequest{}, "", 0, nil)
	if err == nil && result != nil && !result.PartialSuccess() {
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
