package identity

import (
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
// OAuthAgent and OIDCAgent are implemented in oauth.go and oidc.go respectively.

func init() {
	adapter.RegisterIdentity(string(types.IdentityProviderA2A), func() adapter.IdentityProvider { return &A2A{} })
	adapter.RegisterIdentity(string(types.IdentityProviderDID), func() adapter.IdentityProvider { return &DID{} })
	adapter.RegisterIdentity(string(types.IdentityProviderOAuthAgent), func() adapter.IdentityProvider { return &OAuthAgent{} })
	adapter.RegisterIdentity(string(types.IdentityProviderOIDCAgent), func() adapter.IdentityProvider { return &OIDCAgent{} })
}
