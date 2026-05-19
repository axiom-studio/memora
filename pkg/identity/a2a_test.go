package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func TestA2A_VerifySuccess(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	agentID := "agent_test_123"
	sig := ed25519.Sign(priv, []byte(agentID))

	card := AgentCard{
		AgentID:   agentID,
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Name:      "test-agent",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	a := &A2A{Client: srv.Client()}
	err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: agentID,
		IdentityProof: map[string]any{
			"agent_card_url": srv.URL,
			"signature":      base64.StdEncoding.EncodeToString(sig),
		},
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestA2A_VerifyBadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	agentID := "agent_test_123"

	card := AgentCard{
		AgentID:   agentID,
		PublicKey: base64.StdEncoding.EncodeToString(pub),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	a := &A2A{Client: srv.Client()}
	err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: agentID,
		IdentityProof: map[string]any{
			"agent_card_url": srv.URL,
			"signature":      base64.StdEncoding.EncodeToString([]byte("bad-sig-that-is-64-bytes-long-for-ed25519-000000000000000000000000")),
		},
	})
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
}

func TestA2A_VerifyAgentIDMismatch(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(priv, []byte("agent_wrong"))

	card := AgentCard{
		AgentID:   "agent_different",
		PublicKey: base64.StdEncoding.EncodeToString(pub),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	a := &A2A{Client: srv.Client()}
	err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: "agent_wrong",
		IdentityProof: map[string]any{
			"agent_card_url": srv.URL,
			"signature":      base64.StdEncoding.EncodeToString(sig),
		},
	})
	if err == nil {
		t.Fatal("expected error for agent_id mismatch")
	}
}

func TestA2A_MissingProofFields(t *testing.T) {
	a := &A2A{}
	err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing proof fields")
	}
}

func TestA2A_CardCaching(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	agentID := "agent_cached"
	sig := ed25519.Sign(priv, []byte(agentID))

	fetchCount := 0
	card := AgentCard{
		AgentID:   agentID,
		PublicKey: base64.StdEncoding.EncodeToString(pub),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	a := &A2A{Client: srv.Client(), CacheTTL: 1 * time.Hour}
	proof := map[string]any{
		"agent_card_url": srv.URL,
		"signature":      base64.StdEncoding.EncodeToString(sig),
	}

	for i := 0; i < 3; i++ {
		if err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
			AgentID: agentID, IdentityProof: proof,
		}); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}

	if fetchCount != 1 {
		t.Fatalf("expected 1 fetch (cached), got %d", fetchCount)
	}
}

func TestA2A_FetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := &A2A{Client: srv.Client()}
	err := a.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: "agent_test",
		IdentityProof: map[string]any{
			"agent_card_url": srv.URL,
			"signature":      "dGVzdA==",
		},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
