package identity

import (
	"context"
	"errors"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// The standards-based providers below are scaffolded so deployers
// can wire them in v0.3 without an API churn; the verification logic
// requires external infrastructure (DID resolver, OAuth introspection
// endpoint, JWKS for OIDC, AgentCard registry signature key) that
// doesn't make sense to embed at the OSS default. Until the deployer
// supplies the relevant config, Verify returns ErrIdentityNotConfigured.

// ErrIdentityNotConfigured signals that a registered identity provider
// has no external infrastructure wired and must be reconfigured by the
// deployer before it can accept writes.
var ErrIdentityNotConfigured = errors.New("identity provider not configured (deployer must wire external infrastructure)")

// A2A and DID are implemented in a2a.go and did.go respectively.

// OAuthAgent — OAuth 2.0 Client Credentials with optional RAR.
type OAuthAgent struct{}

func (OAuthAgent) Name() string                                       { return string(types.IdentityProviderOAuthAgent) }
func (OAuthAgent) Capabilities() adapter.IdentityCapabilities         { return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true} }
func (OAuthAgent) Verify(_ context.Context, _ adapter.IdentityVerifyInput) error {
	return ErrIdentityNotConfigured
}

// OIDCAgent — OpenID Connect for Agents.
type OIDCAgent struct{}

func (OIDCAgent) Name() string                                       { return string(types.IdentityProviderOIDCAgent) }
func (OIDCAgent) Capabilities() adapter.IdentityCapabilities         { return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true} }
func (OIDCAgent) Verify(_ context.Context, _ adapter.IdentityVerifyInput) error {
	return ErrIdentityNotConfigured
}

func init() {
	adapter.RegisterIdentity(string(types.IdentityProviderA2A), func() adapter.IdentityProvider { return &A2A{} })
	adapter.RegisterIdentity(string(types.IdentityProviderDID), func() adapter.IdentityProvider { return &DID{} })
	adapter.RegisterIdentity(string(types.IdentityProviderOAuthAgent), func() adapter.IdentityProvider { return OAuthAgent{} })
	adapter.RegisterIdentity(string(types.IdentityProviderOIDCAgent), func() adapter.IdentityProvider { return OIDCAgent{} })
}
