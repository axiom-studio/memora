package federation

import (
	"testing"

	"github.com/axiom-studio/memora/internal/config"
)

func TestNewRegistry_Disabled(t *testing.T) {
	r, err := NewRegistry(config.FederationConfig{Enabled: false}, "self")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Fatal("expected nil registry when disabled")
	}
}

func TestNewRegistry_MissingFederationID(t *testing.T) {
	_, err := NewRegistry(config.FederationConfig{Enabled: true}, "self")
	if err == nil {
		t.Fatal("expected error for missing federation_id")
	}
}

func TestNewRegistry_ValidPeers(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "us-west", URL: "https://west.memora.internal:7777", APIKey: "key1"},
			{Name: "eu-central", URL: "https://eu.memora.internal:7777", TLSCert: "/path/cert"},
		},
	}
	r, err := NewRegistry(cfg, "fed_self")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Peers()) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(r.Peers()))
	}
	if r.Peers()[0].TrustMode != TrustAPIKey {
		t.Errorf("expected api_key trust, got %s", r.Peers()[0].TrustMode)
	}
	if r.Peers()[1].TrustMode != TrustMTLS {
		t.Errorf("expected mtls trust, got %s", r.Peers()[1].TrustMode)
	}
}

func TestNewRegistry_DuplicateID(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "peer1", URL: "https://a.internal:7777"},
			{Name: "peer1", URL: "https://b.internal:7777"},
		},
	}
	_, err := NewRegistry(cfg, "fed_self")
	if err == nil {
		t.Fatal("expected error for duplicate peer id")
	}
}

func TestNewRegistry_SelfReference(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "fed_self", URL: "https://self.internal:7777"},
		},
	}
	_, err := NewRegistry(cfg, "fed_self")
	if err == nil {
		t.Fatal("expected error for self-reference")
	}
}

func TestNewRegistry_InvalidURL(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "bad", URL: "ftp://bad.internal"},
		},
	}
	_, err := NewRegistry(cfg, "fed_self")
	if err == nil {
		t.Fatal("expected error for invalid URL scheme")
	}
}

func TestPeersForWorkspace_AllWorkspaces(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "peer1", URL: "https://a.internal:7777"},
		},
	}
	r, _ := NewRegistry(cfg, "fed_self")
	peers := r.PeersForWorkspace("ws_any")
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer (no allowlist = all), got %d", len(peers))
	}
}

func TestIsAuthorizedPeer_Known(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "peer1", URL: "https://a.internal:7777"},
		},
	}
	r, _ := NewRegistry(cfg, "fed_self")
	if !r.IsAuthorizedPeer("peer1", "ws_any") {
		t.Error("expected peer1 to be authorized")
	}
	if r.IsAuthorizedPeer("unknown", "ws_any") {
		t.Error("expected unknown peer to be unauthorized")
	}
}

func TestIsAuthorizedPeer_NilRegistry(t *testing.T) {
	var r *Registry
	if r.IsAuthorizedPeer("peer1", "ws") {
		t.Error("nil registry should always return false")
	}
}

func TestPeersForWorkspace_NilRegistry(t *testing.T) {
	var r *Registry
	if len(r.PeersForWorkspace("ws")) != 0 {
		t.Error("nil registry should return empty")
	}
}

func TestPeerLookup(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{Name: "peer1", URL: "https://a.internal:7777"},
		},
	}
	r, _ := NewRegistry(cfg, "fed_self")
	p := r.Peer("peer1")
	if p == nil {
		t.Fatal("expected peer1")
	}
	if p.Endpoint != "https://a.internal:7777" {
		t.Errorf("unexpected endpoint: %s", p.Endpoint)
	}
	if r.Peer("missing") != nil {
		t.Error("expected nil for missing peer")
	}
}

func TestDefaultRequestTimeout(t *testing.T) {
	p := Peer{RequestTimeoutMS: 3000}
	if p.DefaultRequestTimeout().Milliseconds() != 3000 {
		t.Errorf("expected 3000ms, got %v", p.DefaultRequestTimeout())
	}
	p2 := Peer{}
	if p2.DefaultRequestTimeout().Milliseconds() != 5000 {
		t.Errorf("expected default 5000ms, got %v", p2.DefaultRequestTimeout())
	}
}
