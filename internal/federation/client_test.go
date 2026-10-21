package federation

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/certgen"
	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestPeerClient_Search(t *testing.T) {
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
		if r.URL.Path != "/v1/federation/search" {
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

	got, err := pc.Search(context.Background(), api.FederationSearchRequest{Query: "hello", K: 5}, "fed_a")
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

func TestPeerClient_Search_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	pc := &PeerClient{
		peer:         Peer{ID: "peer1", Name: "test-peer", Endpoint: srv.URL},
		httpClient:   srv.Client(),
		federationID: "fed_a",
	}
	_, err := pc.Search(context.Background(), api.FederationSearchRequest{Query: "fail"}, "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestClientPool_NilSafe(t *testing.T) {
	var pool *ClientPool
	if pool.Client(nid("x")) != nil {
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

// TestPeerClient_KeyAuthenticatesAtPeer round-trips a peer key: the
// calling node's client presents its api_key for the peer, and the peer
// authenticates it against the inbound_key_sha256 it holds. The key is
// sent in mTLS mode too, where it is checked alongside the certificate.
func TestPeerClient_KeyAuthenticatesAtPeer(t *testing.T) {
	const key = "round-trip-peer-key-0123456789"
	caller, callee := nid("caller"), nid("callee")
	calleeReg, err := NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{
			{NodeID: caller, Name: "caller", URL: "https://caller.example.com", InboundKeySHA256: sha256Hex(key)},
		},
	}, callee)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := AuthenticatePeer(r, calleeReg); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RecallResponse{})
	}))
	defer srv.Close()

	for _, mode := range []TrustMode{TrustAPIKey, TrustMTLS} {
		for _, k := range []string{key, "wrong-key"} {
			pc := &PeerClient{
				peer:       Peer{ID: callee, Name: "callee", Endpoint: srv.URL, TrustMode: mode, APIKey: k},
				httpClient: srv.Client(),
				instanceID: caller,
			}
			_, err := pc.Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, "")
			if ok := err == nil; ok != (k == key) {
				t.Errorf("mode %s, key %q: err = %v", mode, k, err)
			}
		}
	}
}

// TestPeerClient_OutboundMTLS: a client built from peer config trusts
// the callee's certificate (tls_cert) and presents its own client
// certificate, which the callee pins (client_cert_sha256). Without the
// client certificate the same key is refused.
func TestPeerClient_OutboundMTLS(t *testing.T) {
	const key = "mtls-round-trip-key-0123456789"
	serverRes, err := certgen.Generate(certgen.Opts{Host: "127.0.0.1", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clientRes, err := certgen.Generate(certgen.Opts{Host: "caller", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err := tls.LoadX509KeyPair(serverRes.CertPath, serverRes.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := tls.LoadX509KeyPair(clientRes.CertPath, clientRes.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	fp := sha256.Sum256(clientCert.Certificate[0])

	caller, callee := nid("caller"), nid("callee")
	calleeReg, err := NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{{
			NodeID: caller, Name: "caller", URL: "https://caller.example.com",
			InboundKeySHA256: sha256Hex(key), ClientCertSHA256: hex.EncodeToString(fp[:]),
		}},
	}, callee)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := AuthenticatePeer(r, calleeReg); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RecallResponse{})
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	defer srv.Close()

	callerReg, err := NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{
			{NodeID: callee, Name: "with-cert", URL: srv.URL, APIKey: key, TLSCert: serverRes.CertPath,
				ClientCertFile: clientRes.CertPath, ClientKeyFile: clientRes.KeyPath},
		},
	}, caller)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewClientPool(callerReg)
	if err != nil {
		t.Fatal(err)
	}
	pc := pool.clients[callee]
	if _, err := pc.Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, ""); err != nil {
		t.Fatalf("with client cert: %v", err)
	}

	noCert := callerReg.Peers()[0]
	noCert.ClientCertFile, noCert.ClientKeyFile = "", ""
	pc2, err := newPeerClient(noCert, "", caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc2.Search(context.Background(), api.FederationSearchRequest{Query: "q", K: 1}, ""); err == nil {
		t.Fatal("without client cert: want 401, got success")
	}
}

func TestNewClientPool_BadClientCert(t *testing.T) {
	reg, err := NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{{NodeID: nid("p"), Name: "p", URL: "https://p.example.com",
			ClientCertFile: "/nonexistent/cert.pem", ClientKeyFile: "/nonexistent/key.pem"}},
	}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClientPool(reg); err == nil || !strings.Contains(err.Error(), "client cert") {
		t.Fatalf("err = %v, want a client cert load error", err)
	}
}
