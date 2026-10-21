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

// TestIntegration_ThreePeerSearch simulates a full federation recall
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
			{NodeID: nid("peer1"), Name: "peer1", URL: srv.URL, APIKey: "key1"},
		},
	}

	reg, err := NewRegistry(cfg, nid("fed_main"))
	if err != nil {
		t.Fatal(err)
	}

	pool, err := NewClientPool(reg)
	if err != nil {
		t.Fatal(err)
	}

	c := pool.Client(nid("peer1"))
	if c == nil {
		t.Fatal("expected client for peer1")
	}

	// Override the http client to use the test server's client (handles TLS)
	c.httpClient = srv.Client()

	resp, err := c.Search(context.Background(), api.FederationSearchRequest{Query: "test"}, "fed_main")
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

	_, err := FanoutSearch(context.Background(), nil, "local-node", peers, api.FederationSearchRequest{}, "", 0, nil)
	if !errors.Is(err, ErrAllPeersFailed) {
		t.Errorf("expected ErrAllPeersFailed, got %v", err)
	}
}
