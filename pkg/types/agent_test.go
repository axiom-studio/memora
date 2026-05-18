package types

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHashIdentityProof_Empty(t *testing.T) {
	if got := HashIdentityProof(nil); got != "" {
		t.Errorf("nil map: want empty string, got %q", got)
	}
	if got := HashIdentityProof(map[string]any{}); got != "" {
		t.Errorf("empty map: want empty string, got %q", got)
	}
}

func TestHashIdentityProof_Deterministic(t *testing.T) {
	// Two maps with the same content (in arbitrary insertion order) must
	// hash to the same digest. Map iteration is randomized in Go, so this
	// is a real test of key-sorting in HashIdentityProof.
	a := map[string]any{"token": "abc.def.ghi", "scope": []any{"read", "write"}, "exp": float64(1736208000)}
	b := map[string]any{"exp": float64(1736208000), "scope": []any{"read", "write"}, "token": "abc.def.ghi"}
	ha, hb := HashIdentityProof(a), HashIdentityProof(b)
	if ha != hb {
		t.Errorf("hash is not order-invariant: %q vs %q", ha, hb)
	}
	if len(ha) != 64 {
		t.Errorf("hash len = %d, want 64 hex chars (sha256)", len(ha))
	}
}

func TestHashIdentityProof_DifferentInputsDiffer(t *testing.T) {
	a := map[string]any{"token": "abc"}
	b := map[string]any{"token": "xyz"}
	if HashIdentityProof(a) == HashIdentityProof(b) {
		t.Error("distinct proofs collided (probabilistically; collision here is a sha256 break)")
	}
}

// TestAgentJSON_OmitsIdentityProof is the primary anti-leak guard for
// issue #2098: marshalling an Agent — through ANY public API — must
// never serialize the raw IdentityProof map. The `json:"-"` tag is the
// type-layer enforcement; this test fails loud the day someone
// "helpfully" reintroduces a tag.
func TestAgentJSON_OmitsIdentityProof(t *testing.T) {
	const secret = "sk-live-NEVER-LEAK-THIS-TOKEN"
	a := Agent{
		AgentID:          "agent_01h0000000000000000000000",
		WorkspaceID:      "ws_01h0000000000000000000000",
		IdentityProvider: "anthropic_session",
		IdentityProof:    map[string]any{"session_token": secret, "exp": 1736208000},
		RegisteredAt:     time.Now(),
	}
	buf, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(buf)
	if strings.Contains(body, secret) {
		t.Fatalf("BUG: IdentityProof secret leaked into JSON: %s", body)
	}
	if strings.Contains(body, `"identity_proof"`) {
		t.Fatalf("BUG: identity_proof key present in JSON output: %s", body)
	}
	// session_token came from the inner map; it must not appear as a top-level key either.
	if strings.Contains(body, "session_token") {
		t.Fatalf("BUG: inner identity_proof key (session_token) leaked: %s", body)
	}
}

// TestAgentJSON_KeepsHashField confirms the wire still carries the
// non-sensitive digest field when the adapter sets it.
func TestAgentJSON_KeepsHashField(t *testing.T) {
	a := Agent{
		AgentID:           "agent_01h0000000000000000000000",
		WorkspaceID:       "ws_01h0000000000000000000000",
		IdentityProvider:  "anthropic_session",
		IdentityProofHash: "deadbeefcafebabe1234567890abcdef1234567890abcdef1234567890abcdef",
		RegisteredAt:      time.Now(),
	}
	buf, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(buf)
	if !strings.Contains(body, `"identity_proof_hash"`) {
		t.Errorf("expected identity_proof_hash field in JSON, got %s", body)
	}
	if !strings.Contains(body, a.IdentityProofHash) {
		t.Errorf("expected hash value in JSON, got %s", body)
	}
}
