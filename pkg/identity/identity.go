// Package identity provides the OSS Memora agent-identity providers
// that satisfy adapter.IdentityProvider. OSS v0.1 ships:
//   - opaque             — accepts any agent_id (default)
//   - anthropic_session  — derives + verifies a Claude session digest
//
// a2a / did / oauth_agent / oidc_agent stubs live alongside but their
// network-dependent verification logic ships in v0.3 (#297 §17).
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// ----- opaque -----

type Opaque struct{}

func (Opaque) Name() string { return string(types.IdentityProviderOpaque) }
func (Opaque) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: false, SupportsRotation: false}
}
func (Opaque) Verify(_ context.Context, _ adapter.IdentityVerifyInput) error {
	return nil
}

// ----- anthropic_session -----

// AnthropicSession derives agent_id from session metadata. The recipe
// (per #297 §17.4):
//
//	agent_id = "agent_anthropic_session_" + base32(
//	    sha256(session_id + ":" + model + ":" + sha256(system_prompt))[:16]
//	)
type AnthropicSession struct{}

func (AnthropicSession) Name() string { return string(types.IdentityProviderAnthropicSession) }
func (AnthropicSession) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: false}
}

// Verify checks the supplied identity_proof reproduces the agent_id.
func (a AnthropicSession) Verify(_ context.Context, in adapter.IdentityVerifyInput) error {
	sessionID, _ := in.IdentityProof["session_id"].(string)
	model, _ := in.IdentityProof["model"].(string)
	systemPrompt, _ := in.IdentityProof["system_prompt"].(string)
	if sessionID == "" || model == "" {
		return fmt.Errorf("anthropic_session: identity_proof requires session_id and model")
	}
	want := DeriveAnthropicSessionID(sessionID, model, systemPrompt)
	if !strings.EqualFold(want, in.AgentID) {
		return fmt.Errorf("anthropic_session: agent_id %q does not match derivation %q", in.AgentID, want)
	}
	return nil
}

// DeriveAnthropicSessionID returns the canonical agent_id for the
// given Anthropic session metadata.
func DeriveAnthropicSessionID(sessionID, model, systemPrompt string) string {
	promptHash := sha256.Sum256([]byte(systemPrompt))
	src := sessionID + ":" + model + ":" + string(promptHash[:])
	full := sha256.Sum256([]byte(src))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(full[:])
	if len(enc) > 16 {
		enc = enc[:16]
	}
	return "agent_anthropic_session_" + strings.ToLower(enc)
}

func init() {
	adapter.RegisterIdentity(string(types.IdentityProviderOpaque), func() adapter.IdentityProvider { return Opaque{} })
	adapter.RegisterIdentity(string(types.IdentityProviderAnthropicSession), func() adapter.IdentityProvider { return AnthropicSession{} })
}
