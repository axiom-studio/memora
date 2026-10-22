package federation

import (
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/internal/certgen"
	"github.com/axiom-studio/memora/internal/config"
)

func TestNewRegistry_Disabled(t *testing.T) {
	r, err := NewRegistry(config.FederationConfig{Enabled: false}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Fatal("expected nil registry when disabled")
	}
}

// TestNewRegistry_SelfNodeIDMustBeUUID: the registry is keyed by node
// IDs, so this node's own ID must be one; federation_id is optional.
func TestNewRegistry_SelfNodeIDMustBeUUID(t *testing.T) {
	if _, err := NewRegistry(config.FederationConfig{Enabled: true}, "self"); err == nil {
		t.Fatal("expected error for a non-UUID self node ID")
	}
	if _, err := NewRegistry(config.FederationConfig{Enabled: true}, nid("self")); err != nil {
		t.Fatalf("no federation_id and no peers should be valid: %v", err)
	}
}

// TestNewRegistry_PeerNeedsNodeID: peers are keyed by node_id.
func TestNewRegistry_PeerNeedsNodeID(t *testing.T) {
	cfg := config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{{Name: "p", URL: "https://p.internal:7777"}}}
	if _, err := NewRegistry(cfg, nid("self")); err == nil {
		t.Fatal("expected error for a peer without node_id")
	}
}

func TestNewRegistry_ValidPeers(t *testing.T) {
	ca, err := certgen.Generate(certgen.Opts{Host: "eu.memora.internal", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{NodeID: nid("us-west"), Name: "us-west", URL: "https://west.memora.internal:7777", APIKey: "key1"},
			{NodeID: nid("eu-central"), Name: "eu-central", URL: "https://eu.memora.internal:7777", TLSCert: ca.CertPath},
		},
	}
	r, err := NewRegistry(cfg, nid("fed_self"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Peers()) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(r.Peers()))
	}
	if p := r.Peer(nid("us-west")); p == nil || p.TrustMode != TrustAPIKey {
		t.Errorf("us-west = %+v, want api_key trust", p)
	}
	if p := r.Peer(nid("eu-central")); p == nil || p.TrustMode != TrustMTLS {
		t.Errorf("eu-central = %+v, want mtls trust", p)
	}
	if bad := (config.PeerConfig{NodeID: nid("x"), Name: "x", URL: "https://x", TLSCert: "/nonexistent/ca.pem"}); r.Validate([]config.PeerConfig{bad}) == nil {
		t.Error("a missing CA file validated")
	}
}

func TestNewRegistry_DuplicateID(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{NodeID: nid("peer1"), Name: "peer1", URL: "https://a.internal:7777"},
			{NodeID: nid("peer1"), Name: "peer1", URL: "https://b.internal:7777"},
		},
	}
	_, err := NewRegistry(cfg, nid("fed_self"))
	if err == nil {
		t.Fatal("expected error for duplicate peer id")
	}
}

func TestNewRegistry_SelfReference(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{NodeID: nid("fed_self"), Name: "fed_self", URL: "https://self.internal:7777"},
		},
	}
	_, err := NewRegistry(cfg, nid("fed_self"))
	if err == nil {
		t.Fatal("expected error for self-reference")
	}
}

func TestNewRegistry_InvalidURL(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{NodeID: nid("bad"), Name: "bad", URL: "ftp://bad.internal"},
		},
	}
	_, err := NewRegistry(cfg, nid("fed_self"))
	if err == nil {
		t.Fatal("expected error for invalid URL scheme")
	}
}

func TestPeerLookup(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_self",
		Peers: []config.PeerConfig{
			{NodeID: nid("peer1"), Name: "peer1", URL: "https://a.internal:7777"},
		},
	}
	r, _ := NewRegistry(cfg, nid("fed_self"))
	p := r.Peer(nid("peer1"))
	if p == nil {
		t.Fatal("expected peer1")
	}
	if p.Endpoint != "https://a.internal:7777" {
		t.Errorf("unexpected endpoint: %s", p.Endpoint)
	}
	if r.Peer(nid("missing")) != nil {
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

// TestRegistry_ReplaceIsAtomic: Replace swaps the whole peer set, and a
// rejected set leaves the registry as it was.
func TestRegistry_ReplaceIsAtomic(t *testing.T) {
	r, err := NewRegistry(config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{
		{NodeID: nid("p1"), Name: "p1", URL: "https://p1.example.com", APIKey: "k"},
	}}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Replace([]config.PeerConfig{
		{NodeID: nid("p2"), Name: "p2", URL: "https://p2.example.com", APIKey: "k"},
	}); err != nil {
		t.Fatal(err)
	}
	if r.Peer(nid("p1")) != nil || r.Client(nid("p1")) != nil || r.Peer(nid("p2")) == nil || r.Client(nid("p2")) == nil {
		t.Fatalf("after Replace: peers %v", r.Peers())
	}
	if err := r.Replace([]config.PeerConfig{
		{NodeID: nid("p3"), Name: "p3", URL: "https://p3.example.com"},
		{NodeID: nid("p3"), Name: "dup", URL: "https://p3.example.com"},
	}); err == nil {
		t.Fatal("a duplicate peer set was accepted")
	}
	if len(r.Peers()) != 1 || r.Peer(nid("p2")) == nil {
		t.Fatalf("a rejected Replace changed the registry: %v", r.Peers())
	}
}

// TestShippedDeployConfigs_PassURLPolicy: the shipped compose demos
// allowlist their http:// peers at private addresses, so the URL policy
// accepts them as copied.
func TestShippedDeployConfigs_PassURLPolicy(t *testing.T) {
	paths, err := filepath.Glob("../../deploy/*/node*.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob deploy configs: %v (found %d)", err, len(paths))
	}
	for _, p := range paths {
		cfg, err := config.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		reg, err := NewRegistry(cfg.Federation, cfg.Node.ID)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if len(reg.Peers()) == 0 {
			t.Errorf("%s: no peers", p)
		}
		cfg.Federation.InsecurePeerHosts, cfg.Federation.PrivatePeerHosts = nil, nil
		if _, err := NewRegistry(cfg.Federation, cfg.Node.ID); err == nil {
			t.Errorf("%s: http:// peers accepted without the allowlists", p)
		}
	}
}
