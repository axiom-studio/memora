package federation

import (
	"container/list"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/axiom-studio/memora/pkg/types/api"
)

// Peer route templates, recorded in audit entries instead of the request
// path, so a caller cannot write text of its choosing into the ledger.
const (
	RouteFederationSearch = "/v1/federation/search"
	RouteFederationMemory = "/v1/federation/memories/{memory_id}"
)

// PeerRouteTemplate returns the route template for a peer route path
// (see IsPeerRoute).
func PeerRouteTemplate(path string) string {
	if path == RouteFederationSearch {
		return RouteFederationSearch
	}
	return RouteFederationMemory
}

// Unauthenticated reports whether a refusal reason means the caller
// proved nothing: no known peer, or not its key. Those refusals are
// aggregated rather than ledgered one by one; the rest (bad_cert,
// revoked, federation_mismatch, loop, rate_limited) need a valid key.
func Unauthenticated(reason string) bool {
	return reason == ReasonUnknownPeer || reason == ReasonBadKey
}

// clientKey identifies the network origin of a request's RemoteAddr for
// pre-authentication limits and aggregation: the IPv4 address, or the
// /64 of an IPv6 one, so a caller cannot escape a limit by rotating
// addresses within its prefix.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap()
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return p.String()
	}
	return ip.String()
}

// AddrLimiter rate-limits peer-route requests per client address before
// authentication. It holds at most maxAddrs limiters and evicts the
// least recently used one when full, so active callers (real peers)
// keep their state however many addresses call it. Addresses it has
// not seen are admitted through one node-wide limiter (newPerSec), so
// rotating through many addresses (a botnet, or the 65,536 /64s of one
// IPv6 /48) cannot mint a fresh burst per address faster than that
// (#6232).
type AddrLimiter struct {
	mu       sync.Mutex
	qps      float64
	maxAddrs int
	lims     map[string]*list.Element // of *addrEntry
	lru      *list.List               // front = most recently used
	newAddrs *rate.Limiter
}

type addrEntry struct {
	key string
	lim *rate.Limiter
}

// NewAddrLimiter limits each client address to qps requests per second,
// and admits at most qps/2 (at least 10) previously unseen addresses
// per second node-wide.
func NewAddrLimiter(qps float64) *AddrLimiter {
	if qps <= 0 {
		qps = 2 * DefaultInboundQPS
	}
	newPerSec := max(10, qps/2)
	return &AddrLimiter{
		qps: qps, maxAddrs: 10000,
		lims:     map[string]*list.Element{},
		lru:      list.New(),
		newAddrs: rate.NewLimiter(rate.Limit(newPerSec), int(newPerSec)),
	}
}

// Allow reports whether a request from remoteAddr (http.Request
// RemoteAddr) is within its address's limit.
func (l *AddrLimiter) Allow(remoteAddr string) bool {
	key := clientKey(remoteAddr)
	l.mu.Lock()
	el, ok := l.lims[key]
	if !ok {
		if !l.newAddrs.Allow() {
			l.mu.Unlock()
			return false
		}
		if l.lru.Len() >= l.maxAddrs {
			oldest := l.lru.Back()
			l.lru.Remove(oldest)
			delete(l.lims, oldest.Value.(*addrEntry).key)
		}
		el = l.lru.PushFront(&addrEntry{key: key, lim: rate.NewLimiter(rate.Limit(l.qps), max(1, int(l.qps)))})
		l.lims[key] = el
	} else {
		l.lru.MoveToFront(el)
	}
	lim := el.Value.(*addrEntry).lim
	l.mu.Unlock()
	return lim.Allow()
}

// QPS is the per-address limit.
func (l *AddrLimiter) QPS() float64 { return l.qps }

// RejectionAggregator folds unauthenticated peer-route refusals into
// one federation_rejected ledger entry per (reason, claimed node,
// client address, route) per window, carrying the count and the first
// and last time seen. It holds at most maxRejectKeys groups per window;
// refusals past that are counted in one overflow group.
type RejectionAggregator struct {
	window time.Duration
	write  func(api.LedgerEntry)
	now    func() time.Time

	mu     sync.Mutex
	groups map[rejectKey]*rejectGroup
	timer  *time.Timer
}

type rejectKey struct{ reason, peer, addr, route string }

type rejectGroup struct {
	count       int
	first, last time.Time
	samples     []string // claimed node IDs of an unknown_peer group
}

// maxClaimedSamples bounds the claimed node IDs kept per unknown_peer
// summary.
const maxClaimedSamples = 5

const maxRejectKeys = 1024

// NewRejectionAggregator returns an aggregator that writes each
// window's summaries with write, window after the first refusal in it.
func NewRejectionAggregator(window time.Duration, write func(api.LedgerEntry)) *RejectionAggregator {
	return &RejectionAggregator{window: window, write: write, now: time.Now, groups: map[rejectKey]*rejectGroup{}}
}

// Record counts one refusal. peerID is the claimed node ID when it is
// well-formed, else ""; remoteAddr is the request's RemoteAddr; route
// is a route template.
func (a *RejectionAggregator) Record(reason, peerID, remoteAddr, route string) {
	// An unknown_peer's claimed node ID is attacker-chosen: grouping on it
	// would let one address fill every group with fresh IDs (#6232), so
	// it is kept only as a bounded sample.
	sample := ""
	if reason == ReasonUnknownPeer {
		sample, peerID = peerID, ""
	}
	k := rejectKey{reason: reason, peer: peerID, addr: clientKey(remoteAddr), route: route}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	g := a.groups[k]
	if g == nil {
		if len(a.groups) >= maxRejectKeys {
			k = rejectKey{reason: reason, addr: "other"}
			g = a.groups[k]
		}
		if g == nil {
			g = &rejectGroup{first: now}
			a.groups[k] = g
		}
	}
	g.count++
	g.last = now
	if sample != "" && len(g.samples) < maxClaimedSamples && !slices.Contains(g.samples, sample) {
		g.samples = append(g.samples, sample)
	}
	if a.timer == nil {
		a.timer = time.AfterFunc(a.window, a.Flush)
	}
}

// Flush writes the pending summaries now and starts a new window.
func (a *RejectionAggregator) Flush() {
	a.mu.Lock()
	groups := a.groups
	a.groups = map[rejectKey]*rejectGroup{}
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.mu.Unlock()
	for k, g := range groups {
		e := RejectedSummaryEntry(k.peer, k.reason, k.route, k.addr, g.count, g.first, g.last)
		if len(g.samples) > 0 {
			e.Metadata["sample_claimed_peers"] = g.samples
		}
		a.write(e)
	}
}

// RejectedSummaryEntry is a federation_rejected entry standing for count
// unauthenticated refusals from one client address in one window.
func RejectedSummaryEntry(peerID, reason, route, addr string, count int, first, last time.Time) api.LedgerEntry {
	e := RejectedEntry(peerID, reason, route)
	e.IP = addr
	e.Metadata["count"] = count
	e.Metadata["client"] = addr
	e.Metadata["first_seen"] = first.UTC().Format(time.RFC3339Nano)
	e.Metadata["last_seen"] = last.UTC().Format(time.RFC3339Nano)
	return e
}
