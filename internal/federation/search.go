package federation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// MaxFederatedTimeout caps a request's timeout_ms.
const MaxFederatedTimeout = 60 * time.Second

// Searcher fans a node-wide search out to this node's peers (F-4),
// reading the registry's current peers and clients on every call, with
// one outbound limiter per peer at that peer's rate_limit_qps.
type Searcher struct {
	reg      *Registry
	outbound *RateLimiterPool
}

// NewSearcher returns a Searcher over reg, or nil for a nil registry
// (federation off).
func NewSearcher(reg *Registry) (*Searcher, error) {
	if reg == nil {
		return nil, nil
	}
	return &Searcher{reg: reg, outbound: NewInboundPool(DefaultOutboundQPS)}, nil
}

// NodeID is this node's ID.
func (s *Searcher) NodeID() string { return s.reg.NodeID }

// SearchOpts are the federated options of a search request.
type SearchOpts struct {
	// Peers restricts the fan-out to these node IDs; empty means every
	// searchable peer (not revoked, with an outbound key).
	Peers []string
	// Timeout overrides every peer's own timeout when positive.
	Timeout time.Duration
}

// Fanout sends req to the selected peers and collects their hits after
// localResult's. The outgoing X-Memora-Federation-Path is this node's
// ID: a federated search starts here, and peers never forward it.
func (s *Searcher) Fanout(ctx context.Context, localResult *api.RecallResponse, req api.FederationSearchRequest, opts SearchOpts) (*FanoutResult, error) {
	peers, unusable, err := s.selectPeers(opts.Peers)
	if err != nil {
		return nil, err
	}
	if opts.Timeout > MaxFederatedTimeout {
		opts.Timeout = MaxFederatedTimeout
	}
	fr, err := FanoutSearch(ctx, localResult, s.reg.NodeID, peers, req, AppendToPath("", s.reg.NodeID), opts.Timeout, s.outbound, s.reg.metrics)
	if err != nil {
		return nil, err
	}
	// Peers that would have been asked but whose key this node cannot
	// open are failures the caller sees, not silent omissions (#6177).
	for _, p := range unusable {
		fr.Asked = append(fr.Asked, p.ID)
		fr.Failures = append(fr.Failures, PeerFailure{NodeID: p.ID, Name: p.Name, Err: fmt.Errorf("%w: peer %s", ErrPeerKeyUnavailable, p.Name)})
		s.reg.metrics.ObserveRequest(p.ID, OutcomeKeyUnavailable, 0)
	}
	sort.SliceStable(fr.Failures, func(i, j int) bool { return fr.Failures[i].NodeID < fr.Failures[j].NodeID })
	return fr, nil
}

// ErrPeerKeyUnavailable is a peer this node cannot call because its
// stored api_key does not open with this node's federation secret.
var ErrPeerKeyUnavailable = errors.New("federation: peer api_key unavailable")

// selectPeers resolves the fan-out targets against one registry
// snapshot, so a concurrent Replace cannot leave a peer without its
// client. unusable are the selected peers whose stored key does not
// open: they are reported as failures, not asked.
func (s *Searcher) selectPeers(ids []string) (out []*PeerClient, unusable []*Peer, err error) {
	snap := s.reg.current()
	if len(ids) == 0 {
		for i := range snap.peers {
			p := &snap.peers[i]
			switch {
			case p.Searchable():
				out = append(out, snap.clients[p.ID])
			case !p.Revoked && p.KeyUnavailable:
				unusable = append(unusable, p)
			}
		}
		return out, unusable, nil
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := types.ParseID("peers", id); err != nil {
			return nil, nil, err
		}
		p := snap.byID[id]
		if p == nil {
			return nil, nil, fmt.Errorf("%w: peers: %s is not a peer of this node", types.ErrInvalidInput, id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		switch {
		case p.Searchable():
			out = append(out, snap.clients[id])
		case !p.Revoked && p.KeyUnavailable:
			unusable = append(unusable, p)
		default:
			return nil, nil, fmt.Errorf("%w: peers: %s is revoked or has no api_key", types.ErrInvalidInput, id)
		}
	}
	return out, unusable, nil
}

// ErrUnknownNode is a node ID that is not a usable peer of this node.
var ErrUnknownNode = errors.New("federation: unknown node")

// Fetch gets a full memory from the peer that holds it, with that peer's
// credentials, timeout and outbound limit, and stamps the result with the
// peer's node_id and node_name from this node's config. A node that is
// not a searchable peer is ErrUnknownNode; a memory the peer does not
// hold is types.ErrNotFound; other failures are returned for
// PeerErrorCode to classify.
func (s *Searcher) Fetch(ctx context.Context, nodeID, memoryID string) (*api.MemoryEnvelope, error) {
	snap := s.reg.current()
	p := snap.byID[nodeID]
	if p != nil && !p.Revoked && p.KeyUnavailable {
		s.reg.metrics.ObserveRequest(nodeID, OutcomeKeyUnavailable, 0)
		return nil, fmt.Errorf("%w: peer %s", ErrPeerKeyUnavailable, p.Name)
	}
	if p == nil || !p.Searchable() {
		return nil, fmt.Errorf("%w: %s", ErrUnknownNode, nodeID)
	}
	c := snap.clients[nodeID]
	if !s.outbound.AllowRate(nodeID, p.RateLimitQPS) {
		s.reg.metrics.ObserveRequest(nodeID, OutcomeRateLimited, 0)
		return nil, ErrOutboundRateLimited
	}
	ctx, cancel := context.WithTimeout(ctx, c.RequestTimeout())
	defer cancel()
	start := time.Now()
	env, err := c.FetchMemory(ctx, memoryID, AppendToPath("", s.reg.NodeID))
	s.reg.metrics.ObserveRequest(nodeID, OutcomeOf(err), time.Since(start))
	if err != nil {
		return nil, err
	}
	env.NodeID, env.NodeName = p.ID, p.Name
	return env, nil
}

// PeerErrorCode classifies a peer failure for a response's error_code;
// see api.FailedPeer.
func PeerErrorCode(err error) string { return peerErrorCode(err) }
