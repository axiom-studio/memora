package federation

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_local",
		Peers: []config.PeerConfig{
			{NodeID: nid("peer-a"), Name: "peer-a", URL: "https://a.example.com", APIKey: "k"},
		},
	}
	reg, err := NewRegistry(cfg, nid("fed_local"))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestAuthorizeFederatedRequest_NonFederated(t *testing.T) {
	reg := testRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws/recall", nil)
	result, err := AuthorizeFederatedRequest(r, reg)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil result for non-federated request")
	}
}

func TestAuthorizeFederatedRequest_Valid(t *testing.T) {
	reg := testRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws/recall", nil)
	r.Header.Set(HeaderLocalInstanceID, nid("peer-a"))
	r.Header.Set(HeaderFederationID, "fed_local")

	result, err := AuthorizeFederatedRequest(r, reg)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PeerID != nid("peer-a") {
		t.Errorf("expected peer-a's node ID, got %s", result.PeerID)
	}
}

func TestAuthorizeFederatedRequest_FedIDMismatch(t *testing.T) {
	reg := testRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(HeaderLocalInstanceID, nid("peer-a"))
	r.Header.Set(HeaderFederationID, "fed_other")

	_, err := AuthorizeFederatedRequest(r, reg)
	if !errors.Is(err, types.ErrFederationAuth) {
		t.Errorf("expected ErrFederationAuth, got %v", err)
	}
}

func TestAuthorizeFederatedRequest_UnknownPeer(t *testing.T) {
	reg := testRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(HeaderLocalInstanceID, nid("unknown-peer"))
	r.Header.Set(HeaderFederationID, "fed_local")

	_, err := AuthorizeFederatedRequest(r, reg)
	if !errors.Is(err, types.ErrFederationAuth) {
		t.Errorf("expected ErrFederationAuth, got %v", err)
	}
}

func TestAuthorizeFederatedRequest_LoopDetected(t *testing.T) {
	reg := testRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(HeaderLocalInstanceID, nid("peer-a"))
	r.Header.Set(HeaderFederationID, "fed_local")
	r.Header.Set(HeaderFederationPath, nid("fed_local")) // this node's own ID: a loop

	_, err := AuthorizeFederatedRequest(r, reg)
	if !errors.Is(err, types.ErrFederationLoop) {
		t.Errorf("expected ErrFederationLoop, got %v", err)
	}
}

func TestAuthorizeFederatedWorkspace_Allowed(t *testing.T) {
	reg := testRegistry(t)
	if err := AuthorizeFederatedWorkspace(reg, nid("peer-a"), "any_ws"); err != nil {
		t.Errorf("expected allowed (wildcard), got %v", err)
	}
}

func TestAuthorizeFederatedWorkspace_Denied(t *testing.T) {
	cfg := config.FederationConfig{
		Enabled:      true,
		FederationID: "fed_local",
		Peers: []config.PeerConfig{
			{NodeID: nid("peer-scoped"), Name: "peer-scoped", URL: "https://a.example.com", APIKey: "k"},
		},
	}
	reg, err := NewRegistry(cfg, nid("fed_local"))
	if err != nil {
		t.Fatal(err)
	}
	err = AuthorizeFederatedWorkspace(reg, nid("nonexistent"), "ws_test")
	if !errors.Is(err, types.ErrFederationAuth) {
		t.Errorf("expected ErrFederationAuth, got %v", err)
	}
}
