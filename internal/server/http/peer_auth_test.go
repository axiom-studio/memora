package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/pkg/types"
)

const (
	testAPIKey   = "operator-api-key-0123456789"
	testPeerKey  = "peer-inbound-key-0123456789"
	testRevoKey  = "revoked-peer-key-0123456789"
	testPeerName = "peer-a"
)

var (
	testSelfNode    = types.NewID()
	testPeerNode    = types.NewID()
	testRevokedNode = types.NewID()
)

func keyHash(k string) string {
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:])
}

func peerTestRegistry(t *testing.T) *federation.Registry {
	t.Helper()
	reg, err := federation.NewRegistry(config.FederationConfig{
		Enabled: true,
		Peers: []config.PeerConfig{
			{NodeID: testPeerNode, Name: testPeerName, URL: "https://a.example.com", InboundKeySHA256: keyHash(testPeerKey)},
			{NodeID: testRevokedNode, Name: "revoked", URL: "https://r.example.com", InboundKeySHA256: keyHash(testRevoKey), Revoked: true},
		},
	}, testSelfNode)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestPeerRouteAuth drives the server middleware: the routes peers call
// take peer credentials instead of the API key, the operator routes keep
// the API key, and the authenticated peer reaches the handler.
func TestPeerRouteAuth(t *testing.T) {
	var logBuf bytes.Buffer
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := peerFrom(r.Context()); p != nil {
			_, _ = io.WriteString(w, "peer:"+p.PeerID)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})

	cases := []struct {
		name            string
		apiKey          string
		noFederation    bool
		path, node, key string
		bearer          string
		want            int
		wantCode        string
	}{
		{name: "peer key, no API key", apiKey: testAPIKey, path: "/v1/federation/search", node: testPeerNode, key: testPeerKey, want: 200},
		{name: "memory fetch route", apiKey: testAPIKey, path: "/v1/federation/memories/" + types.NewID(), node: testPeerNode, key: testPeerKey, want: 200},
		// Not routed until F-7: the route table's 404 comes after auth,
		// so reaching it shows the API key was accepted.
		{name: "wrong peer key", apiKey: testAPIKey, path: "/v1/federation/search", node: testPeerNode, key: "wrong-key", want: 401, wantCode: "federation_unauthorized"},
		{name: "unknown node", apiKey: testAPIKey, path: "/v1/federation/search", node: types.NewID(), key: testPeerKey, want: 401, wantCode: "federation_unauthorized"},
		{name: "revoked peer", apiKey: testAPIKey, path: "/v1/federation/search", node: testRevokedNode, key: testRevoKey, want: 403, wantCode: "federation_peer_revoked"},
		{name: "API key does not open a peer route", apiKey: testAPIKey, path: "/v1/federation/search", bearer: testAPIKey, want: 401, wantCode: "federation_unauthorized"},
		{name: "no-auth mode still needs peer credentials", path: "/v1/federation/search", want: 401, wantCode: "federation_unauthorized"},
		{name: "peer key does not open an operator route", apiKey: testAPIKey, path: "/v1/federation/peers", node: testPeerNode, key: testPeerKey, want: 401, wantCode: "unauthorized"},
		{name: "operator route takes the API key", apiKey: testAPIKey, path: "/v1/federation/peers", bearer: testAPIKey, want: 404, wantCode: "not_found"},
		{name: "federation disabled", apiKey: testAPIKey, noFederation: true, path: "/v1/federation/search", node: testPeerNode, key: testPeerKey, want: 404, wantCode: "federation_disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testServer(c.apiKey)
			s.cfg.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
			if !c.noFederation {
				s.cfg.Federation = peerTestRegistry(t)
			}
			method := http.MethodPost
			if strings.HasPrefix(c.path, "/v1/federation/memories/") {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, c.path, nil)
			if c.node != "" {
				req.Header.Set(federation.HeaderLocalInstanceID, c.node)
			}
			if c.key != "" {
				req.Header.Set(federation.HeaderFederationAuth, "Bearer "+c.key)
			}
			if c.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+c.bearer)
			}
			rec := httptest.NewRecorder()
			s.middleware(next).ServeHTTP(rec, req)

			body := rec.Body.String()
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.want, body)
			}
			if c.wantCode != "" && !strings.Contains(body, `"`+c.wantCode+`"`) {
				t.Fatalf("body %s lacks error code %q", body, c.wantCode)
			}
			if c.want == 200 && c.key != "" && body != "peer:"+c.node {
				t.Fatalf("handler saw %q, want the authenticated peer %s", body, c.node)
			}
			for _, k := range []string{testPeerKey, testRevoKey, "wrong-key"} {
				if strings.Contains(body, k) {
					t.Fatalf("response contains a peer key: %s", body)
				}
			}
		})
	}
	for _, k := range []string{testPeerKey, testRevoKey, "wrong-key", testAPIKey} {
		if strings.Contains(logBuf.String(), k) {
			t.Fatalf("server log contains a credential: %s", logBuf.String())
		}
	}
}
