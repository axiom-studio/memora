package federation

import (
	"testing"
)

func TestOutboundPool_Allow(t *testing.T) {
	peers := []Peer{
		{ID: "peer1", RateLimitQPS: 10},
		{ID: "peer2", RateLimitQPS: 0}, // should use default
	}
	pool := NewOutboundPool(peers)

	// First call should always be allowed (burst)
	if !pool.Allow("peer1") {
		t.Error("first call to peer1 should be allowed")
	}
	if !pool.Allow("peer2") {
		t.Error("first call to peer2 should be allowed")
	}
}

func TestOutboundPool_Exhaustion(t *testing.T) {
	peers := []Peer{{ID: "peer1", RateLimitQPS: 2}}
	pool := NewOutboundPool(peers)

	// Burst of 2, then should be denied
	pool.Allow("peer1")
	pool.Allow("peer1")
	if pool.Allow("peer1") {
		t.Error("third call should be rate-limited (burst=2)")
	}
}

func TestInboundPool_Allow(t *testing.T) {
	pool := NewInboundPool(100)
	if !pool.Allow("unknown_peer") {
		t.Error("first call should be allowed for unknown peer")
	}
}

func TestInboundPool_Exhaustion(t *testing.T) {
	pool := NewInboundPool(1)
	pool.Allow("peer_a")
	if pool.Allow("peer_a") {
		t.Error("second call should be rate-limited (burst=1)")
	}
}

func TestInboundPool_PerPeerIsolation(t *testing.T) {
	pool := NewInboundPool(1)
	pool.Allow("peer_a")
	// peer_b should have its own bucket
	if !pool.Allow("peer_b") {
		t.Error("peer_b should not be affected by peer_a exhaustion")
	}
}

func TestOutboundPool_UnknownPeer(t *testing.T) {
	pool := NewOutboundPool(nil)
	if !pool.Allow("never_configured") {
		t.Error("unknown peer should get fallback limiter")
	}
}
