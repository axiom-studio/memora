package federation

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

type InboundAuthResult struct {
	PeerID         string
	FederationID   string
	FederationPath []string
}

// noPeerHash stands in for the stored hash when the caller names an
// unknown peer, so that path does the same hashing and comparison work
// as a known one. It is random, so no key can match it.
var noPeerHash = func() [32]byte {
	var h [32]byte
	if _, err := rand.Read(h[:]); err != nil {
		panic(fmt.Sprintf("federation: seed noPeerHash: %v", err))
	}
	return h
}()

// AuthenticatePeer authenticates a call to one of the routes peers call.
// The caller names itself in X-Memora-Local-Instance-ID (its node ID)
// and presents "Bearer <key>" in X-Memora-Federation-Auth; the key's
// SHA-256 is compared in constant time with the hash configured for
// that peer. A peer that pins a client certificate must also present it
// over TLS. Then the cluster label, when both sides set one, must match
// and the loop guard is applied against this node's ID.
//
// Unknown peers, peers with no inbound key, and wrong or missing keys
// all return the same ErrFederationAuth (401), so a caller cannot tell
// which node IDs are configured. A revoked peer with a valid credential
// gets ErrFederationRevoked (403). Errors never contain the key.
func AuthenticatePeer(r *http.Request, reg *Registry) (*InboundAuthResult, error) {
	peerID := r.Header.Get(HeaderLocalInstanceID)
	key, bearer := strings.CutPrefix(r.Header.Get(HeaderFederationAuth), "Bearer ")
	got := sha256.Sum256([]byte(key))

	p := reg.Peer(peerID)
	want := noPeerHash
	if p != nil && p.hasInboundKey {
		want = p.inboundKeyHash
	}
	match := subtle.ConstantTimeCompare(got[:], want[:]) == 1
	if !match || !bearer || key == "" || p == nil || !p.hasInboundKey {
		return nil, fmt.Errorf("%w: unknown peer or invalid credential", types.ErrFederationAuth)
	}

	if p.hasClientCert {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return nil, fmt.Errorf("%w: client certificate required", types.ErrFederationAuth)
		}
		fp := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(fp[:], p.clientCertHash[:]) != 1 {
			return nil, fmt.Errorf("%w: client certificate does not match", types.ErrFederationAuth)
		}
	}

	if p.Revoked {
		return nil, fmt.Errorf("%w: peer %s", types.ErrFederationRevoked, peerID)
	}

	remoteFedID := r.Header.Get(HeaderFederationID)
	if remoteFedID != "" && reg.FederationID != "" && remoteFedID != reg.FederationID {
		return nil, fmt.Errorf("%w: remote federation %q != local %q", types.ErrFederationAuth, remoteFedID, reg.FederationID)
	}

	path := ParseFederationPath(r.Header.Get(HeaderFederationPath))
	if err := CheckLoop(path, reg.NodeID); err != nil {
		return nil, err
	}

	return &InboundAuthResult{
		PeerID:         peerID,
		FederationID:   remoteFedID,
		FederationPath: path,
	}, nil
}

// IsPeerRoute reports whether path is one of the routes peers call, which
// take peer credentials instead of the server's API key. The operator
// routes under /v1/federation/peers keep the API key.
func IsPeerRoute(path string) bool {
	return path == "/v1/federation/search" || strings.HasPrefix(path, "/v1/federation/memories/")
}
