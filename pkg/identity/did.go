package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// DIDDocument is a minimal W3C DID Document with verification methods.
type DIDDocument struct {
	ID                 string               `json:"id"`
	VerificationMethod []VerificationMethod `json:"verificationMethod"`
	Authentication     []json.RawMessage    `json:"authentication,omitempty"`
}

// VerificationMethod is a single key in a DID Document.
type VerificationMethod struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	PublicKeyBase64 string `json:"publicKeyBase64,omitempty"`
}

type cachedDIDDoc struct {
	doc       DIDDocument
	fetchedAt time.Time
}

// DID verifies agent identity via W3C DID resolution + proof verification.
// The agent presents {did, proof: {type, verificationMethod, signatureValue}}.
// The provider resolves the DID document, finds the verification method,
// and verifies the Ed25519 signature over the agent_id.
type DID struct {
	ResolverURL string // universal resolver URL (default: https://dev.uniresolver.io/1.0/identifiers/)
	Client      *http.Client
	CacheTTL    time.Duration

	mu    sync.RWMutex
	cache map[string]cachedDIDDoc
}

func (d *DID) Name() string { return string(types.IdentityProviderDID) }

func (d *DID) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true}
}

func (d *DID) Verify(ctx context.Context, in adapter.IdentityVerifyInput) error {
	did, _ := in.IdentityProof["did"].(string)
	proofRaw, _ := in.IdentityProof["proof"].(map[string]any)
	if did == "" || proofRaw == nil {
		return fmt.Errorf("did: identity_proof requires did and proof")
	}

	proofType, _ := proofRaw["type"].(string)
	vmID, _ := proofRaw["verificationMethod"].(string)
	sigValue, _ := proofRaw["signatureValue"].(string)
	if proofType == "" || vmID == "" || sigValue == "" {
		return fmt.Errorf("did: proof requires type, verificationMethod, and signatureValue")
	}

	if proofType != "Ed25519Signature2020" {
		return fmt.Errorf("did: unsupported proof type %q (only Ed25519Signature2020 supported)", proofType)
	}

	doc, err := d.resolve(ctx, did)
	if err != nil {
		return fmt.Errorf("did: resolve %q: %w", did, err)
	}

	if doc.ID != did {
		return fmt.Errorf("did: resolved document id %q does not match requested %q", doc.ID, did)
	}

	var vm *VerificationMethod
	for i := range doc.VerificationMethod {
		if doc.VerificationMethod[i].ID == vmID {
			vm = &doc.VerificationMethod[i]
			break
		}
	}
	if vm == nil {
		return fmt.Errorf("did: verificationMethod %q not found in DID document", vmID)
	}

	authIDs := authenticationVMIDs(doc)
	if len(authIDs) > 0 {
		if !authIDs[vmID] {
			return fmt.Errorf("did: verificationMethod %q is not listed under authentication proof purpose", vmID)
		}
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(vm.PublicKeyBase64)
	if err != nil {
		return fmt.Errorf("did: invalid public key encoding: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("did: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(pubKeyBytes))
	}

	sigBytes, err := base64.StdEncoding.DecodeString(sigValue)
	if err != nil {
		return fmt.Errorf("did: invalid signature encoding: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), []byte(in.AgentID), sigBytes) {
		return fmt.Errorf("did: signature verification failed")
	}

	return nil
}

func (d *DID) resolve(ctx context.Context, did string) (DIDDocument, error) {
	ttl := d.CacheTTL
	if ttl == 0 {
		ttl = 1 * time.Hour
	}

	d.mu.RLock()
	if d.cache != nil {
		if c, ok := d.cache[did]; ok && time.Since(c.fetchedAt) < ttl {
			d.mu.RUnlock()
			return c.doc, nil
		}
	}
	d.mu.RUnlock()

	resolverURL := d.ResolverURL
	if resolverURL == "" {
		resolverURL = "https://dev.uniresolver.io/1.0/identifiers/"
	}

	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolverURL+did, nil)
	if err != nil {
		return DIDDocument{}, err
	}
	req.Header.Set("Accept", "application/did+ld+json")

	resp, err := client.Do(req)
	if err != nil {
		return DIDDocument{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DIDDocument{}, fmt.Errorf("DID resolver returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DIDDocument{}, err
	}

	// The universal resolver wraps the document in {"didDocument": ...}.
	var wrapper struct {
		DIDDocument json.RawMessage `json:"didDocument"`
	}
	var doc DIDDocument
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.DIDDocument != nil {
		if err := json.Unmarshal(wrapper.DIDDocument, &doc); err != nil {
			return DIDDocument{}, fmt.Errorf("invalid DID document JSON: %w", err)
		}
	} else {
		if err := json.Unmarshal(body, &doc); err != nil {
			return DIDDocument{}, fmt.Errorf("invalid DID document JSON: %w", err)
		}
	}

	d.mu.Lock()
	if d.cache == nil {
		d.cache = make(map[string]cachedDIDDoc)
	}
	d.cache[did] = cachedDIDDoc{doc: doc, fetchedAt: time.Now()}
	d.mu.Unlock()

	return doc, nil
}

// authenticationVMIDs extracts VM IDs allowed for authentication purpose.
// Per W3C DID Core, authentication entries can be string references or
// embedded verification method objects.
func authenticationVMIDs(doc DIDDocument) map[string]bool {
	if len(doc.Authentication) == 0 {
		return nil
	}
	ids := make(map[string]bool, len(doc.Authentication))
	for _, raw := range doc.Authentication {
		var ref string
		if json.Unmarshal(raw, &ref) == nil {
			ids[ref] = true
			continue
		}
		var embedded struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &embedded) == nil && embedded.ID != "" {
			ids[embedded.ID] = true
		}
	}
	return ids
}
