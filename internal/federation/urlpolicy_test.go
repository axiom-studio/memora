package federation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestURLPolicy_PeerURLs: without operator allowlists, only https peers
// at public addresses are accepted; http needs a loopback host or
// insecure_peer_hosts, and non-public addresses need private_peer_hosts.
func TestURLPolicy_PeerURLs(t *testing.T) {
	cases := []struct {
		url      string
		insecure []string
		private  []string
		ok       bool
	}{
		{url: "https://peer.example.com:7777", ok: true},
		{url: "https://203.0.113.9", ok: false}, // documentation (TEST-NET-3)
		{url: "https://8.8.8.8", ok: true},
		{url: "https://[2606:4700::1111]", ok: true},
		{url: "http://peer.example.com", ok: false},
		{url: "http://peer.example.com", insecure: []string{"peer.example.com"}, ok: true},
		{url: "http://PEER.example.com", insecure: []string{"peer.example.com"}, ok: true},
		{url: "http://other.example.com", insecure: []string{"peer.example.com"}, ok: false},
		{url: "http://localhost:7777", ok: true}, // cleartext to loopback; the dial check still applies
		{url: "http://127.0.0.1:7777", ok: false},
		{url: "http://127.0.0.1:7777", private: []string{"127.0.0.1"}, ok: true},
		{url: "http://169.254.169.254", ok: false},
		{url: "https://169.254.169.254", ok: false},
		{url: "https://10.1.2.3", ok: false},
		{url: "https://10.1.2.3", private: []string{"10.0.0.0/8"}, ok: true},
		{url: "https://10.1.2.3", private: []string{"192.168.0.0/16"}, ok: false},
		{url: "https://172.16.0.1", ok: false},
		{url: "https://192.168.1.1", ok: false},
		{url: "https://100.64.0.1", ok: false},
		{url: "https://0.0.0.0", ok: false},
		{url: "https://[::1]", ok: false},
		{url: "https://[::ffff:127.0.0.1]", ok: false},
		{url: "https://[::ffff:10.0.0.1]", ok: false},
		{url: "https://[fe80::1]", ok: false},
		{url: "https://[fd00::1]", ok: false},
		{url: "https://[fd00::1]", private: []string{"fd00::/8"}, ok: true},
		{url: "https://[64:ff9b::a9fe:a9fe]", ok: false}, // NAT64 of 169.254.169.254
		{url: "https://user:pw@peer.example.com", ok: false},
		{url: "ftp://peer.example.com", ok: false},
		{url: "https://", ok: false},
	}
	for _, c := range cases {
		p, err := newURLPolicy(config.FederationConfig{InsecurePeerHosts: c.insecure, PrivatePeerHosts: c.private})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.checkURL(c.url)
		if ok := err == nil; ok != c.ok {
			t.Errorf("%s (insecure %v, private %v): err = %v, want ok=%v", c.url, c.insecure, c.private, err, c.ok)
		}
	}
	if _, err := newURLPolicy(config.FederationConfig{PrivatePeerHosts: []string{"10.0.0.0/33"}}); err == nil {
		t.Error("a bad CIDR in private_peer_hosts was accepted")
	}
}

