package federation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/axiom-studio/memora/internal/rank"
	"github.com/axiom-studio/memora/pkg/types"
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
	// DroppedHits counts, by node ID, the hits a peer returned that were
	// discarded: malformed IDs, or more than the k it was asked for.
	DroppedHits map[string]int
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
	case errors.Is(err, ErrPeerAddressRefused):
		return "peer_address_refused"
	case errors.Is(err, ErrPeerKeyUnavailable):
		return "key_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &pe):
		if knownPeerCodes[pe.Code] {
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

// knownPeerCodes are the error codes a Memora node answers its peer
// routes with. Only these are passed on in failed_peers; any other code
// a peer sends becomes http_<status>, so a peer cannot put text of its
// choosing into this node's responses.
var knownPeerCodes = map[string]bool{
	"federation_unauthorized": true,
	"federation_peer_revoked": true,
	"federation_disabled":     true,
	"federation_loop":         true,
	"federation_multi_tenant": true,
	"rate_limited":            true,
	"invalid_input":           true,
	"not_found":               true,
	"method_not_allowed":      true,
	"capability_unavailable":  true,
	"internal_error":          true,
}

// Bounds on the text of a peer hit; a peer's own hits are far smaller.
const (
	maxPeerExcerptBytes = 4 << 10
	maxPeerMatchedBytes = 64 << 10
	maxPeerTags         = 64
	maxPeerTagBytes     = 256
)

// sanitizePeerHit checks one hit from a peer's answer against what this
// node asked for. It reports false for a hit to drop: one whose
// memory_id is not a UUID, or whose workspace_id, cell_id, watermark or
// written_by_agent_id is set but not a UUID. A kept hit has its text
// bounded, content cut to the requested max_content_bytes (or removed
// when content was not asked for), and fields a peer search never
// returns cleared.
func sanitizePeerHit(h *api.RecallHit, req api.FederationSearchRequest) bool {
	if _, err := types.ParseID("memory_id", h.MemoryID); err != nil {
		return false
	}
	for _, f := range []struct{ name, v string }{
		{"workspace_id", h.WorkspaceID}, {"cell_id", h.CellID},
		{"watermark", h.Watermark}, {"written_by_agent_id", h.WrittenByAgentID},
	} {
		if f.v == "" {
			continue
		}
		if _, err := types.ParseID(f.name, f.v); err != nil {
			return false
		}
	}
	h.Text, _ = truncateUTF8(h.Text, maxPeerExcerptBytes)
	h.MatchedText, _ = truncateUTF8(h.MatchedText, maxPeerMatchedBytes)
	if req.IncludeContent {
		var cut bool
		h.Content, cut = truncateUTF8(h.Content, EffectiveMaxContentBytes(req.MaxContentBytes))
		h.ContentTruncated = h.ContentTruncated || cut
	} else {
		h.Content, h.ContentTruncated = "", false
	}
	h.Cells, h.GraphProvenance = nil, nil
	if len(h.Tags) > 0 {
		tags := make(map[string]string, min(len(h.Tags), maxPeerTags))
		for k, v := range h.Tags {
			if len(tags) == maxPeerTags {
				break
			}
			if len(k) <= maxPeerTagBytes && len(v) <= maxPeerTagBytes {
				tags[k] = v
			}
		}
		h.Tags = tags
	}
	return true
}

// Content limits for include_content, shared by local and federated
// search.
const (
	DefaultMaxContentBytes = 64 << 10
	HardMaxContentBytes    = 1 << 20
)

// EffectiveMaxContentBytes is the content cut for a requested
// max_content_bytes: the default when unset, never above the hard cap.
func EffectiveMaxContentBytes(requested int) int {
	if requested <= 0 {
		return DefaultMaxContentBytes
	}
	return min(requested, HardMaxContentBytes)
}

// truncateUTF8 cuts s to at most max bytes on a rune boundary and
// reports whether it cut anything.
func truncateUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
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
// peer returned twice keeps its first (best) rank. A peer's hits are
// checked by sanitizePeerHit and capped at req.K; the rest are counted
// in DroppedHits.
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
		DroppedHits:   map[string]int{},
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
		hits := make([]api.RecallHit, 0, min(len(pr.Response.Results), max(req.K, 0)))
		dropped := 0
		for _, h := range pr.Response.Results {
			if req.K > 0 && len(hits) == req.K || !sanitizePeerHit(&h, req) {
				dropped++
				continue
			}
			if seen[h.MemoryID] {
				continue
			}
			seen[h.MemoryID] = true
			h.NodeID, h.NodeName, h.Via = pr.PeerID, pr.PeerName, "federation:"+pr.PeerID
			hits = append(hits, h)
		}
		if dropped > 0 {
			out.DroppedHits[pr.PeerID] = dropped
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
