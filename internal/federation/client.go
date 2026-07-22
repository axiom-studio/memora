package federation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

// ClientPool holds a PeerClient per registered peer.
type ClientPool struct {
	clients      map[string]*PeerClient
	federationID string
	instanceID   string
}

// NewClientPool creates a PeerClient for each peer in the registry.
func NewClientPool(reg *Registry) (*ClientPool, error) {
	if reg == nil {
		return nil, nil
	}
	pool := &ClientPool{
		clients:      make(map[string]*PeerClient, len(reg.peers)),
		federationID: reg.FederationID,
		instanceID:   reg.FederationID,
	}
	for _, p := range reg.peers {
		c, err := newPeerClient(p, reg.FederationID)
		if err != nil {
			return nil, fmt.Errorf("peer %s: %w", p.Name, err)
		}
		pool.clients[p.ID] = c
	}
	return pool, nil
}

func newPeerClient(p Peer, federationID string) (*PeerClient, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p.TrustMode == TrustMTLS && p.TLSCert != "" {
		tlsCfg, err := buildMTLSConfig(p.TLSCert)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}
	return &PeerClient{
		peer: p,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   p.DefaultRequestTimeout(),
		},
		federationID: federationID,
		instanceID:   federationID,
	}, nil
}

func buildMTLSConfig(caCertPath string) (*tls.Config, error) {
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", caCertPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert from %s", caCertPath)
	}
	return &tls.Config{RootCAs: pool}, nil
}

func (c *PeerClient) setHeaders(req *http.Request, path string) {
	req.Header.Set(HeaderFederationID, c.federationID)
	req.Header.Set(HeaderLocalInstanceID, c.instanceID)
	if path != "" {
		req.Header.Set(HeaderFederationPath, path)
	}
	if c.peer.TrustMode == TrustAPIKey && c.peer.APIKey != "" {
		req.Header.Set(HeaderFederationAuth, "Bearer "+c.peer.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

// Recall sends a recall query to this peer and returns the response.
func (c *PeerClient) Recall(ctx context.Context, wsID string, recallReq api.RecallRequest, federationPath string) (*api.RecallResponse, error) {
	body, err := json.Marshal(recallReq)
	if err != nil {
		return nil, fmt.Errorf("marshal recall request: %w", err)
	}
	url := fmt.Sprintf("%s/v1/workspaces/%s/recall", strings.TrimRight(c.peer.Endpoint, "/"), wsID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, federationPath)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer %s recall: %w", c.peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer %s recall: HTTP %d", c.peer.Name, resp.StatusCode)
	}
	var result api.RecallResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("peer %s recall decode: %w", c.peer.Name, err)
	}
	return &result, nil
}

// Lookup fetches a single memory from this peer.
func (c *PeerClient) Lookup(ctx context.Context, wsID, memoryID, federationPath string) (*types.Memory, error) {
	url := fmt.Sprintf("%s/v1/workspaces/%s/memories/%s", strings.TrimRight(c.peer.Endpoint, "/"), wsID, memoryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, federationPath)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer %s lookup: %w", c.peer.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer %s lookup: HTTP %d", c.peer.Name, resp.StatusCode)
	}
	var envelope api.MemoryEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("peer %s lookup decode: %w", c.peer.Name, err)
	}
	return envelope.Memory, nil
}

// Client returns the PeerClient for a given peer ID, or nil.
func (p *ClientPool) Client(peerID string) *PeerClient {
	if p == nil {
		return nil
	}
	return p.clients[peerID]
}

// PeerName returns the human-readable name of the peer.
func (c *PeerClient) PeerName() string { return c.peer.Name }

// PeerID returns the peer's ID.
func (c *PeerClient) PeerID() string { return c.peer.ID }

// RequestTimeout returns the configured timeout for this peer.
func (c *PeerClient) RequestTimeout() time.Duration { return c.peer.DefaultRequestTimeout() }
