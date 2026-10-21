package federation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/axiom-studio/memora/internal/rank"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// ErrAllPeersFailed is returned when every peer (including local) fails.
var ErrAllPeersFailed = errors.New("federation: all peers failed")

// PeerResult captures one peer's search response or error.
type PeerResult struct {
	PeerID    string
	PeerName  string
	Response  *api.RecallResponse
	Err       error
	LatencyMS int
}

// PeerFailure is a peer that did not contribute to a federated search.
type PeerFailure struct {
	NodeID string
	Name   string
	Err    error
}

// NodeHits is one node's ranked hit list, best first, each memory once.
type NodeHits struct {
	NodeID string
	Hits   []api.RecallHit
}

// FanoutResult is the collected output of a federated search: one ranked
// list per contributing node, this node's first, for Merge to fuse.
type FanoutResult struct {
	Asked         []string // node IDs of the peers asked
	Lists         []NodeHits
	Failures      []PeerFailure
	PeerLatencyMS map[string]int // by node ID, this node's included
	TotalScanned  int
}

// PartialSuccess reports whether any peer did not contribute.
func (fr *FanoutResult) PartialSuccess() bool { return len(fr.Failures) > 0 }

// FailedPeers renders Failures for the response, with an error code
// per peer (see api.FailedPeer).
func (fr *FanoutResult) FailedPeers() []api.FailedPeer {
	out := make([]api.FailedPeer, 0, len(fr.Failures))
	for _, f := range fr.Failures {
		out = append(out, api.FailedPeer{NodeID: f.NodeID, Name: f.Name, ErrorCode: peerErrorCode(f.Err)})
	}
	return out
}

// peerErrorCode classifies why a peer did not contribute.
func peerErrorCode(err error) string {
	var pe *PeerError
	var ue *url.Error
	switch {
	case errors.Is(err, ErrOutboundRateLimited):
		return "outbound_rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &pe):
		if pe.Code != "" {
			return pe.Code
		}
		return fmt.Sprintf("http_%d", pe.Status)
	case errors.As(err, &ue):
		if ue.Timeout() {
			return "timeout"
		}
		return "unreachable"
	default:
		return "bad_response"
	}
}

// FanoutSearch sends req to every peer in parallel, each bounded by its
// own timeout (or timeout, when positive) and admitted by its outbound
// limiter (when limiter is non-nil), and collects the answers. A slow,
// failed or rate-limited peer never fails the search; it is recorded in
// Failures. local is this node's completed search (nil when it failed),
// listed first under localNodeID.
//
// Each peer hit's node_id, node_name and via are set from this node's
// config for that peer, not taken from the peer's answer, so a peer
// cannot attribute hits to another node. Within a list, a memory the
// peer returned twice keeps its first (best) rank.
func FanoutSearch(
	ctx context.Context,
	local *api.RecallResponse,
	localNodeID string,
	peers []*PeerClient,
	req api.FederationSearchRequest,
	federationPath string,
	timeout time.Duration,
	limiter *RateLimiterPool,
	metrics *Metrics,
) (*FanoutResult, error) {
	out := &FanoutResult{
		PeerLatencyMS: make(map[string]int, len(peers)+1),
	}
	for _, pc := range peers {
		out.Asked = append(out.Asked, pc.PeerID())
	}

	if local != nil {
		out.Lists = append(out.Lists, NodeHits{NodeID: localNodeID, Hits: local.Results})
		out.TotalScanned += local.TotalCandidatesScanned
		out.PeerLatencyMS[localNodeID] = local.LatencyMS
	}

	resultCh := make(chan PeerResult, len(peers))
	var wg sync.WaitGroup
	for _, pc := range peers {
		wg.Add(1)
		go func(c *PeerClient) {
			defer wg.Done()
			if limiter != nil && !limiter.AllowRate(c.PeerID(), c.peer.RateLimitQPS) {
				resultCh <- PeerResult{PeerID: c.PeerID(), PeerName: c.PeerName(), Err: ErrOutboundRateLimited}
				return
			}
			d := c.RequestTimeout()
			if timeout > 0 {
				d = timeout
			}
			start := time.Now()
			peerCtx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			resp, err := c.Search(peerCtx, req, federationPath)
			resultCh <- PeerResult{
				PeerID:    c.PeerID(),
				PeerName:  c.PeerName(),
				Response:  resp,
				Err:       err,
				LatencyMS: int(time.Since(start).Milliseconds()),
			}
		}(pc)
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	byPeer := map[string]NodeHits{}
	for pr := range resultCh {
		metrics.ObserveRequest(pr.PeerID, OutcomeOf(pr.Err), time.Duration(pr.LatencyMS)*time.Millisecond)
		if pr.Err != nil {
			out.Failures = append(out.Failures, PeerFailure{NodeID: pr.PeerID, Name: pr.PeerName, Err: pr.Err})
			continue
		}
		out.PeerLatencyMS[pr.PeerID] = pr.LatencyMS
		if pr.Response == nil {
			continue
		}
		seen := map[string]bool{}
		hits := make([]api.RecallHit, 0, len(pr.Response.Results))
		for _, h := range pr.Response.Results {
			if seen[h.MemoryID] {
				continue
			}
			seen[h.MemoryID] = true
			h.NodeID, h.NodeName, h.Via = pr.PeerID, pr.PeerName, "federation:"+pr.PeerID
			hits = append(hits, h)
		}
		byPeer[pr.PeerID] = NodeHits{NodeID: pr.PeerID, Hits: hits}
		out.TotalScanned += pr.Response.TotalCandidatesScanned
	}
	// Peer lists in configured order, so the fused order is deterministic.
	for _, pc := range peers {
		if l, ok := byPeer[pc.PeerID()]; ok {
			out.Lists = append(out.Lists, l)
		}
	}
	sort.SliceStable(out.Failures, func(i, j int) bool { return out.Failures[i].NodeID < out.Failures[j].NodeID })

	if local == nil && len(out.Failures) == len(peers) {
		return nil, ErrAllPeersFailed
	}
	return out, nil
}

// Merge fuses the node lists by reciprocal rank fusion (rank.Fuse, as
// hybrid search does). Ranks, not scores, are combined because the
// nodes run different embedding models, so their scores are on
// unrelated scales. A hit is identified by (node_id, memory_id): the
// same memory ID on two nodes is two hits. Returns at most k hits (all
// when k <= 0), each scored by its fused RRF score.
func Merge(fr *FanoutResult, k int) []api.RecallHit {
	lists := make([]rank.List, 0, len(fr.Lists))
	for _, l := range fr.Lists {
		lists = append(lists, rank.List{Hits: l.Hits, Weight: 1})
	}
	return rank.Fuse(lists, func(h api.RecallHit) string { return h.NodeID + "/" + h.MemoryID }, k)
}

// Reranker re-scores the merged top hits of a federated search, for
// example with a cross-encoder. It returns the hits in their new order;
// the caller keeps the top k.
type Reranker interface {
	Rerank(ctx context.Context, query string, hits []api.RecallHit) ([]api.RecallHit, error)
}

// NoopReranker keeps the fused order. It is the default.
type NoopReranker struct{}

func (NoopReranker) Rerank(_ context.Context, _ string, hits []api.RecallHit) ([]api.RecallHit, error) {
	return hits, nil
}

// ErrOutboundRateLimited marks a peer skipped because this node's
// outbound limit for it was exhausted.
var ErrOutboundRateLimited = errors.New("federation: outbound rate limit for peer exhausted")
