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

func TestOAuthAgent_AgentIDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(IntrospectionResponse{
			Active:   true,
			ClientID: "agent_different",
		})
	}))
	defer srv.Close()

	o := &OAuthAgent{IntrospectionURL: srv.URL, Client: srv.Client()}
	err := o.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_expected",
		IdentityProof: map[string]any{"access_token": "token"},
	})
	if err == nil {
		t.Fatal("expected error for agent_id mismatch")
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
