package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func TestDID_VerifySuccess(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	agentID := "agent_did_test"
	did := "did:agent:test123"
	vmID := did + "#key-1"
	sig := ed25519.Sign(priv, []byte(agentID))

	doc := DIDDocument{
		ID: did,
		VerificationMethod: []VerificationMethod{{
			ID:              vmID,
			Type:            "Ed25519VerificationKey2020",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/did+ld+json")
		json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	d := &DID{ResolverURL: srv.URL + "/", Client: srv.Client()}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: agentID,
		IdentityProof: map[string]any{
			"did": did,
			"proof": map[string]any{
				"type":               "Ed25519Signature2020",
				"verificationMethod": vmID,
				"signatureValue":     base64.StdEncoding.EncodeToString(sig),
			},
		},
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestDID_VerifyBadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	did := "did:agent:test456"
	vmID := did + "#key-1"

	doc := DIDDocument{
		ID: did,
		VerificationMethod: []VerificationMethod{{
			ID:              vmID,
			Type:            "Ed25519VerificationKey2020",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	d := &DID{ResolverURL: srv.URL + "/", Client: srv.Client()}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: "agent_did_test",
		IdentityProof: map[string]any{
			"did": did,
			"proof": map[string]any{
				"type":               "Ed25519Signature2020",
				"verificationMethod": vmID,
				"signatureValue":     base64.StdEncoding.EncodeToString([]byte("bad-sig-that-is-64-bytes-long-for-ed25519-000000000000000000000000")),
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
}

func TestDID_MissingProofFields(t *testing.T) {
	d := &DID{}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID:       "agent_test",
		IdentityProof: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing proof fields")
	}
}

func TestDID_UnsupportedProofType(t *testing.T) {
	d := &DID{}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: "agent_test",
		IdentityProof: map[string]any{
			"did": "did:agent:x",
			"proof": map[string]any{
				"type":               "RsaSignature2018",
				"verificationMethod": "did:agent:x#key-1",
				"signatureValue":     "abc",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported proof type")
	}
}

func TestDID_VMNotFound(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	agentID := "agent_did_test"
	did := "did:agent:novm"
	sig := ed25519.Sign(priv, []byte(agentID))

	doc := DIDDocument{
		ID: did,
		VerificationMethod: []VerificationMethod{{
			ID:              did + "#key-99",
			Type:            "Ed25519VerificationKey2020",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	d := &DID{ResolverURL: srv.URL + "/", Client: srv.Client()}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: agentID,
		IdentityProof: map[string]any{
			"did": did,
			"proof": map[string]any{
				"type":               "Ed25519Signature2020",
				"verificationMethod": did + "#key-1",
				"signatureValue":     base64.StdEncoding.EncodeToString(sig),
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing verification method")
	}
}

func TestDID_UniversalResolverWrapper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	agentID := "agent_did_wrapped"
	did := "did:agent:wrapped"
	vmID := did + "#key-1"
	sig := ed25519.Sign(priv, []byte(agentID))

	doc := DIDDocument{
		ID: did,
		VerificationMethod: []VerificationMethod{{
			ID:              vmID,
			Type:            "Ed25519VerificationKey2020",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapper := map[string]any{"didDocument": doc}
		json.NewEncoder(w).Encode(wrapper)
	}))
	defer srv.Close()

	d := &DID{ResolverURL: srv.URL + "/", Client: srv.Client()}
	err := d.Verify(context.Background(), adapter.IdentityVerifyInput{
		AgentID: agentID,
		IdentityProof: map[string]any{
			"did": did,
			"proof": map[string]any{
				"type":               "Ed25519Signature2020",
				"verificationMethod": vmID,
				"signatureValue":     base64.StdEncoding.EncodeToString(sig),
			},
		},
	})
	if err != nil {
		t.Fatalf("expected success with wrapped response, got: %v", err)
	}
}
