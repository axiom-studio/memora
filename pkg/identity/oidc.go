package identity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// OIDCAgent verifies agent identity via an OIDC ID Token. The agent
// presents {id_token}; the provider fetches JWKS from the issuer,
// validates the JWT signature, and checks the configured claim matches
// agent_id.
type OIDCAgent struct {
	IssuerURL    string // OIDC issuer (used to fetch .well-known/openid-configuration)
	AgentIDClaim string // claim in the ID token to match (default: "sub")
	Client       *http.Client

	mu       sync.RWMutex
	jwksKeys []jwkKey
	jwksFetched time.Time
}

type openidConfig struct {
	JWKSURI string `json:"jwks_uri"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (o *OIDCAgent) Name() string { return string(types.IdentityProviderOIDCAgent) }

func (o *OIDCAgent) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true}
}

func (o *OIDCAgent) Verify(ctx context.Context, in adapter.IdentityVerifyInput) error {
	if o.IssuerURL == "" {
		return ErrIdentityNotConfigured
	}

	idToken, _ := in.IdentityProof["id_token"].(string)
	if idToken == "" {
		return fmt.Errorf("oidc_agent: identity_proof requires id_token")
	}

	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return fmt.Errorf("oidc_agent: invalid JWT format")
	}

	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		return fmt.Errorf("oidc_agent: invalid JWT header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return fmt.Errorf("oidc_agent: invalid JWT header JSON: %w", err)
	}

	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return fmt.Errorf("oidc_agent: invalid JWT payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return fmt.Errorf("oidc_agent: invalid JWT claims: %w", err)
	}

	if iss, _ := claims["iss"].(string); iss != o.IssuerURL {
		return fmt.Errorf("oidc_agent: issuer %q does not match configured %q", iss, o.IssuerURL)
	}

	if exp, ok := claims["exp"].(float64); ok {
		if time.Unix(int64(exp), 0).Before(time.Now()) {
			return fmt.Errorf("oidc_agent: token expired")
		}
	}

	keys, err := o.fetchJWKS(ctx)
	if err != nil {
		return fmt.Errorf("oidc_agent: fetch JWKS: %w", err)
	}

	sigBytes, err := base64URLDecode(parts[2])
	if err != nil {
		return fmt.Errorf("oidc_agent: invalid JWT signature: %w", err)
	}
	signedContent := parts[0] + "." + parts[1]

	verified := false
	for _, key := range keys {
		if header.Kid != "" && key.Kid != header.Kid {
			continue
		}
		if verifyJWTSignature(header.Alg, key, []byte(signedContent), sigBytes) {
			verified = true
			break
		}
	}
	if !verified {
		return fmt.Errorf("oidc_agent: JWT signature verification failed")
	}

	claimKey := o.AgentIDClaim
	if claimKey == "" {
		claimKey = "sub"
	}
	got, _ := claims[claimKey].(string)
	if got != in.AgentID {
		return fmt.Errorf("oidc_agent: claim %q = %q does not match agent_id %q", claimKey, got, in.AgentID)
	}

	return nil
}

func (o *OIDCAgent) fetchJWKS(ctx context.Context) ([]jwkKey, error) {
	o.mu.RLock()
	if o.jwksKeys != nil && time.Since(o.jwksFetched) < 1*time.Hour {
		keys := o.jwksKeys
		o.mu.RUnlock()
		return keys, nil
	}
	o.mu.RUnlock()

	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	configURL := strings.TrimRight(o.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var cfg openidConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	if cfg.JWKSURI == "" {
		return nil, fmt.Errorf("no jwks_uri in openid-configuration")
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.JWKSURI, nil)
	if err != nil {
		return nil, err
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()

	body2, err := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var jwks jwksResponse
	if err := json.Unmarshal(body2, &jwks); err != nil {
		return nil, err
	}

	o.mu.Lock()
	o.jwksKeys = jwks.Keys
	o.jwksFetched = time.Now()
	o.mu.Unlock()

	return jwks.Keys, nil
}

func verifyJWTSignature(alg string, key jwkKey, signed, sig []byte) bool {
	switch alg {
	case "RS256":
		return verifyRS256(key, signed, sig)
	case "ES256":
		return verifyES256(key, signed, sig)
	default:
		return false
	}
}

func verifyRS256(key jwkKey, signed, sig []byte) bool {
	if key.Kty != "RSA" {
		return false
	}
	nBytes, err := base64URLDecode(key.N)
	if err != nil {
		return false
	}
	eBytes, err := base64URLDecode(key.E)
	if err != nil {
		return false
	}
	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())
	pub := &rsa.PublicKey{N: n, E: e}

	hash := sha256.Sum256(signed)
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, hash[:], sig) == nil
}

func verifyES256(key jwkKey, signed, sig []byte) bool {
	if key.Kty != "EC" || key.Crv != "P-256" {
		return false
	}
	xBytes, err := base64URLDecode(key.X)
	if err != nil {
		return false
	}
	yBytes, err := base64URLDecode(key.Y)
	if err != nil {
		return false
	}
	pub := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}

	hash := sha256.Sum256(signed)

	// ES256 signature is r || s, each 32 bytes
	if len(sig) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, hash[:], r, s)
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}
