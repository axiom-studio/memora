package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types/api"
)

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
