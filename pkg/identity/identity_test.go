package identity

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func TestOpaque_AlwaysVerifies(t *testing.T) {
	p := Opaque{}
	err := p.Verify(context.Background(), adapter.IdentityVerifyInput{AgentID: "agent_opaque_x"})
	if err != nil {
		t.Fatalf("opaque rejected an agent_id: %v", err)
	}
}

func TestAnthropicSession_DeriveDeterministic(t *testing.T) {
	a := DeriveAnthropicSessionID("sess-1", "claude-opus-4-7", "you are a helpful assistant")
	b := DeriveAnthropicSessionID("sess-1", "claude-opus-4-7", "you are a helpful assistant")
	if a != b {
		t.Fatalf("derivation non-deterministic: %s vs %s", a, b)
	}
	if a == "" {
		t.Fatal("empty derivation")
	}
}

func TestAnthropicSession_VerifyHappy(t *testing.T) {
	p := AnthropicSession{}
	derived := DeriveAnthropicSessionID("sess-1", "claude-opus-4-7", "sys")
	err := p.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       derived,
		IdentityProof: map[string]any{"session_id": "sess-1", "model": "claude-opus-4-7", "system_prompt": "sys"},
	})
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
}

func TestAnthropicSession_VerifyMismatchRejected(t *testing.T) {
	p := AnthropicSession{}
	err := p.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_anthropic_session_wrong",
		IdentityProof: map[string]any{"session_id": "sess-1", "model": "claude-opus-4-7", "system_prompt": "sys"},
	})
	if err == nil {
		t.Fatal("expected verification failure on mismatched agent_id")
	}
}
