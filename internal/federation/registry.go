// Package federation implements multi-instance Memora federation:
// peer registry, fanout, aggregation, and loop prevention.
package federation

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

// TrustMode describes how a peer authenticates.
type TrustMode string

const (
	TrustMTLS   TrustMode = "mtls"
	TrustAPIKey TrustMode = "api_key"
)

// Peer is a resolved, validated federation peer.
type Peer struct {
	ID               string
	Name             string
	Endpoint         string
	TrustMode        TrustMode
	APIKey           string
	TLSCert          string // CA for the peer's server certificate
	ClientCertFile   string // our client certificate for outbound mTLS
	ClientKeyFile    string
	Workspaces       []string // empty = all workspaces
	RateLimitQPS     int
	RequestTimeoutMS int
	// Revoked peers stay known but their inbound calls get 403.
	Revoked bool

	// inboundKeyHash is the SHA-256 of the key the peer presents to us;
	// hasInboundKey is false when the peer is outbound-only.
	inboundKeyHash [32]byte
	hasInboundKey  bool
	// clientCertHash pins the peer's TLS client certificate when set.
	clientCertHash [32]byte
	hasClientCert  bool

	workspaceSet map[string]bool
}

// AcceptsInbound reports whether the peer has an inbound credential and
// so can call this node's peer routes.
func (p *Peer) AcceptsInbound() bool { return p.hasInboundKey }

// Registry holds the set of validated federation peers and provides
// lookup methods used by the fanout engine and inbound auth.
type Registry struct {
	// NodeID is this node's [node] id: sent as X-Memora-Local-Instance-ID
	// and appended to loop-guard paths.
	NodeID string
	// FederationID is the optional cluster label from config.
	FederationID string
	peers        []Peer
	byID         map[string]*Peer // keyed by peer node ID
}

// NewRegistry parses and validates the federation config into a ready
// Registry, keyed by each peer's node ID. Returns nil when federation is
// disabled. selfNodeID is this node's ID; a peer listing it is rejected
// as a self-reference.
func NewRegistry(cfg config.FederationConfig, selfNodeID string) (*Registry, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if _, err := types.ParseID("node id", selfNodeID); err != nil {
		return nil, fmt.Errorf("federation: %w", err)
	}

	r := &Registry{
		NodeID:       selfNodeID,
		FederationID: cfg.FederationID,
		byID:         make(map[string]*Peer, len(cfg.Peers)),
	}

	var errs []error
	for i, pc := range cfg.Peers {
		p, err := parsePeer(pc, i, selfNodeID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, dup := r.byID[p.ID]; dup {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: duplicate peer id %q", i, p.ID))
			continue
		}
		r.peers = append(r.peers, p)
		r.byID[p.ID] = &r.peers[len(r.peers)-1]
	}
	return r, errors.Join(errs...)
}

func parsePeer(pc config.PeerConfig, idx int, selfNodeID string) (Peer, error) {
	if _, err := types.ParseID(fmt.Sprintf("federation.peers[%d].node_id", idx), pc.NodeID); err != nil {
		return Peer{}, err
	}
	if pc.Name == "" {
		return Peer{}, fmt.Errorf("federation.peers[%d]: name is required", idx)
	}
	if pc.URL == "" {
		return Peer{}, fmt.Errorf("federation.peers[%d] (%s): url is required", idx, pc.Name)
	}
	u, err := url.Parse(pc.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Peer{}, fmt.Errorf("federation.peers[%d] (%s): invalid url %q", idx, pc.Name, pc.URL)
	}

	id := pc.NodeID
	if id == selfNodeID {
		return Peer{}, fmt.Errorf("federation.peers[%d] (%s): node_id is this node's own ID (self-reference)", idx, pc.Name)
	}

	trust := TrustAPIKey
	if pc.TLSCert != "" || pc.ClientCertFile != "" {
		trust = TrustMTLS
	}

	p := Peer{
		ID:               id,
		Name:             pc.Name,
		Endpoint:         pc.URL,
		TrustMode:        trust,
		APIKey:           pc.APIKey,
		TLSCert:          pc.TLSCert,
		ClientCertFile:   pc.ClientCertFile,
		ClientKeyFile:    pc.ClientKeyFile,
		RateLimitQPS:     50,
		RequestTimeoutMS: 5000,
		Revoked:          pc.Revoked,
	}
	if pc.InboundKeySHA256 != "" {
		h, err := decodeSHA256(pc.InboundKeySHA256)
		if err != nil {
			return Peer{}, fmt.Errorf("federation.peers[%d] (%s): inbound_key_sha256: %w", idx, pc.Name, err)
		}
		p.inboundKeyHash, p.hasInboundKey = h, true
	}
	if pc.ClientCertSHA256 != "" {
		h, err := decodeSHA256(pc.ClientCertSHA256)
		if err != nil {
			return Peer{}, fmt.Errorf("federation.peers[%d] (%s): client_cert_sha256: %w", idx, pc.Name, err)
		}
		p.clientCertHash, p.hasClientCert = h, true
	}
	return p, nil
}

func decodeSHA256(s string) ([32]byte, error) {
	var h [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h, errors.New("must be 64 hex characters")
	}
	copy(h[:], b)
	return h, nil
}

// RequiresClientCerts reports whether any peer pins a TLS client
// certificate, so the server must ask clients for one.
func (r *Registry) RequiresClientCerts() bool {
	if r == nil {
		return false
	}
	for i := range r.peers {
		if r.peers[i].hasClientCert {
			return true
		}
	}
	return false
}

// Peers returns all registered peers.
func (r *Registry) Peers() []Peer {
	if r == nil {
		return nil
	}
	return r.peers
}

// PeersForWorkspace returns peers authorized to receive queries for
// wsID. Peers with an empty workspace allowlist are authorized for all
// workspaces.
func (r *Registry) PeersForWorkspace(wsID string) []Peer {
	if r == nil {
		return nil
	}
	var result []Peer
	for _, p := range r.peers {
		if len(p.Workspaces) == 0 || p.workspaceSet[wsID] {
			result = append(result, p)
		}
	}
	return result
}

// IsAuthorizedPeer checks whether an inbound request from peerID is
// allowed to query wsID.
func (r *Registry) IsAuthorizedPeer(peerID, wsID string) bool {
	if r == nil {
		return false
	}
	p, ok := r.byID[peerID]
	if !ok {
		return false
	}
	if len(p.Workspaces) == 0 {
		return true
	}
	return p.workspaceSet[wsID]
}

// Peer returns a specific peer by ID, or nil if not found.
func (r *Registry) Peer(id string) *Peer {
	if r == nil {
		return nil
	}
	return r.byID[id]
}

// DefaultRequestTimeout returns the peer's configured request timeout.
func (p *Peer) DefaultRequestTimeout() time.Duration {
	if p.RequestTimeoutMS <= 0 {
		return 5 * time.Second
	}
	return time.Duration(p.RequestTimeoutMS) * time.Millisecond
}
