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

// PeerResult captures one peer's recall response or error.
type PeerResult struct {
	PeerID    string
	PeerName  string
	Response  *api.RecallResponse
	Err       error
	LatencyMS int
}

// FanoutResult is the merged output of a federated recall.
type FanoutResult struct {
	Results        []api.RecallHit
	PartialSuccess bool
	FailedPeers    []string
	PeerLatencyMS  map[string]int
	TotalScanned   int
}

// FanoutRecall sends the recall request to all peers in parallel,
// collects results within the ctx deadline, and returns the merged
// FanoutResult. localResult is the already-completed local query
// (nil if local failed). peers are the PeerClients to fan out to.
func FanoutRecall(
	ctx context.Context,
	localResult *api.RecallResponse,
	peers []*PeerClient,
	wsID string,
	req api.RecallRequest,
	federationPath string,
) (*FanoutResult, error) {
	out := &FanoutResult{
		PeerLatencyMS: make(map[string]int, len(peers)+1),
	}

	if localResult != nil {
		out.Results = append(out.Results, localResult.Results...)
		out.TotalScanned += localResult.TotalCandidatesScanned
		out.PeerLatencyMS["local"] = localResult.LatencyMS
	}

	if len(peers) == 0 {
		if localResult == nil {
			return nil, ErrAllPeersFailed
		}
		return out, nil
	}

	resultCh := make(chan PeerResult, len(peers))
	var wg sync.WaitGroup

	for _, pc := range peers {
		wg.Add(1)
		go func(c *PeerClient) {
			defer wg.Done()
			start := time.Now()
			peerCtx, cancel := context.WithTimeout(ctx, c.RequestTimeout())
			defer cancel()

			resp, err := c.Recall(peerCtx, wsID, req, federationPath)
			latency := int(time.Since(start).Milliseconds())
			resultCh <- PeerResult{
				PeerID:    c.PeerID(),
				PeerName:  c.PeerName(),
				Response:  resp,
				Err:       err,
				LatencyMS: latency,
			}
		}(pc)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for pr := range resultCh {
		out.PeerLatencyMS[pr.PeerID] = pr.LatencyMS
		if pr.Err != nil {
			out.FailedPeers = append(out.FailedPeers, pr.PeerName)
			out.PartialSuccess = true
			continue
		}
		if pr.Response != nil {
			for i := range pr.Response.Results {
				pr.Response.Results[i].Via = "federation:" + pr.PeerName
			}
			out.Results = append(out.Results, pr.Response.Results...)
			out.TotalScanned += pr.Response.TotalCandidatesScanned
		}
	}

	if len(out.Results) == 0 && localResult == nil {
		return nil, ErrAllPeersFailed
	}

	return out, nil
}

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
