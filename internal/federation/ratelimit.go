package federation

import (
	"sync"

	"golang.org/x/time/rate"
)

const (
	DefaultOutboundQPS = 50
	DefaultInboundQPS  = 100
)

type RateLimiterPool struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	defaults map[string]float64
	fallback float64
}

func NewOutboundPool(peers []Peer) *RateLimiterPool {
	defaults := make(map[string]float64, len(peers))
	for _, p := range peers {
		qps := float64(p.RateLimitQPS)
		if qps <= 0 {
			qps = DefaultOutboundQPS
		}
		defaults[p.ID] = qps
	}
	return &RateLimiterPool{
		limiters: make(map[string]*rate.Limiter, len(peers)),
		defaults: defaults,
		fallback: DefaultOutboundQPS,
	}
}

func NewInboundPool(fallbackQPS float64) *RateLimiterPool {
	if fallbackQPS <= 0 {
		fallbackQPS = DefaultInboundQPS
	}
	return &RateLimiterPool{
		limiters: make(map[string]*rate.Limiter),
		defaults: make(map[string]float64),
		fallback: fallbackQPS,
	}
}

func (p *RateLimiterPool) Allow(peerID string) bool {
	p.mu.Lock()
	lim, ok := p.limiters[peerID]
	if !ok {
		qps := p.fallback
		if d, exists := p.defaults[peerID]; exists {
			qps = d
		}
		lim = rate.NewLimiter(rate.Limit(qps), int(qps))
		p.limiters[peerID] = lim
	}
	p.mu.Unlock()
	return lim.Allow()
}
