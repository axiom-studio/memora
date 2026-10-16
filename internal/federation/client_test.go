package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestPeerClient_Recall(t *testing.T) {
	want := api.RecallResponse{
		Results: []api.RecallHit{
			{MemoryID: "mem_abc", Score: 0.9, Text: "hello"},
		},
		TotalCandidatesScanned: 10,
		LatencyMS:              5,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/workspaces/ws_test/recall" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(HeaderFederationID) == "" {
			t.Error("missing federation ID header")
		}
		if r.Header.Get(HeaderFederationAuth) != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get(HeaderFederationAuth))
		}
		if r.Header.Get(HeaderFederationPath) != "fed_a" {
			t.Errorf("unexpected path header: %s", r.Header.Get(HeaderFederationPath))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	pc := &PeerClient{
		peer: Peer{
			ID:        "peer1",
			Name:      "test-peer",
			Endpoint:  srv.URL,
			TrustMode: TrustAPIKey,
			APIKey:    "test-key",
		},
		httpClient:   srv.Client(),
		federationID: "fed_a",
		instanceID:   "fed_a",
	}

	got, err := pc.Recall(context.Background(), "ws_test", api.RecallRequest{Query: "hello", K: 5}, "fed_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got.Results))
	}
	if got.Results[0].MemoryID != "mem_abc" {
		t.Errorf("unexpected memory id: %s", got.Results[0].MemoryID)
	}
}

func TestPeerClient_Recall_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	pc := &PeerClient{
		peer:         Peer{ID: "peer1", Name: "test-peer", Endpoint: srv.URL},
		httpClient:   srv.Client(),
		federationID: "fed_a",
	}
	_, err := pc.Recall(context.Background(), "ws_test", api.RecallRequest{Query: "fail"}, "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestPeerClient_Lookup(t *testing.T) {
	want := api.MemoryEnvelope{Memory: &types.Memory{ID: "mem_found", WorkspaceID: "ws_test"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/workspaces/ws_test/memories/mem_found" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	pc := &PeerClient{
		peer:         Peer{ID: "peer1", Name: "test-peer", Endpoint: srv.URL},
		httpClient:   srv.Client(),
		federationID: "fed_a",
	}
	got, err := pc.Lookup(context.Background(), "ws_test", "mem_found", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "mem_found" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestPeerClient_Lookup_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	pc := &PeerClient{
		peer:         Peer{ID: "peer1", Name: "test-peer", Endpoint: srv.URL},
		httpClient:   srv.Client(),
		federationID: "fed_a",
	}
	got, err := pc.Lookup(context.Background(), "ws_test", "mem_missing", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for 404")
	}
}

func TestClientPool_NilSafe(t *testing.T) {
	var pool *ClientPool
	if pool.Client("x") != nil {
		t.Error("nil pool should return nil client")
	}
}

func TestNewClientPool_NilRegistry(t *testing.T) {
	pool, err := NewClientPool(nil)
	if err != nil {
		t.Fatal(err)
	}
	if pool != nil {
		t.Error("expected nil pool for nil registry")
	}
}
