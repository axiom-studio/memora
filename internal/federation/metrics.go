package federation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// Outcomes of an outbound peer request, as metric labels. The set is
// fixed so a peer cannot grow the label space with its own error codes.
const (
	OutcomeOK          = "ok"
	OutcomeTimeout     = "timeout"
	OutcomeUnreachable = "unreachable"
	OutcomeRateLimited = "rate_limited"
	OutcomeRejected    = "rejected" // the peer refused us (4xx)
	OutcomeNotFound    = "not_found"
	OutcomeError       = "error" // a 5xx or an answer we could not use
)

// latencyBuckets are the histogram bounds, in seconds.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type histogram struct {
	counts []uint64 // per bucket, cumulative at render
	sum    float64
	n      uint64
}

// Metrics counts federation traffic for /metrics (F-8):
//
//	memora_federation_peer_requests_total{peer,outcome}   calls this node made
//	memora_federation_peer_latency_seconds{peer}          their latency
//	memora_federation_inbound_total{peer,outcome}         calls this node received
//
// peer is a node ID from the registry; an inbound caller that is not a
// registered peer is counted as peer="unknown".
type Metrics struct {
	mu       sync.Mutex
	requests map[[2]string]uint64
	latency  map[string]*histogram
	inbound  map[[2]string]uint64
}

// NewMetrics returns an empty collector.
func NewMetrics() *Metrics {
	return &Metrics{requests: map[[2]string]uint64{}, latency: map[string]*histogram{}, inbound: map[[2]string]uint64{}}
}

// ObserveRequest records one outbound call to peer.
func (m *Metrics) ObserveRequest(peer, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[[2]string{peer, outcome}]++
	if outcome == OutcomeRateLimited {
		return // never sent
	}
	h := m.latency[peer]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(latencyBuckets))}
		m.latency[peer] = h
	}
	s := d.Seconds()
	for i, b := range latencyBuckets {
		if s <= b {
			h.counts[i]++
			break
		}
	}
	h.sum += s
	h.n++
}

// ObserveInbound records one call this node received from peer.
func (m *Metrics) ObserveInbound(peer, outcome string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.inbound[[2]string{peer, outcome}]++
	m.mu.Unlock()
}

// OutcomeOf maps an outbound call's error to an outcome label.
func OutcomeOf(err error) string {
	var pe *PeerError
	var ue *url.Error
	switch {
	case err == nil:
		return OutcomeOK
	case errors.Is(err, types.ErrNotFound):
		return OutcomeNotFound
	case errors.Is(err, ErrOutboundRateLimited):
		return OutcomeRateLimited
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeTimeout
	case errors.As(err, &pe):
		if pe.Status >= 400 && pe.Status < 500 {
			return OutcomeRejected
		}
		return OutcomeError
	case errors.As(err, &ue):
		if ue.Timeout() {
			return OutcomeTimeout
		}
		return OutcomeUnreachable
	default:
		return OutcomeError
	}
}

// WritePrometheus writes the counters in the Prometheus text format.
func (m *Metrics) WritePrometheus(w io.Writer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	writeCounter(w, "memora_federation_peer_requests_total", "Federation calls this node made, by peer node ID and outcome.", m.requests)
	fmt.Fprintln(w, "# HELP memora_federation_peer_latency_seconds Latency of federation calls this node made, by peer node ID.")
	fmt.Fprintln(w, "# TYPE memora_federation_peer_latency_seconds histogram")
	peers := make([]string, 0, len(m.latency))
	for p := range m.latency {
		peers = append(peers, p)
	}
	sort.Strings(peers)
	for _, p := range peers {
		h := m.latency[p]
		var cum uint64
		for i, b := range latencyBuckets {
			cum += h.counts[i]
			fmt.Fprintf(w, "memora_federation_peer_latency_seconds_bucket{peer=%q,le=%q} %d\n", p, fmt.Sprint(b), cum)
		}
		fmt.Fprintf(w, "memora_federation_peer_latency_seconds_bucket{peer=%q,le=\"+Inf\"} %d\n", p, h.n)
		fmt.Fprintf(w, "memora_federation_peer_latency_seconds_sum{peer=%q} %g\n", p, h.sum)
		fmt.Fprintf(w, "memora_federation_peer_latency_seconds_count{peer=%q} %d\n", p, h.n)
	}
	writeCounter(w, "memora_federation_inbound_total", "Federation calls this node received, by caller node ID and outcome.", m.inbound)
}

func writeCounter(w io.Writer, name, help string, c map[[2]string]uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := make([][2]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		fmt.Fprintf(w, "%s{peer=%q,outcome=%q} %d\n", name, k[0], k[1], c[k])
	}
}
