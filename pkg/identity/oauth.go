package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// OAuthAgent verifies agent identity via OAuth 2.0 token introspection
// (RFC 7662). The agent presents {access_token}; the provider calls
// the configured introspection endpoint and checks that the token is
// active and the client_id matches the agent_id.
type OAuthAgent struct {
	IntrospectionURL string // RFC 7662 introspection endpoint
	ClientID         string // OAuth client_id for authenticating to the introspection endpoint
	ClientSecret     string // OAuth client_secret
	AgentIDClaim     string // token field to match against agent_id (default: "client_id")
	Client           *http.Client
}

// IntrospectionResponse is the subset of RFC 7662 response we need.
type IntrospectionResponse struct {
	Active   bool   `json:"active"`
	ClientID string `json:"client_id"`
	Sub      string `json:"sub"`
	Scope    string `json:"scope"`
}

func (o *OAuthAgent) Name() string { return string(types.IdentityProviderOAuthAgent) }

func (o *OAuthAgent) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true}
}

func (o *OAuthAgent) Verify(ctx context.Context, in adapter.IdentityVerifyInput) error {
	if o.IntrospectionURL == "" {
		return ErrIdentityNotConfigured
	}

	token, _ := in.IdentityProof["access_token"].(string)
	if token == "" {
		return fmt.Errorf("oauth_agent: identity_proof requires access_token")
	}

	resp, err := o.introspect(ctx, token)
	if err != nil {
		return fmt.Errorf("oauth_agent: introspection failed: %w", err)
	}

	if !resp.Active {
		return fmt.Errorf("oauth_agent: token is not active")
	}

	claim := o.AgentIDClaim
	if claim == "" {
		claim = "client_id"
	}

	var got string
	switch claim {
	case "client_id":
		got = resp.ClientID
	case "sub":
		got = resp.Sub
	default:
		got = resp.ClientID
	}

	if got != in.AgentID {
		return fmt.Errorf("oauth_agent: token %s %q does not match agent_id %q", claim, got, in.AgentID)
	}

	return nil
}

func (o *OAuthAgent) introspect(ctx context.Context, token string) (*IntrospectionResponse, error) {
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.IntrospectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if o.ClientID != "" {
		req.SetBasicAuth(o.ClientID, o.ClientSecret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var ir IntrospectionResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return nil, fmt.Errorf("invalid introspection response: %w", err)
	}

	return &ir, nil
}