// TestRegistry_CheckPeerURL_Resolves: a runtime peer whose hostname
// resolves to an internal address is refused when it is added.
func TestRegistry_CheckPeerURL_Resolves(t *testing.T) {
	reg, err := NewRegistry(config.FederationConfig{Enabled: true}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	err = reg.CheckPeerURL(context.Background(), "https://localhost:7777")
	if !errors.Is(err, ErrPeerAddressRefused) {
		t.Fatalf("localhost: err = %v, want ErrPeerAddressRefused", err)
	}
	allowed, err := NewRegistry(config.FederationConfig{Enabled: true, PrivatePeerHosts: []string{"localhost"}}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	if err := allowed.CheckPeerURL(context.Background(), "https://localhost:7777"); err != nil {
		t.Fatalf("allowlisted localhost: %v", err)
	}
	if err := reg.CheckPeerURL(context.Background(), "https://8.8.8.8"); err != nil {
		t.Fatalf("public IP: %v", err)
	}
}

// TestPeerClient_DialRefusesInternalAddress: the address check runs on
// every connection, after name resolution, so a name that passed when
// the peer was added and now resolves to an internal address (DNS
// rebinding) is never connected to.
func TestPeerClient_DialRefusesInternalAddress(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	// "localhost" passes the URL check (a name, cleartext to loopback is
	// allowed) but resolves to 127.0.0.1 when dialed.
	reg, err := NewRegistry(config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{
		{NodeID: nid("peer"), Name: "peer", URL: "http://localhost" + port, APIKey: "k"},
	}}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	c := reg.Client(nid("peer"))
	_, err = c.Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, "")
	if !errors.Is(err, ErrPeerAddressRefused) || PeerErrorCode(err) != "peer_address_refused" {
		t.Fatalf("search: err = %v (code %q), want peer_address_refused", err, PeerErrorCode(err))
	}
	if _, err := c.FetchMemory(context.Background(), nid("mem"), ""); !errors.Is(err, ErrPeerAddressRefused) {
		t.Fatalf("fetch: err = %v, want ErrPeerAddressRefused", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the internal server received %d requests", n)
	}

	// The same peer with the operator's allowlist connects.
	ok, err := NewRegistry(config.FederationConfig{Enabled: true, PrivatePeerHosts: []string{"127.0.0.0/8", "::1/128"}, Peers: []config.PeerConfig{
		{NodeID: nid("peer"), Name: "peer", URL: "http://localhost" + port, APIKey: "k"},
	}}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ok.Client(nid("peer")).Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, "")
	if errors.Is(err, ErrPeerAddressRefused) || hits.Load() == 0 {
		t.Fatalf("allowlisted: err = %v, hits %d; want the request to reach the server", err, hits.Load())
	}
}

// TestPeerClient_NoKeyOverCleartext: a client never puts the peer key on
// an http:// request unless the policy allowed cleartext for its host.
func TestPeerClient_NoKeyOverCleartext(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get(HeaderFederationAuth))
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	pc := &PeerClient{peer: Peer{ID: nid("p"), Name: "p", Endpoint: srv.URL, APIKey: "secret"}, httpClient: srv.Client()}
	if _, err := pc.Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if v := got.Load(); v != "" {
		t.Fatalf("key sent over cleartext: %q", v)
	}
}

// TestRegistry_InboundKeyUnique: one inbound key cannot authenticate as
// two peers.
func TestRegistry_InboundKeyUnique(t *testing.T) {
	h := sha256Hex("shared-key")
	_, err := NewRegistry(config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{
		{NodeID: nid("p1"), Name: "p1", URL: "https://p1.example.com", InboundKeySHA256: h},
		{NodeID: nid("p2"), Name: "p2", URL: "https://p2.example.com", InboundKeySHA256: strings.ToUpper(h)},
	}}, nid("self"))
	if err == nil || !strings.Contains(err.Error(), "own key") {
		t.Fatalf("err = %v, want a duplicate inbound key error", err)
	}
}

// TestURLPolicy_ErrorsHideCredentials (#6247): no policy error repeats
// a credential from the URL, whether it parses or not.
func TestURLPolicy_ErrorsHideCredentials(t *testing.T) {
	p, err := newURLPolicy(config.FederationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"https://peer:hunter2@peer.example.com",
		"http://hunter2@peer.example.com",           // token as user name
		"https://u:hunter2@10.0.0.5",                // also a private address
		"ftp://u:hunter2@peer.example.com",          // bad scheme
		"https://u:hunter2@peer.example.com:99999x", // does not parse
		"https://u:hunter2 @bad host/%zz",           // does not parse
	} {
		_, err := p.checkURL(raw)
		if err == nil {
			t.Errorf("%q accepted", raw)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: error shows the credential: %v", raw, err)
		}
	}
}
