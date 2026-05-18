package adapter

import "context"

// IdentityCapabilities advertises what an identity provider supports.
type IdentityCapabilities struct {
	RequiresProof    bool `json:"requires_proof"`
	SupportsRotation bool `json:"supports_rotation"`
}

// IdentityVerifyInput captures the bits of a write request that an
// IdentityProvider needs to verify.
type IdentityVerifyInput struct {
	AgentID       string
	IdentityProof map[string]any
	RequestID     string
}

// IdentityProvider verifies an agent_id matches an identity_proof.
// Built-in providers: opaque (no verification, default),
// anthropic_session, a2a, did, oauth_agent, oidc_agent.
type IdentityProvider interface {
	Name() string
	Verify(ctx context.Context, input IdentityVerifyInput) error
	Capabilities() IdentityCapabilities
}

// IdentityFactory builds an IdentityProvider.
type IdentityFactory func() IdentityProvider
