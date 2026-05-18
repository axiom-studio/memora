package types

import "time"

// IdentityProvider is the registered name of an agent-identity provider.
// OSS v0.1 ships: opaque (default), anthropic_session, a2a, did,
// oauth_agent, oidc_agent.
type IdentityProvider string

const (
	IdentityProviderOpaque           IdentityProvider = "opaque"
	IdentityProviderAnthropicSession IdentityProvider = "anthropic_session"
	IdentityProviderA2A              IdentityProvider = "a2a"
	IdentityProviderDID              IdentityProvider = "did"
	IdentityProviderOAuthAgent       IdentityProvider = "oauth_agent"
	IdentityProviderOIDCAgent        IdentityProvider = "oidc_agent"
)

// AgentLegacyVibeflowID is the reserved sentinel agent_id used by the
// parent_context_id migration shim (F11). Auto-registered in every
// workspace at server startup.
const AgentLegacyVibeflowID = "agent_legacy_vibeflow"

// Agent is the entity that wrote a Memory, Cell, Edge, or Ledger entry.
// Separate from user_id and api_key_id so we can attribute writes to
// the agent loop responsible.
type Agent struct {
	AgentID          string         `json:"agent_id"`
	WorkspaceID      string         `json:"workspace_id"`
	DisplayName      string         `json:"display_name,omitempty"`
	IdentityProvider string         `json:"identity_provider"`
	IdentityProof    map[string]any `json:"identity_proof,omitempty"`
	AgentType        string         `json:"agent_type,omitempty"`
	Model            string         `json:"model,omitempty"`
	Capabilities     map[string]any `json:"capabilities,omitempty"`
	RegisteredAt     time.Time      `json:"registered_at"`
	LastSeenAt       *time.Time     `json:"last_seen_at,omitempty"`
	Active           bool           `json:"active"`
}

// Validate returns an error if the agent is malformed.
func (a *Agent) Validate() error {
	if a.AgentID == "" {
		return errEmpty("agent.agent_id")
	}
	if err := MustHavePrefix(a.AgentID, AgentIDPrefix); err != nil {
		return err
	}
	if err := MustHavePrefix(a.WorkspaceID, WorkspaceIDPrefix); err != nil {
		return err
	}
	if a.IdentityProvider == "" {
		return errEmpty("agent.identity_provider")
	}
	return nil
}
