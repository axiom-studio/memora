package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func TestOpaque_AlwaysVerifies(t *testing.T) {
	p := Opaque{}
	err := p.Verify(context.Background(), adapter.IdentityVerifyInput{AgentID: "5d0c1f9a-2b3e-4c7d-8a6f-9e0b1c2d3e4f"})
	if err != nil {
		t.Fatalf("opaque rejected an agent_id: %v", err)
	}
}

func TestAnthropicSessionKey_DeterministicAndSessionScoped(t *testing.T) {
	a := AnthropicSessionKey("sess-1", "claude-opus-4-7", "you are a helpful assistant")
	b := AnthropicSessionKey("sess-1", "claude-opus-4-7", "you are a helpful assistant")
	if a != b {
		t.Fatalf("key non-deterministic: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "anthropic_session|") {
		t.Fatalf("key %q is not provider-scoped", a)
	}
	if c := AnthropicSessionKey("sess-2", "claude-opus-4-7", "you are a helpful assistant"); c == a {
		t.Fatal("different sessions produced the same key")
	}
}

func TestAnthropicSession_IdentityKey_MatchesRecipe(t *testing.T) {
	proof := map[string]any{"session_id": "sess-1", "model": "claude-opus-4-7", "system_prompt": "sys"}
	key, err := AnthropicSession{}.IdentityKey(context.Background(), proof)
	if err != nil {
		t.Fatalf("IdentityKey: %v", err)
	}
	if want := AnthropicSessionKey("sess-1", "claude-opus-4-7", "sys"); key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	if err := (AnthropicSession{}).Verify(context.Background(), adapter.IdentityVerifyInput{IdentityProof: proof}); err != nil {
		t.Fatalf("Verify on a well-formed proof: %v", err)
	}
}

func TestAnthropicSession_MalformedProofRejected(t *testing.T) {
	for name, proof := range map[string]map[string]any{
		"nil":           nil,
		"no session_id": {"model": "claude-opus-4-7"},
		"no model":      {"session_id": "sess-1"},
	} {
		if _, err := (AnthropicSession{}).IdentityKey(context.Background(), proof); err == nil {
			t.Errorf("%s: IdentityKey accepted a malformed proof", name)
		}
		if err := (AnthropicSession{}).Verify(context.Background(), adapter.IdentityVerifyInput{IdentityProof: proof}); err == nil {
			t.Errorf("%s: Verify accepted a malformed proof", name)
		}
	}
}
