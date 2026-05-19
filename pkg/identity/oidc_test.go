package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func makeJWT(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signed := b64url(hb) + "." + b64url(cb)
	hash := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + b64url(sig)
}

func rsaJWK(pub *rsa.PublicKey, kid string) jwkKey {
	return jwkKey{
		Kid: kid,
		Kty: "RSA",
		Alg: "RS256",
		N:   b64url(pub.N.Bytes()),
		E:   b64url(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func setupOIDCServer(t *testing.T, jwks jwksResponse) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srvURL string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openidConfig{JWKSURI: srvURL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	})

	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	return srv
}

func TestOIDCAgent_VerifySuccess(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "test-key-1"
	jwks := jwksResponse{Keys: []jwkKey{rsaJWK(&priv.PublicKey, kid)}}

	srv := setupOIDCServer(t, jwks)
	defer srv.Close()

	token := makeJWT(t, priv, kid, map[string]any{
		"iss": srv.URL,
		"sub": "agent_oidc_test",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	o := &OIDCAgent{IssuerURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_oidc_test",
		IdentityProof: map[string]any{"id_token": token},
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestOIDCAgent_ExpiredToken(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "test-key-1"
	jwks := jwksResponse{Keys: []jwkKey{rsaJWK(&priv.PublicKey, kid)}}

	srv := setupOIDCServer(t, jwks)
	defer srv.Close()

	token := makeJWT(t, priv, kid, map[string]any{
		"iss": srv.URL,
		"sub": "agent_test",
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()),
	})

	o := &OIDCAgent{IssuerURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"id_token": token},
	})
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestOIDCAgent_WrongIssuer(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "test-key-1"
	jwks := jwksResponse{Keys: []jwkKey{rsaJWK(&priv.PublicKey, kid)}}

	srv := setupOIDCServer(t, jwks)
	defer srv.Close()

	token := makeJWT(t, priv, kid, map[string]any{
		"iss": "https://evil.example.com",
		"sub": "agent_test",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	o := &OIDCAgent{IssuerURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"id_token": token},
	})
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestOIDCAgent_BadSignature(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "test-key-1"
	jwks := jwksResponse{Keys: []jwkKey{rsaJWK(&priv.PublicKey, kid)}}

	srv := setupOIDCServer(t, jwks)
	defer srv.Close()

	// Sign with different key
	token := makeJWT(t, otherPriv, kid, map[string]any{
		"iss": srv.URL,
		"sub": "agent_test",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	o := &OIDCAgent{IssuerURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"id_token": token},
	})
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
}

func TestOIDCAgent_AgentIDMismatch(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "test-key-1"
	jwks := jwksResponse{Keys: []jwkKey{rsaJWK(&priv.PublicKey, kid)}}

	srv := setupOIDCServer(t, jwks)
	defer srv.Close()

	token := makeJWT(t, priv, kid, map[string]any{
		"iss": srv.URL,
		"sub": "agent_different",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	o := &OIDCAgent{IssuerURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_expected",
		IdentityProof: map[string]any{"id_token": token},
	})
	if err == nil {
		t.Fatal("expected error for agent_id mismatch")
	}
}

func TestOIDCAgent_NotConfigured(t *testing.T) {
	o := &OIDCAgent{}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"id_token": "x.y.z"},
	})
	if err != ErrIdentityNotConfigured {
		t.Fatalf("expected ErrIdentityNotConfigured, got: %v", err)
	}
}

func TestOIDCAgent_InvalidJWT(t *testing.T) {
	o := &OIDCAgent{IssuerURL: "http://localhost"}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"id_token": "not-a-jwt"},
	})
	if err == nil {
		t.Fatal("expected error for invalid JWT")
	}
	_ = fmt.Sprintf("%v", err) // ensure error is printable
}
