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
// parent_context_id migration shim (F11). Like every reserved agent it is
// registered in every workspace (agents are keyed per workspace). The value is fixed and published so
// clients can recognise shim-authored edges.
const AgentLegacyVibeflowID = "6f1c3a2e-9b4d-4e7a-8c5f-0d2b1e3a4c50"

// AgentSystemAutoLinkID is the reserved sentinel agent_id used by the
// auto-link feature (F14) for system-generated vector_neighbor edges.
const AgentSystemAutoLinkID = "2a7e5d1c-8f3b-4c6a-9e0d-4b1f2c3a5d61"

// ConsoleAgentID is the agent the web console writes as: every imprint,
// edit, forget, link and unlink made through the console is attributed
// to it (display name "ui-admin").
const ConsoleAgentID = "8d3b6e1f-4a2c-4f5d-b7e9-0c1a2b3d4e5f"

// ReservedAgentIDs lists all sentinel agent_ids that cannot be
// assigned to user-created agents.
var ReservedAgentIDs = []string{AgentLegacyVibeflowID, AgentSystemAutoLinkID, ConsoleAgentID}

// ReservedAgentDisplayNames are the display names the sentinels are
// registered with; agent IDs carry no meaning of their own.
var ReservedAgentDisplayNames = map[string]string{
	AgentLegacyVibeflowID: "legacy-vibeflow",
	AgentSystemAutoLinkID: "system-auto-link",
	ConsoleAgentID:        "ui-admin",
}

// IsReservedAgentID returns true if the given id is a system sentinel.
func IsReservedAgentID(id string) bool {
	for _, r := range ReservedAgentIDs {
		if id == r {
			return true
		}
	}
	return false
}

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
	if err := parseErr("agent_id", a.AgentID); err != nil {
		return err
	}
	if err := parseErr("workspace_id", a.WorkspaceID); err != nil {
		return err
	}
	if a.IdentityProvider == "" {
		return errEmpty("agent.identity_provider")
	}
	return nil
}
