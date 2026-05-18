package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// HashIdentityProof computes the canonical SHA-256 digest of an
// identity_proof map. Keys are sorted so the digest is stable across
// marshaller implementations. Adapters store this digest, not the
// raw map, so a DB dump never leaks secrets.
func HashIdentityProof(proof map[string]any) string {
	if len(proof) == 0 {
		return ""
	}
	keys := make([]string, 0, len(proof))
	for k := range proof {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		b, _ := json.Marshal(proof[k])
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

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
//
// IdentityProof is never serialized by default (json:"-"); it lives
// only in the adapter row and the verifier's input. Adapters store
// the SHA-256 digest of the proof, not the proof itself, so even a
// DB dump leaks no secrets. A separate IdentityProofHex field carries
// the hex-encoded digest on the wire when callers need to confirm a
// proof was recorded without exposing it.
type Agent struct {
	AgentID           string         `json:"agent_id"`
	WorkspaceID       string         `json:"workspace_id"`
	DisplayName       string         `json:"display_name,omitempty"`
	IdentityProvider  string         `json:"identity_provider"`
	IdentityProof     map[string]any `json:"-"`
	IdentityProofHash string         `json:"identity_proof_hash,omitempty"`
	AgentType         string         `json:"agent_type,omitempty"`
	Model             string         `json:"model,omitempty"`
	Capabilities      map[string]any `json:"capabilities,omitempty"`
	RegisteredAt      time.Time      `json:"registered_at"`
	LastSeenAt        *time.Time     `json:"last_seen_at,omitempty"`
	Active            bool           `json:"active"`
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
