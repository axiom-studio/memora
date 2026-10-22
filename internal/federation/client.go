package federation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

const (
	HeaderFederationAuth  = "X-Memora-Federation-Auth"
	HeaderFederationPath  = "X-Memora-Federation-Path"
	HeaderFederationID    = "X-Memora-Federation-ID"
	HeaderLocalInstanceID = "X-Memora-Local-Instance-ID"
)

// PeerClient handles outbound HTTP requests to a single federation peer.
type PeerClient struct {
	peer         Peer
	httpClient   *http.Client
	federationID string
	instanceID   string
	// cleartextKeyOK allows the peer key on an http:// request: the
	// peer's host is loopback or in federation.insecure_peer_hosts.
	cleartextKeyOK bool
	host           string // the URL's host, without port
}

func newPeerClient(p Peer, federationID, nodeID string, policy *urlPolicy) (*PeerClient, error) {
	u, err := url.Parse(p.Endpoint)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// No proxy: the dial check below must see the peer's own address.
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   policy.dialControl(u.Hostname()),
	}).DialContext
	if p.TrustMode == TrustMTLS {
		tlsCfg, err := buildMTLSConfig(p)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}
	return &PeerClient{
		peer: p,
		httpClient: &http.Client{
			Transport: transport,
			// A ceiling only: each call's context carries the peer's
			// timeout or the request's timeout_ms, which may be longer.
			Timeout: MaxFederatedTimeout,
			// A peer never redirects a federation call. Following one
			// would re-send the query and X-Memora-Federation-Auth (Go
			// strips only Authorization and cookies) to whatever host the
			// Location names, so a 3xx is returned as the answer and
			// becomes a *PeerError.
			CheckRedirect: noRedirects,
		},
		federationID:   federationID,
		instanceID:     nodeID,
		cleartextKeyOK: policy.cleartextAllowed(normHost(u.Hostname())),
		host:           u.Hostname(),
	}, nil
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// buildMTLSConfig trusts the peer's CA (TLSCert) when set, and presents
// this node's client certificate when one is configured.
func buildMTLSConfig(p Peer) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.TLSCert != "" {
		caCert, err := os.ReadFile(p.TLSCert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %s: %w", p.TLSCert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert from %s", p.TLSCert)
		}
		cfg.RootCAs = pool
	}
	if p.ClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(p.ClientCertFile, p.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (c *PeerClient) setHeaders(req *http.Request, path string) {
	req.Header.Set(HeaderFederationID, c.federationID)
	req.Header.Set(HeaderLocalInstanceID, c.instanceID)
	if path != "" {
		req.Header.Set(HeaderFederationPath, path)
	}
	// The registry refuses http:// peers it may not send keys to; this
	// keeps the key off the wire should one get through.
	if c.peer.APIKey != "" && (req.URL.Scheme == "https" || c.cleartextKeyOK) {
		req.Header.Set(HeaderFederationAuth, "Bearer "+c.peer.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

// Search sends a node-wide search to the peer's POST /v1/federation/search
// and returns its hits. The peer embeds the query text itself. A non-200
// answer is a *PeerError carrying the peer's status and error_code.
func (c *PeerClient) Search(ctx context.Context, searchReq api.FederationSearchRequest, federationPath string) (*api.RecallResponse, error) {
	body, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("marshal search request: %w", err)
	}
	url := strings.TrimRight(c.peer.Endpoint, "/") + "/v1/federation/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, federationPath)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer %s search: %w", c.peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var env struct {
			ErrorCode string `json:"error_code"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&env)
		return nil, newPeerError(resp.StatusCode, env.ErrorCode)
	}
	var result api.RecallResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("peer %s search decode: %w", c.peer.Name, err)
	}
	return &result, nil
}

// PeerError is a peer's non-200 answer.
type PeerError struct {
	Status int
	// Code is the peer's error_code when it is one a Memora node sends
	// (knownPeerCodes), else "". The peer controls that string, and
	// Error() text reaches callers, logs and MCP agents verbatim, so an
	// unknown code is never kept (#6174).
	Code string
}

func newPeerError(status int, code string) *PeerError {
	if !knownPeerCodes[code] {
		code = ""
	}
	return &PeerError{Status: status, Code: code}
}

func (e *PeerError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("peer answered HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("peer answered HTTP %d", e.Status)
}

// FetchMemory gets a full memory from the peer's
// GET /v1/federation/memories/{id}. A memory the peer does not hold is
// types.ErrNotFound; any other non-200 answer is a *PeerError.
func (c *PeerClient) FetchMemory(ctx context.Context, memoryID, federationPath string) (*api.MemoryEnvelope, error) {
	url := strings.TrimRight(c.peer.Endpoint, "/") + "/v1/federation/memories/" + memoryID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, federationPath)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer %s fetch: %w", c.peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var env struct {
			ErrorCode string `json:"error_code"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&env)
		if resp.StatusCode == http.StatusNotFound && env.ErrorCode == "not_found" {
			return nil, fmt.Errorf("%w: memory %s is not on node %s", types.ErrNotFound, memoryID, c.peer.ID)
		}
		return nil, newPeerError(resp.StatusCode, env.ErrorCode)
	}
	var envelope api.MemoryEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("peer %s fetch decode: %w", c.peer.Name, err)
	}
	if envelope.Memory == nil || envelope.Memory.ID != memoryID {
		return nil, fmt.Errorf("peer %s fetch: answered with a different memory", c.peer.Name)
	}
	return &envelope, nil
}

// PeerName returns the human-readable name of the peer.
func (c *PeerClient) PeerName() string { return c.peer.Name }

// PeerID returns the peer's ID.
func (c *PeerClient) PeerID() string { return c.peer.ID }

// RequestTimeout returns the configured timeout for this peer.
func (c *PeerClient) RequestTimeout() time.Duration { return c.peer.DefaultRequestTimeout() }
