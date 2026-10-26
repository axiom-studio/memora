package federation

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestClientKey(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.7:5555":                "203.0.113.7",
		"[::ffff:203.0.113.7]:1":          "203.0.113.7",
		"[2001:db8:1:2:aaaa::1]:443":      "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff:ffff::9]:443": "2001:db8:1:2::/64",
		"not-an-addr":                     "not-an-addr",
	} {
		if got := clientKey(addr); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", addr, got, want)
		}
	}
}

// TestAddrLimiter: each address gets its own burst, and the table of
// addresses stays bounded.
func TestAddrLimiter(t *testing.T) {
	l := NewAddrLimiter(3)
	for i := 0; i < 3; i++ {
		if !l.Allow("198.51.100.1:1") {
			t.Fatalf("request %d refused within the burst", i)
		}
	}
	if l.Allow("198.51.100.1:2") {
		t.Fatal("4th request in the same second allowed")
	}
	if !l.Allow("198.51.100.2:1") {
		t.Fatal("another address shares the first one's limit")
	}
	l.maxAddrs = 10
	for i := 0; i < 50; i++ {
		l.Allow(fmt.Sprintf("10.0.0.%d:1", i))
	}
	if n := len(l.lims); n > 10 || l.lru.Len() > 10 {
		t.Fatalf("%d limiters held, bound 10", n)
	}
}

// TestAddrLimiter_Rotation (#6232): rotating through 20,000 /64s gets no
// more than the node-wide new-address allowance, a busy address keeps
// its (spent) limiter instead of a fresh one, and the table evicts the
// least recently used address rather than clearing.
func TestAddrLimiter_Rotation(t *testing.T) {
	start := time.Now()
	l := NewAddrLimiter(200) // 200/s per address, 100 new addresses/s
	l.maxAddrs = 1000
	for i := 0; i < 200; i++ {
		l.Allow("[2001:db8:aaaa:1::1]:1") // spend the busy address's burst
	}
	if l.Allow("[2001:db8:aaaa:1::2]:1") {
		t.Fatal("the busy /64 still had tokens")
	}
	busy := l.lims[clientKey("[2001:db8:aaaa:1::1]:1")].Value.(*addrEntry).lim
	allowed := 0
	for i := 0; i < 20000; i++ {
		if l.Allow(fmt.Sprintf("[2001:db8:%x:%x::1]:443", i/65536+1, i%65536)) {
			allowed++
		}
	}
	// The contract for qps 200 is 100 new addresses/s, burst 100. The
	// allowance is that burst plus what refills while the test runs,
	// which is real time, so a slow runner legitimately admits more
	// (#6362). The constants are the contract, not read back from the
	// limiter, so a weakened new-address rate still fails.
	const wantRate, wantBurst = 100, 100
	allowance := wantBurst + int(wantRate*time.Since(start).Seconds()) + 1
	if allowed > allowance {
		t.Fatalf("%d of 20,000 rotated /64s admitted, want at most the new-address allowance %d", allowed, allowance)
	}
	el, ok := l.lims[clientKey("[2001:db8:aaaa:1::1]:1")]
	if !ok || el.Value.(*addrEntry).lim != busy {
		t.Fatal("rotation evicted or reset the busy /64's limiter")
	}
	if l.lru.Len() > l.maxAddrs {
		t.Fatalf("%d entries, bound %d", l.lru.Len(), l.maxAddrs)
	}
}

// TestRejectionAggregator: refusals are grouped per key into one entry
// per window with their count; past maxRejectKeys groups, the rest are
// counted in one overflow group.
func TestRejectionAggregator(t *testing.T) {
	var mu sync.Mutex
	var got []api.LedgerEntry
	a := NewRejectionAggregator(time.Hour, func(e api.LedgerEntry) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	peer := nid("p")
	for i := 0; i < 500; i++ {
		a.Record(ReasonBadKey, peer, "192.0.2.1:9", RouteFederationSearch)
	}
	a.Record(ReasonUnknownPeer, "", "192.0.2.1:9", RouteFederationMemory)
	a.Flush()
	if len(got) != 2 {
		t.Fatalf("%d entries, want 2", len(got))
	}
	for _, e := range got {
		if e.Op != OpRejected || e.IP != "192.0.2.1" {
			t.Fatalf("entry %+v", e)
		}
		if e.Metadata["reason"] == ReasonBadKey && (e.Metadata["count"] != 500 || e.APIKeyID != peer || e.Target != RouteFederationSearch) {
			t.Fatalf("bad_key summary %+v %v", e, e.Metadata)
		}
	}

	got = nil
	for i := 0; i < maxRejectKeys+300; i++ {
		a.Record(ReasonUnknownPeer, "", fmt.Sprintf("10.%d.%d.1:1", i/256, i%256), RouteFederationSearch)
	}
	a.Flush()
	total := 0
	for _, e := range got {
		total += e.Metadata["count"].(int)
	}
	if len(got) > maxRejectKeys+1 || total != maxRejectKeys+300 {
		t.Fatalf("%d entries counting %d, want at most %d counting %d", len(got), total, maxRejectKeys+1, maxRejectKeys+300)
	}

	// The window flushes by itself.
	got = nil
	b := NewRejectionAggregator(20*time.Millisecond, a.write)
	b.Record(ReasonBadKey, "", "192.0.2.9:1", RouteFederationSearch)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("after the window: %d entries, want 1", len(got))
	}
}

// TestRejectionAggregator_ClaimedIDsDoNotSplitGroups (#6232): 5,000
// unknown_peer refusals from one address, each claiming a fresh node
// ID, are one summary with a bounded sample of the claimed IDs.
func TestRejectionAggregator_ClaimedIDsDoNotSplitGroups(t *testing.T) {
	var got []api.LedgerEntry
	a := NewRejectionAggregator(time.Hour, func(e api.LedgerEntry) { got = append(got, e) })
	for i := 0; i < 5000; i++ {
		a.Record(ReasonUnknownPeer, nid(fmt.Sprint("claimed", i)), "192.0.2.7:1", RouteFederationSearch)
	}
	a.Flush()
	if len(got) != 1 {
		t.Fatalf("%d entries, want 1", len(got))
	}
	e := got[0]
	samples, _ := e.Metadata["sample_claimed_peers"].([]string)
	if e.Metadata["count"] != 5000 || e.APIKeyID != "" || len(samples) == 0 || len(samples) > maxClaimedSamples {
		t.Fatalf("summary = %+v %v", e, e.Metadata)
	}
}

// TestAddrLimiter_EvictsLeastRecentlyUsed: a full table drops its least
// recently used address, not all of them.
func TestAddrLimiter_EvictsLeastRecentlyUsed(t *testing.T) {
	l := NewAddrLimiter(100)
	l.maxAddrs = 3
	for _, a := range []string{"198.51.100.1:1", "198.51.100.2:1", "198.51.100.3:1", "198.51.100.1:2", "198.51.100.4:1"} {
		l.Allow(a)
	}
	_, a := l.lims["198.51.100.1"]
	_, b := l.lims["198.51.100.2"]
	_, d := l.lims["198.51.100.4"]
	if !a || b || !d || l.lru.Len() != 3 {
		t.Fatalf("after eviction: 1=%v 2=%v 4=%v len=%d; want 2 (least recently used) evicted", a, b, d, l.lru.Len())
	}
}
