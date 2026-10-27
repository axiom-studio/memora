package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestCheckConfig_FederationPeers (#6230): check-config refuses the peer
// configs serve refuses, and accepts the ones it starts with.
func TestCheckConfig_FederationPeers(t *testing.T) {
	self, peer := types.NewID(), types.NewID()
	base := func(peers ...config.PeerConfig) config.Config {
		c := config.Defaults()
		c.Node.ID = self
		c.Federation = config.FederationConfig{Enabled: true, Peers: peers}
		return c
	}
	hash := strings.Repeat("ab", 32)
	cases := map[string]struct {
		cfg  config.Config
		want string // "" = ok
	}{
		"https public peer": {cfg: base(config.PeerConfig{NodeID: peer, Name: "p", URL: "https://peer.example.com"})},
		"cleartext http":    {cfg: base(config.PeerConfig{NodeID: peer, Name: "p", URL: "http://memora-node2:7777"}), want: "cleartext"},
		"private address":   {cfg: base(config.PeerConfig{NodeID: peer, Name: "p", URL: "https://10.0.0.5:7777"}), want: "not public"},
		"self reference":    {cfg: base(config.PeerConfig{NodeID: self, Name: "me", URL: "https://peer.example.com"}), want: "self-reference"},
		"duplicate inbound key": {cfg: base(
			config.PeerConfig{NodeID: peer, Name: "p1", URL: "https://p1.example.com", InboundKeySHA256: hash},
			config.PeerConfig{NodeID: types.NewID(), Name: "p2", URL: "https://p2.example.com", InboundKeySHA256: hash},
		), want: "own key"},
		"missing CA file":        {cfg: base(config.PeerConfig{NodeID: peer, Name: "p", URL: "https://peer.example.com", TLSCert: "/nonexistent/ca.pem"}), want: "read ca cert"},
		"pin without server.tls": {cfg: base(config.PeerConfig{NodeID: peer, Name: "p", URL: "https://peer.example.com", ClientCertSHA256: hash}), want: "server.tls"},
	}
	allowed := base(config.PeerConfig{NodeID: peer, Name: "p", URL: "http://memora-node2:7777"})
	allowed.Federation.InsecurePeerHosts = []string{"memora-node2"}
	allowed.Federation.PrivatePeerHosts = []string{"memora-node2"}
	cases["allowlisted http peer"] = struct {
		cfg  config.Config
		want string
	}{cfg: allowed}

	for name, c := range cases {
		err := checkConfig(c.cfg)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want ok", name, err)
		case c.want != "" && (err == nil || !strings.Contains(strings.ToLower(err.Error()), c.want)):
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, c.want)
		}
	}
}

// TestCheckConfig_ShippedDeployConfigs: the shipped node configs pass
// check-config, peers included.
func TestCheckConfig_ShippedDeployConfigs(t *testing.T) {
	paths, _ := filepath.Glob("../../deploy/*/node*.toml")
	if len(paths) == 0 {
		t.Fatal("no deploy configs found")
	}
	for _, p := range paths {
		cfg, err := config.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkConfig(cfg); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// TestCheckConfig_HidesPeerURLCredentials (#6247).
func TestCheckConfig_HidesPeerURLCredentials(t *testing.T) {
	for _, u := range []string{"https://u:hunter2@peer.example.com", "https://u:hunter2 @bad host/%zz"} {
		c := config.Defaults()
		c.Federation = config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{{NodeID: types.NewID(), Name: "p", URL: u}}}
		err := checkConfig(c)
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: err = %v, want a refusal without the password", u, err)
		}
	}
}
