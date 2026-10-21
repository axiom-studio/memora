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

// IdentityKeyer is implemented by providers whose identity comes from
// the proof itself (a session fingerprint, a token claim) rather than
// from an agent_id the client chose. Agent IDs are UUIDv4s, which an
// external claim never is, so the server maps each identity key to a
// UUIDv4 agent_id minted on first sight (MetadataStore.ResolveIdentityKey).
//
// IdentityKey verifies the proof and returns the key: provider-scoped,
// stable for the same identity, and never empty on success. For these
// providers Verify checks the proof only; binding the key to an
// agent_id is the server's job.
type IdentityKeyer interface {
	IdentityKey(ctx context.Context, proof map[string]any) (string, error)
}

// IdentityFactory builds an IdentityProvider.
type IdentityFactory func() IdentityProvider
