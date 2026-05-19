// Package federation implements multi-instance Memora federation:
// peer registry, fanout, aggregation, and loop prevention.
package federation

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/axiom-studio/memora/internal/config"
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
	TLSCert          string
	Workspaces       []string // empty = all workspaces
	RateLimitQPS     int
	RequestTimeoutMS int

	workspaceSet map[string]bool
}

// Registry holds the set of validated federation peers and provides
// lookup methods used by the fanout engine and inbound auth.
type Registry struct {
	FederationID string
	peers        []Peer
	byID         map[string]*Peer
}

// NewRegistry parses and validates the federation config into a ready
// Registry. Returns nil when federation is disabled. selfID is this
// instance's federation_id (used to reject self-reference).
func NewRegistry(cfg config.FederationConfig, selfID string) (*Registry, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.FederationID == "" {
		return nil, errors.New("federation: federation_id is required when enabled")
	}

	r := &Registry{
		FederationID: cfg.FederationID,
		byID:         make(map[string]*Peer, len(cfg.Peers)),
	}

	var errs []error
	for i, pc := range cfg.Peers {
		p, err := parsePeer(pc, i, selfID)
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

func parsePeer(pc config.PeerConfig, idx int, selfID string) (Peer, error) {
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

	id := pc.Name
	if id == selfID {
		return Peer{}, fmt.Errorf("federation.peers[%d] (%s): peer name matches own federation_id (self-reference)", idx, pc.Name)
	}

	trust := TrustAPIKey
	if pc.TLSCert != "" {
		trust = TrustMTLS
	}

	p := Peer{
		ID:               id,
		Name:             pc.Name,
		Endpoint:         pc.URL,
		TrustMode:        trust,
		APIKey:           pc.APIKey,
		TLSCert:          pc.TLSCert,
		RateLimitQPS:     50,
		RequestTimeoutMS: 5000,
	}
	return p, nil
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
