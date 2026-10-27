package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func TestOAuthAgent_VerifySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(IntrospectionResponse{
			Active:   true,
			ClientID: "agent_oauth_test",
		})
	}))
	defer srv.Close()

	o := &OAuthAgent{
		IntrospectionURL: srv.URL,
		Client:           srv.Client(),
	}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: "agent_oauth_test",
		IdentityProof: map[string]any{
			"access_token": "valid-token",
		},
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestOAuthAgent_InactiveToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(IntrospectionResponse{Active: false})
	}))
	defer srv.Close()

	o := &OAuthAgent{IntrospectionURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"access_token": "expired"},
	})
	if err == nil {
		t.Fatal("expected error for inactive token")
	}
}

// TestOAuthAgent_IdentityKey_FromClaim: the key names the introspection
// endpoint, the claim and its value, so different clients get different
// agents and a token without the claim is refused rather than mapping
// every such token to one agent.
func TestOAuthAgent_IdentityKey_FromClaim(t *testing.T) {
	clientID := "client-a"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(IntrospectionResponse{Active: true, ClientID: clientID})
	}))
	defer srv.Close()
	o := &OAuthAgent{IntrospectionURL: srv.URL, Client: srv.Client()}
	proof := map[string]any{"access_token": "token"}

	keyA, err := o.IdentityKey(context.Background(), proof)
	if err != nil {
		t.Fatalf("IdentityKey: %v", err)
	}
	if want := "oauth_agent|" + srv.URL + "|client_id|client-a"; keyA != want {
		t.Fatalf("key = %q, want %q", keyA, want)
	}
	clientID = "client-b"
	if keyB, _ := o.IdentityKey(context.Background(), proof); keyB == keyA {
		t.Fatal("different client_ids produced the same key")
	}
	clientID = ""
	if _, err := o.IdentityKey(context.Background(), proof); err == nil {
		t.Fatal("a token without client_id must be refused")
	}
}

func TestOAuthAgent_MissingToken(t *testing.T) {
	o := &OAuthAgent{IntrospectionURL: "http://localhost"}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing access_token")
	}
}

func TestOAuthAgent_NotConfigured(t *testing.T) {
	o := &OAuthAgent{}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{"access_token": "tok"},
	})
	if err != ErrIdentityNotConfigured {
		t.Fatalf("expected ErrIdentityNotConfigured, got: %v", err)
	}
}

func TestOAuthAgent_SubClaim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(IntrospectionResponse{
			Active: true, ClientID: "irrelevant", Sub: "agent_sub_match",
		})
	}))
	defer srv.Close()

	o := &OAuthAgent{IntrospectionURL: srv.URL, Client: srv.Client(), AgentIDClaim: "sub"}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_sub_match",
		IdentityProof: map[string]any{"access_token": "tok"},
	})
	if err != nil {
		t.Fatalf("expected success with sub claim, got: %v", err)
	}
}
