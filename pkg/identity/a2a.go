package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// AgentCard is the JSON envelope fetched from the agent_card_url.
type AgentCard struct {
	AgentID   string `json:"agent_id"`
	PublicKey string `json:"public_key"` // base64-encoded Ed25519 public key
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
}

type cachedCard struct {
	card      AgentCard
	fetchedAt time.Time
}

// A2A verifies agent identity via the Google A2A AgentCard protocol.
// The agent presents {agent_card_url, signature}; the provider fetches
// the card, checks the agent_id matches, and verifies the Ed25519
// signature over the agent_id using the card's public key.
type A2A struct {
	Client    *http.Client
	CacheTTL  time.Duration
	AllowHTTP bool // allow http:// URLs (default: require https://)

	mu    sync.RWMutex
	cache map[string]cachedCard
}

func (a *A2A) Name() string { return string(types.IdentityProviderA2A) }

func (a *A2A) Capabilities() adapter.IdentityCapabilities {
	return adapter.IdentityCapabilities{RequiresProof: true, SupportsRotation: true}
}

func (a *A2A) Verify(ctx context.Context, in adapter.IdentityVerifyInput) error {
	cardURL, _ := in.IdentityProof["agent_card_url"].(string)
	sig, _ := in.IdentityProof["signature"].(string)
	if cardURL == "" || sig == "" {
		return fmt.Errorf("a2a: identity_proof requires agent_card_url and signature")
	}

	// Skip URL validation when a custom Client is injected (tests, allow-listed transports).
	if a.Client == nil {
		if err := validateCardURL(cardURL, a.AllowHTTP); err != nil {
			return fmt.Errorf("a2a: %w", err)
		}
	}

	card, err := a.fetchCard(ctx, in.AgentID, cardURL)
	if err != nil {
		return fmt.Errorf("a2a: fetch card: %w", err)
	}

	if card.AgentID != in.AgentID {
		return fmt.Errorf("a2a: card agent_id %q does not match request agent_id %q", card.AgentID, in.AgentID)
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(card.PublicKey)
	if err != nil {
		return fmt.Errorf("a2a: invalid public_key encoding: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("a2a: public_key must be %d bytes, got %d", ed25519.PublicKeySize, len(pubKeyBytes))
	}

	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("a2a: invalid signature encoding: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), []byte(in.AgentID), sigBytes) {
		return fmt.Errorf("a2a: signature verification failed")
	}

	return nil
}

func validateCardURL(rawURL string, allowHTTP bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid agent_card_url: %w", err)
	}
	if allowHTTP {
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("agent_card_url scheme must be https or http, got %q", u.Scheme)
		}
	} else {
		if u.Scheme != "https" {
			return fmt.Errorf("agent_card_url scheme must be https, got %q", u.Scheme)
		}
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("agent_card_url resolves to private/reserved IP %s", ip)
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.0.0/16 (IMDS, link-local)
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return ip.IsPrivate()
}

// ssrfSafeDialContext wraps a dialer to reject connections to private IPs
// after DNS resolution — prevents redirect-based SSRF bypasses.
func ssrfSafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if isPrivateIP(ip.IP) {
			return nil, fmt.Errorf("a2a: resolved IP %s is private/reserved", ip.IP)
		}
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func ssrfSafeClient() *http.Client {
	transport := &http.Transport{
		DialContext: ssrfSafeDialContext,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("a2a: too many redirects")
			}
			host := req.URL.Hostname()
			if ip := net.ParseIP(host); ip != nil {
				if isPrivateIP(ip) {
					return fmt.Errorf("a2a: redirect to private/reserved IP %s", ip)
				}
			}
			return nil
		},
	}
}

func (a *A2A) fetchCard(ctx context.Context, agentID, cardURL string) (AgentCard, error) {
	ttl := a.CacheTTL
	if ttl == 0 {
		ttl = 1 * time.Hour
	}

	a.mu.RLock()
	if a.cache != nil {
		if c, ok := a.cache[agentID]; ok && time.Since(c.fetchedAt) < ttl {
			a.mu.RUnlock()
			return c.card, nil
		}
	}
	a.mu.RUnlock()

	client := a.Client
	if client == nil {
		client = ssrfSafeClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return AgentCard{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return AgentCard{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return AgentCard{}, fmt.Errorf("agent card fetch returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return AgentCard{}, err
	}

	var card AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return AgentCard{}, fmt.Errorf("agent card not valid JSON")
	}

	a.mu.Lock()
	if a.cache == nil {
		a.cache = make(map[string]cachedCard)
	}
	a.cache[agentID] = cachedCard{card: card, fetchedAt: time.Now()}
	a.mu.Unlock()

	return card, nil
}
