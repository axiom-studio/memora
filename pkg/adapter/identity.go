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
	// Name returns the provider identifier (e.g. "opaque", "anthropic_session").
	Name() string
	// Verify checks that the agent_id is valid given the identity_proof. Returns nil on success, error on mismatch or missing proof.
	Verify(ctx context.Context, input IdentityVerifyInput) error
	// Capabilities reports whether this provider requires proof material and supports key rotation.
	Capabilities() IdentityCapabilities
}

// IdentityFactory builds an IdentityProvider.
type IdentityFactory func() IdentityProvider
