package federation

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/certgen"
	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

const (
	peerAKey = "peer-a-inbound-key-0123456789"
	revokKey = "revoked-peer-key-0123456789"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func testRegistry(t *testing.T, extra ...config.PeerConfig) *Registry {
	t.Helper()
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_local",
		Peers: append([]config.PeerConfig{
			{NodeID: nid("peer-a"), Name: "peer-a", URL: "https://a.example.com", APIKey: "k", InboundKeySHA256: sha256Hex(peerAKey)},
			{NodeID: nid("revoked"), Name: "revoked", URL: "https://r.example.com", InboundKeySHA256: sha256Hex(revokKey), Revoked: true},
			{NodeID: nid("outbound-only"), Name: "outbound-only", URL: "https://o.example.com", APIKey: "k"},
		}, extra...),
	}
	reg, err := NewRegistry(cfg, nid("fed_local"))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func peerRequest(nodeID, auth string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/federation/search", nil)
	if nodeID != "" {
		r.Header.Set(HeaderLocalInstanceID, nodeID)
	}
	if auth != "" {
		r.Header.Set(HeaderFederationAuth, auth)
	}
	return r
}

func TestAuthenticatePeer_Valid(t *testing.T) {
	reg := testRegistry(t)
	r := peerRequest(nid("peer-a"), "Bearer "+peerAKey)
	r.Header.Set(HeaderFederationID, "fed_local")

	result, err := AuthenticatePeer(r, reg)
	if err != nil {
		t.Fatal(err)
	}
	if result.PeerID != nid("peer-a") {
		t.Errorf("expected peer-a's node ID, got %s", result.PeerID)
	}
}

// TestAuthenticatePeer_Rejections: every way of failing to prove identity
// is the same 401, and no error message carries a presented key.
func TestAuthenticatePeer_Rejections(t *testing.T) {
	reg := testRegistry(t)
	cases := []struct {
		name, nodeID, auth string
		want               error
	}{
		{"wrong key", nid("peer-a"), "Bearer wrong-key", types.ErrFederationAuth},
		{"missing key", nid("peer-a"), "", types.ErrFederationAuth},
		{"key without Bearer", nid("peer-a"), peerAKey, types.ErrFederationAuth},
		{"empty Bearer", nid("peer-a"), "Bearer ", types.ErrFederationAuth},
		{"another peer's key", nid("peer-a"), "Bearer " + revokKey, types.ErrFederationAuth},
		{"unknown node with a valid key", nid("unknown"), "Bearer " + peerAKey, types.ErrFederationAuth},
		{"missing node ID", "", "Bearer " + peerAKey, types.ErrFederationAuth},
		{"peer with no inbound key", nid("outbound-only"), "Bearer k", types.ErrFederationAuth},
		{"revoked, wrong key", nid("revoked"), "Bearer wrong-key", types.ErrFederationAuth},
		{"revoked, valid key", nid("revoked"), "Bearer " + revokKey, types.ErrFederationRevoked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := AuthenticatePeer(peerRequest(c.nodeID, c.auth), reg)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == types.ErrFederationAuth && errors.Is(err, types.ErrFederationRevoked) {
				t.Fatalf("err = %v: a bad credential must not reveal revocation", err)
			}
			for _, k := range []string{peerAKey, revokKey, "wrong-key"} {
				if strings.Contains(err.Error(), k) {
					t.Fatalf("error %q contains a presented key", err)
				}
			}
		})
	}
}

func TestAuthenticatePeer_NilRegistry(t *testing.T) {
	_, err := AuthenticatePeer(peerRequest(nid("peer-a"), "Bearer "+peerAKey), nil)
	if !errors.Is(err, types.ErrFederationAuth) {
		t.Fatalf("err = %v, want ErrFederationAuth", err)
	}
}

func TestAuthenticatePeer_FedIDMismatch(t *testing.T) {
	reg := testRegistry(t)
	r := peerRequest(nid("peer-a"), "Bearer "+peerAKey)
	r.Header.Set(HeaderFederationID, "fed_other")

	_, err := AuthenticatePeer(r, reg)
	if !errors.Is(err, types.ErrFederationAuth) {
		t.Errorf("expected ErrFederationAuth, got %v", err)
	}
}

func TestAuthenticatePeer_LoopDetected(t *testing.T) {
	reg := testRegistry(t)
	r := peerRequest(nid("peer-a"), "Bearer "+peerAKey)
	r.Header.Set(HeaderFederationPath, nid("fed_local")) // this node's own ID: a loop

	_, err := AuthenticatePeer(r, reg)
	if !errors.Is(err, types.ErrFederationLoop) {
		t.Errorf("expected ErrFederationLoop, got %v", err)
	}
}

// TestAuthenticatePeer_ClientCertPin runs over real TLS: a peer that pins
// a client certificate must present that certificate as well as its key.
func TestAuthenticatePeer_ClientCertPin(t *testing.T) {
	gen := func(host string) (tls.Certificate, string) {
		res, err := certgen.Generate(certgen.Opts{Host: host, OutputDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		c, err := tls.LoadX509KeyPair(res.CertPath, res.KeyPath)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(c.Certificate[0])
		return c, hex.EncodeToString(sum[:])
	}
	pinned, pinnedFP := gen("peer-m")
	other, _ := gen("impostor")

	const key = "mtls-peer-key-0123456789"
	reg := testRegistry(t, config.PeerConfig{
		NodeID: nid("peer-m"), Name: "peer-m", URL: "https://m.example.com",
		InboundKeySHA256: sha256Hex(key), ClientCertSHA256: pinnedFP,
	})
	if !reg.RequiresClientCerts() {
		t.Fatal("RequiresClientCerts = false with a pinned peer")
	}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := AuthenticatePeer(r, reg); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	ts.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	ts.StartTLS()
	defer ts.Close()

	call := func(cert *tls.Certificate) int {
		client := ts.Client()
		tr := client.Transport.(*http.Transport).Clone()
		if cert != nil {
			tr.TLSClientConfig.Certificates = []tls.Certificate{*cert}
		}
		client.Transport = tr
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/federation/search", nil)
		req.Header.Set(HeaderLocalInstanceID, nid("peer-m"))
		req.Header.Set(HeaderFederationAuth, "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := call(&pinned); got != http.StatusOK {
		t.Errorf("pinned cert: status %d, want 200", got)
	}
	if got := call(&other); got != http.StatusUnauthorized {
		t.Errorf("other cert: status %d, want 401", got)
	}
	if got := call(nil); got != http.StatusUnauthorized {
		t.Errorf("no cert: status %d, want 401", got)
	}
}

func TestIsPeerRoute(t *testing.T) {
	cases := map[string]bool{
		"/v1/federation/search":       true,
		"/v1/federation/memories/abc": true,
		"/v1/federation/peers":        false,
		"/v1/federation/peers/x":      false,
		"/v1/federation/search/extra": false,
		"/v1/workspaces/ws/recall":    false,
		"/v1/federation/memories":     false,
		"/v1/search":                  false,
	}
	for path, want := range cases {
		if got := IsPeerRoute(path); got != want {
			t.Errorf("IsPeerRoute(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestNewRegistry_BadInboundHash(t *testing.T) {
	_, err := NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{
			{NodeID: nid("p"), Name: "p", URL: "https://p.example.com", InboundKeySHA256: "not-hex"},
		},
	}, nid("self"))
	if err == nil || !strings.Contains(err.Error(), "inbound_key_sha256") {
		t.Fatalf("err = %v, want an inbound_key_sha256 error", err)
	}
}
