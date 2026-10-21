package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// MaxFederatedTimeout caps a request's timeout_ms.
const MaxFederatedTimeout = 60 * time.Second

// Searcher fans a node-wide search out to this node's peers (F-4). It
// owns one client and one outbound limiter per peer.
type Searcher struct {
	reg      *Registry
	pool     *ClientPool
	outbound *RateLimiterPool
}

// NewSearcher builds the peer clients for reg. It returns nil for a nil
// registry (federation off).
func NewSearcher(reg *Registry) (*Searcher, error) {
	if reg == nil {
		return nil, nil
	}
	pool, err := NewClientPool(reg)
	if err != nil {
		return nil, err
	}
	return &Searcher{reg: reg, pool: pool, outbound: NewOutboundPool(reg.Peers())}, nil
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
	peers, err := s.selectPeers(opts.Peers)
	if err != nil {
		return nil, err
	}
	if opts.Timeout > MaxFederatedTimeout {
		opts.Timeout = MaxFederatedTimeout
	}
	return FanoutSearch(ctx, localResult, peers, req, AppendToPath("", s.reg.NodeID), opts.Timeout, s.outbound)
}

func (s *Searcher) selectPeers(ids []string) ([]*PeerClient, error) {
	var out []*PeerClient
	if len(ids) == 0 {
		for _, p := range s.reg.Peers() {
			if p.Searchable() {
				out = append(out, s.pool.Client(p.ID))
			}
		}
		return out, nil
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := types.ParseID("peers", id); err != nil {
			return nil, err
		}
		p := s.reg.Peer(id)
		if p == nil {
			return nil, fmt.Errorf("%w: peers: %s is not a peer of this node", types.ErrInvalidInput, id)
		}
		if !p.Searchable() {
			return nil, fmt.Errorf("%w: peers: %s is revoked or has no api_key", types.ErrInvalidInput, id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, s.pool.Client(id))
		}
	}
	return out, nil
}
