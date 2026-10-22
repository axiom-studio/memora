package federation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/axiom-studio/memora/internal/config"
)

// ErrPeerAddressRefused marks a peer call refused because the peer's
// address is loopback, private or otherwise non-public and the operator
// has not allowed it in federation.private_peer_hosts.
var ErrPeerAddressRefused = errors.New("federation: peer address is not public and not in federation.private_peer_hosts")

// urlPolicy decides which peer URLs this node may call. It is set by the
// operator in the config file only, never through the peer API, so a
// holder of the node's API key cannot widen it:
//
//   - https is always allowed. http sends the peer key in cleartext, so
//     it is allowed only to a loopback host or a host listed in
//     federation.insecure_peer_hosts.
//   - A peer may reach a non-public address (loopback, private,
//     link-local, CGNAT, unspecified, multicast, reserved) only when its
//     URL host is listed in federation.private_peer_hosts, or the address
//     is inside a CIDR listed there. This is checked on the URL when a
//     peer is added and on every connection, so a hostname that later
//     resolves to an internal address (DNS rebinding) is refused too.
type urlPolicy struct {
	insecureHosts map[string]bool
	privateHosts  map[string]bool
	privateNets   []netip.Prefix
}

func newURLPolicy(fc config.FederationConfig) (*urlPolicy, error) {
	p := &urlPolicy{insecureHosts: map[string]bool{}, privateHosts: map[string]bool{}}
	for _, h := range fc.InsecurePeerHosts {
		h = normHost(h)
		if h == "" {
			return nil, errors.New("federation.insecure_peer_hosts: empty entry")
		}
		p.insecureHosts[h] = true
	}
	for _, h := range fc.PrivatePeerHosts {
		if strings.Contains(h, "/") {
			pfx, err := netip.ParsePrefix(strings.TrimSpace(h))
			if err != nil {
				return nil, fmt.Errorf("federation.private_peer_hosts: %q: %w", h, err)
			}
			p.privateNets = append(p.privateNets, pfx.Masked())
			continue
		}
		h = normHost(h)
		if h == "" {
			return nil, errors.New("federation.private_peer_hosts: empty entry")
		}
		p.privateHosts[h] = true
	}
	return p, nil
}

// normHost lowercases a host and strips IPv6 brackets, so entries match
// url.URL.Hostname().
func normHost(h string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
}

// checkURL validates a peer URL against the policy without resolving
// it: the scheme, cleartext transport and, for an IP literal host, the
// address.
func (p *urlPolicy) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid url %q: must be http(s)://host[:port]", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("url %q must not carry credentials", raw)
	}
	host := normHost(u.Hostname())
	if u.Scheme == "http" && !p.cleartextAllowed(host) {
		return nil, fmt.Errorf("url %q is http://, which would send the peer key in cleartext; use https, or list %q in federation.insecure_peer_hosts", raw, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !p.allowAddr(host, ip) {
		return nil, fmt.Errorf("url %q: %w", raw, ErrPeerAddressRefused)
	}
	return u, nil
}

// cleartextAllowed reports whether the peer key may be sent to host
// over http.
func (p *urlPolicy) cleartextAllowed(host string) bool {
	return isLoopbackHost(host) || p.insecureHosts[host]
}

// checkResolved resolves a peer URL's hostname and refuses it when any
// address it resolves to is not allowed. A name that does not resolve
// passes: every connection is checked again when it is dialed.
func (p *urlPolicy) checkResolved(ctx context.Context, u *url.URL) error {
	host := normHost(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil || p.privateHosts[host] {
		return nil // checked by checkURL, or allowed by name
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	for _, ip := range addrs {
		if !p.allowAddr(host, ip) {
			return fmt.Errorf("url host %q resolves to %s: %w", host, ip.Unmap(), ErrPeerAddressRefused)
		}
	}
	return nil
}

// allowAddr reports whether a connection for a peer whose URL host is
// host may go to ip.
func (p *urlPolicy) allowAddr(host string, ip netip.Addr) bool {
	ip = ip.Unmap()
	if isPublicAddr(ip) || p.privateHosts[host] {
		return true
	}
	for _, n := range p.privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// dialControl returns a net.Dialer Control function that refuses a
// connection to an address the policy does not allow for host. It runs
// after name resolution, on the address actually dialed.
func (p *urlPolicy) dialControl(host string) func(network, address string, _ syscall.RawConn) error {
	host = normHost(host)
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrPeerAddressRefused, address)
		}
		if !p.allowAddr(host, ap.Addr()) {
			return fmt.Errorf("%w: %s", ErrPeerAddressRefused, ap.Addr())
		}
		return nil
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// nonPublicNets are ranges netip has no predicate for.
var nonPublicNets = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: may embed any IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2002::/16"),       // 6to4: may embed any IPv4
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
}

// isPublicAddr reports whether ip is a globally routable unicast address.
func isPublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	for _, n := range nonPublicNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
