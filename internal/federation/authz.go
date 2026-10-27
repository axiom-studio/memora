package federation

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
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

// Reject reasons recorded in the audit ledger and metrics. They are
// internal: the response a refused caller sees does not reveal them.
const (
	ReasonUnknownPeer        = "unknown_peer"
	ReasonBadKey             = "bad_key"
	ReasonBadCert            = "bad_cert"
	ReasonRevoked            = "revoked"
	ReasonFederationMismatch = "federation_mismatch"
	ReasonLoop               = "loop"
	ReasonRateLimited        = "rate_limited"
)

// PeerAuthError is an authentication refusal. Error() is what the caller
// is told; Reason is what the audit records.
type PeerAuthError struct {
	Reason string
	msg    string
	kind   error // types.ErrFederationAuth or types.ErrFederationRevoked
}

func (e *PeerAuthError) Error() string { return e.kind.Error() + ": " + e.msg }
func (e *PeerAuthError) Unwrap() error { return e.kind }

func refuse(reason, msg string) error {
	return &PeerAuthError{Reason: reason, msg: msg, kind: types.ErrFederationAuth}
}

// RejectReason returns the audit reason for an AuthenticatePeer error.
func RejectReason(err error) string {
	var pe *PeerAuthError
	switch {
	case errors.As(err, &pe):
		return pe.Reason
	case errors.Is(err, types.ErrFederationLoop):
		return ReasonLoop
	default:
		return "error"
	}
}

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
		reason := ReasonBadKey
		if p == nil {
			reason = ReasonUnknownPeer
		}
		return nil, refuse(reason, "unknown peer or invalid credential")
	}

	if p.hasClientCert {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return nil, refuse(ReasonBadCert, "client certificate required")
		}
		fp := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(fp[:], p.clientCertHash[:]) != 1 {
			return nil, refuse(ReasonBadCert, "client certificate does not match")
		}
	}

	if p.Revoked {
		return nil, &PeerAuthError{Reason: ReasonRevoked, msg: "peer " + peerID, kind: types.ErrFederationRevoked}
	}

	remoteFedID := r.Header.Get(HeaderFederationID)
	if remoteFedID != "" && reg.FederationID != "" && remoteFedID != reg.FederationID {
		return nil, refuse(ReasonFederationMismatch, fmt.Sprintf("remote federation %q != local %q", remoteFedID, reg.FederationID))
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
