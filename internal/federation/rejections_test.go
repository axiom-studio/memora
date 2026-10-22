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
	if n := len(l.lims); n > 10 {
		t.Fatalf("%d limiters held, bound 10", n)
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
