package federation

import (
	"context"
	"errors"
	"sync"
	"time"

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

// FanoutResult is the collected output of a federated search: the local
// hits followed by each answering peer's, unmerged.
type FanoutResult struct {
	Results        []api.RecallHit
	PartialSuccess bool
	FailedPeers    []string // names, as the existing response field
	Failures       []PeerFailure
	PeerLatencyMS  map[string]int // keyed by node ID ("local" for this node)
	TotalScanned   int
}

// FanoutSearch sends req to every peer in parallel, each bounded by its
// own timeout (or timeout, when positive) and admitted by its outbound
// limiter (when limiter is non-nil), and collects the answers. A slow,
// failed or rate-limited peer never fails the search; it is recorded in
// Failures. localResult is the completed local search (nil when it
// failed). Peer hits are tagged Via "federation:<name>" and keep the
// node_id/node_name the peer stamped.
func FanoutSearch(
	ctx context.Context,
	localResult *api.RecallResponse,
	peers []*PeerClient,
	req api.FederationSearchRequest,
	federationPath string,
	timeout time.Duration,
	limiter *RateLimiterPool,
) (*FanoutResult, error) {
	out := &FanoutResult{
		PeerLatencyMS: make(map[string]int, len(peers)+1),
	}

	if localResult != nil {
		out.Results = append(out.Results, localResult.Results...)
		out.TotalScanned += localResult.TotalCandidatesScanned
		out.PeerLatencyMS["local"] = localResult.LatencyMS
	}

	resultCh := make(chan PeerResult, len(peers))
	var wg sync.WaitGroup
	for _, pc := range peers {
		wg.Add(1)
		go func(c *PeerClient) {
			defer wg.Done()
			if limiter != nil && !limiter.Allow(c.PeerID()) {
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

	for pr := range resultCh {
		if pr.Err != nil {
			out.FailedPeers = append(out.FailedPeers, pr.PeerName)
			out.Failures = append(out.Failures, PeerFailure{NodeID: pr.PeerID, Name: pr.PeerName, Err: pr.Err})
			out.PartialSuccess = true
			continue
		}
		out.PeerLatencyMS[pr.PeerID] = pr.LatencyMS
		if pr.Response != nil {
			for i := range pr.Response.Results {
				pr.Response.Results[i].Via = "federation:" + pr.PeerName
			}
			out.Results = append(out.Results, pr.Response.Results...)
			out.TotalScanned += pr.Response.TotalCandidatesScanned
		}
	}

	if localResult == nil && len(out.Failures) == len(peers) {
		return nil, ErrAllPeersFailed
	}
	return out, nil
}

// ErrOutboundRateLimited marks a peer skipped because this node's
// outbound limit for it was exhausted.
var ErrOutboundRateLimited = errors.New("federation: outbound rate limit for peer exhausted")

// FanoutLookup queries peers in parallel, returning the first
// successful (non-nil) memory. Returns nil if all peers return 404.
func FanoutLookup(
	ctx context.Context,
	peers []*PeerClient,
	wsID, memoryID, federationPath string,
) (*LookupResult, error) {
	if len(peers) == 0 {
		return nil, nil
	}

	type result struct {
		peer   string
		memory *api.MemoryEnvelope
		err    error
	}

	resultCh := make(chan result, len(peers))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, pc := range peers {
		go func(c *PeerClient) {
			mem, err := c.Lookup(ctx, wsID, memoryID, federationPath)
			var env *api.MemoryEnvelope
			if mem != nil {
				env = &api.MemoryEnvelope{Memory: mem}
			}
			resultCh <- result{peer: c.PeerName(), memory: env, err: err}
		}(pc)
	}

	var firstErr error
	for range peers {
		r := <-resultCh
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if r.memory != nil {
			cancel()
			return &LookupResult{Memory: r.memory.Memory, PeerName: r.peer}, nil
		}
	}
	return nil, firstErr
}

// LookupResult wraps a found memory with the peer that served it.
type LookupResult struct {
	Memory   *types.Memory
	PeerName string
}
