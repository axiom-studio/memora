// Package federation implements multi-instance Memora federation:
// peer registry, fanout, aggregation, and loop prevention.
package federation

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync/atomic"
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
	ClientCertSHA256 string // hex pin of the peer's client certificate
	RateLimitQPS     float64
	InboundQPS       float64 // 0 = the node's federation.inbound_qps
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
}

// InboundKeySHA256 returns the hex hash of the peer's inbound key, or ""
// when it has none.
func (p *Peer) InboundKeySHA256() string {
	if !p.hasInboundKey {
		return ""
	}
	return hex.EncodeToString(p.inboundKeyHash[:])
}

// AcceptsInbound reports whether the peer has an inbound credential and
// so can call this node's peer routes.
func (p *Peer) AcceptsInbound() bool { return p.hasInboundKey }

// Registry holds this node's federation peers and a client for each.
// Its contents are an immutable snapshot swapped atomically by Replace,
// so peers can be added, changed, revoked or removed while the node
// runs: every lookup sees one consistent set, and the next request after
// a Replace sees the new one.
type Registry struct {
	// NodeID is this node's [node] id: sent as X-Memora-Local-Instance-ID
	// and appended to loop-guard paths.
	NodeID string
	// FederationID is the optional cluster label from config.
	FederationID string

	defaults config.FederationConfig // peer timeout and QPS defaults
	snap     atomic.Pointer[registrySnapshot]
	metrics  *Metrics
}

// Metrics returns the node's federation traffic counters.
func (r *Registry) Metrics() *Metrics {
	if r == nil {
		return nil
	}
	return r.metrics
}

type registrySnapshot struct {
	peers   []Peer                 // ordered by node ID
	byID    map[string]*Peer       // keyed by peer node ID
	clients map[string]*PeerClient // keyed by peer node ID
}

// NewRegistry parses and validates the federation config into a ready
// Registry, keyed by each peer's node ID. Returns nil when federation is
// disabled. selfNodeID is this node's ID; a peer listing it is rejected
// as a self-reference. cfg's peer timeout and QPS defaults also apply to
// peers given later to Replace.
func NewRegistry(cfg config.FederationConfig, selfNodeID string) (*Registry, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if _, err := types.ParseID("node id", selfNodeID); err != nil {
		return nil, fmt.Errorf("federation: %w", err)
	}
	r := &Registry{NodeID: selfNodeID, FederationID: cfg.FederationID, defaults: cfg, metrics: NewMetrics()}
	r.defaults.Peers = nil
	if err := r.Replace(cfg.Peers); err != nil {
		return nil, err
	}
	return r, nil
}

// Replace validates peers, builds a client for each, and swaps them in
// as the registry's peers. On any error the registry is unchanged.
func (r *Registry) Replace(peers []config.PeerConfig) error {
	next, err := r.build(peers)
	if err != nil {
		return err
	}
	r.snap.Store(next)
	return nil
}

// Validate reports whether peers would be accepted by Replace, building
// their clients (TLS files included) without swapping them in.
func (r *Registry) Validate(peers []config.PeerConfig) error {
	_, err := r.build(peers)
	return err
}

func (r *Registry) build(peers []config.PeerConfig) (*registrySnapshot, error) {
	next := &registrySnapshot{
		byID:    make(map[string]*Peer, len(peers)),
		clients: make(map[string]*PeerClient, len(peers)),
	}
	var errs []error
	for i, pc := range peers {
		p, err := parsePeer(pc, i, r.NodeID, r.defaults)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, dup := next.byID[p.ID]; dup {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: duplicate peer id %q", i, p.ID))
			continue
		}
		next.peers = append(next.peers, p)
		next.byID[p.ID] = nil
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(next.peers, func(i, j int) bool { return next.peers[i].ID < next.peers[j].ID })
	for i := range next.peers {
		p := &next.peers[i]
		c, err := newPeerClient(*p, r.FederationID, r.NodeID)
		if err != nil {
			return nil, fmt.Errorf("peer %s: %w", p.Name, err)
		}
		next.byID[p.ID] = p
		next.clients[p.ID] = c
	}
	return next, nil
}

func (r *Registry) current() *registrySnapshot {
	if r == nil {
		return nil
	}
	return r.snap.Load()
}

// Defaults for a peer's outbound limits when neither the peer nor the
// federation section sets them.
const (
	DefaultPeerTimeoutMS = 5000
)

func parsePeer(pc config.PeerConfig, idx int, selfNodeID string, fc config.FederationConfig) (Peer, error) {
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
		ClientCertSHA256: pc.ClientCertSHA256,
		ID:               id,
		Name:             pc.Name,
		Endpoint:         pc.URL,
		TrustMode:        trust,
		APIKey:           pc.APIKey,
		TLSCert:          pc.TLSCert,
		ClientCertFile:   pc.ClientCertFile,
		ClientKeyFile:    pc.ClientKeyFile,
		RateLimitQPS:     firstPositive(pc.RateLimitQPS, fc.OutboundQPS, DefaultOutboundQPS),
		RequestTimeoutMS: int(firstPositive(float64(pc.TimeoutMS), float64(fc.PeerTimeoutMS), DefaultPeerTimeoutMS)),
		Revoked:          pc.Revoked,
		InboundQPS:       pc.InboundQPS,
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
	snap := r.current()
	if snap == nil {
		return false
	}
	for i := range snap.peers {
		if snap.peers[i].hasClientCert {
			return true
		}
	}
	return false
}

// Peers returns the registered peers, ordered by node ID. The slice is
// the current snapshot's and must not be modified.
func (r *Registry) Peers() []Peer {
	snap := r.current()
	if snap == nil {
		return nil
	}
	return snap.peers
}

// Peer returns a specific peer by ID, or nil if not found.
func (r *Registry) Peer(id string) *Peer {
	snap := r.current()
	if snap == nil {
		return nil
	}
	return snap.byID[id]
}

// Client returns the client for the peer with this node ID, or nil.
func (r *Registry) Client(id string) *PeerClient {
	snap := r.current()
	if snap == nil {
		return nil
	}
	return snap.clients[id]
}

// DefaultRequestTimeout returns the peer's configured request timeout.
func (p *Peer) DefaultRequestTimeout() time.Duration {
	if p.RequestTimeoutMS <= 0 {
		return 5 * time.Second
	}
	return time.Duration(p.RequestTimeoutMS) * time.Millisecond
}

func firstPositive(vals ...float64) float64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// Searchable reports whether this node queries the peer in a federated
// search: it is not revoked and has an outbound key to present.
func (p *Peer) Searchable() bool { return !p.Revoked && p.APIKey != "" }
