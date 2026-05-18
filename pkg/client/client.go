// Package client is the Go SDK for Memora. memora-cli is the primary
// consumer; third parties can vendor it directly. v0.1 ships REST-only
// transport; gRPC + MCP-attached transports are follow-on.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Client is the Memora REST client.
type Client struct {
	Endpoint   string
	APIKey     string
	AgentID    string
	Workspace  string
	HTTPClient *http.Client
}

// New returns a Client with a 30s default timeout.
func New(endpoint, apiKey string) *Client {
	return &Client{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) request(ctx context.Context, method, path string, body any, ifMatch string) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.Endpoint + path
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.AgentID != "" {
		req.Header.Set("Memora-Agent-Id", c.AgentID)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	return c.HTTPClient.Do(req)
}

func (c *Client) do(ctx context.Context, method, path string, body any, into any, ifMatch string) error {
	resp, err := c.request(ctx, method, path, body, ifMatch)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rb, &env)
		return &APIError{Status: resp.StatusCode, Envelope: env, RawBody: string(rb)}
	}
	if into != nil && len(rb) > 0 {
		if err := json.Unmarshal(rb, into); err != nil {
			return fmt.Errorf("decode response: %w (body=%s)", err, string(rb))
		}
	}
	return nil
}

// APIError is the typed error returned for non-2xx responses.
type APIError struct {
	Status   int
	Envelope api.ErrorEnvelope
	RawBody  string
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Envelope.ErrorCode != "" {
		return fmt.Sprintf("memora: %d %s: %s", e.Status, e.Envelope.ErrorCode, e.Envelope.Message)
	}
	return fmt.Sprintf("memora: status %d: %s", e.Status, e.RawBody)
}

// IsNotFound returns true on 404.
func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// IsCAS returns true on 412.
func (e *APIError) IsCAS() bool { return e.Status == http.StatusPreconditionFailed }

// IsCASErr returns true if err is a CAS conflict.
func IsCASErr(err error) bool {
	var ae *APIError
	return err != nil && errorsAs(err, &ae) && ae.IsCAS()
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// ---- Workspaces ----

func (c *Client) ListWorkspaces(ctx context.Context) ([]types.Workspace, error) {
	var resp struct {
		Workspaces []types.Workspace `json:"workspaces"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces", nil, &resp, ""); err != nil {
		return nil, err
	}
	return resp.Workspaces, nil
}

func (c *Client) CreateWorkspace(ctx context.Context, req api.CreateWorkspaceRequest) (*types.Workspace, error) {
	var out types.Workspace
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetWorkspace(ctx context.Context, id string) (*types.Workspace, error) {
	var out types.Workspace
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+id, nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWorkspace(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/workspaces/"+id, nil, nil, "")
}

// ---- Collections ----

func (c *Client) ListCollections(ctx context.Context, ws string) ([]types.Collection, error) {
	var resp struct {
		Collections []types.Collection `json:"collections"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+ws+"/collections", nil, &resp, ""); err != nil {
		return nil, err
	}
	return resp.Collections, nil
}

func (c *Client) CreateCollection(ctx context.Context, ws, name string) (*types.Collection, error) {
	var out types.Collection
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/collections", api.CreateCollectionRequest{Name: name}, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Memories ----

func (c *Client) Imprint(ctx context.Context, ws string, req api.ImprintRequest) (*api.ImprintResponse, error) {
	var out api.ImprintResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/memories", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Lookup(ctx context.Context, ws, memID string) (*api.MemoryEnvelope, error) {
	var out api.MemoryEnvelope
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+ws+"/memories/"+memID, nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Update(ctx context.Context, ws, memID, ifMatch string, req api.UpdateRequest) (*api.UpdateResponse, error) {
	var out api.UpdateResponse
	if err := c.do(ctx, http.MethodPut, "/v1/workspaces/"+ws+"/memories/"+memID, req, &out, ifMatch); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Patch(ctx context.Context, ws, memID, ifMatch string, req api.PatchRequest) (*api.PatchResponse, error) {
	var out api.PatchResponse
	if err := c.do(ctx, http.MethodPatch, "/v1/workspaces/"+ws+"/memories/"+memID, req, &out, ifMatch); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Append(ctx context.Context, ws, memID, ifMatch string, req api.AppendRequest) (*api.AppendResponse, error) {
	var out api.AppendResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/memories/"+memID+":append", req, &out, ifMatch); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Forget(ctx context.Context, ws, memID string) (*api.ForgetResponse, error) {
	var out api.ForgetResponse
	if err := c.do(ctx, http.MethodDelete, "/v1/workspaces/"+ws+"/memories/"+memID, nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListMemories(ctx context.Context, ws, collectionID string, limit int) ([]types.Memory, error) {
	u := "/v1/workspaces/" + ws + "/memories"
	q := url.Values{}
	if collectionID != "" {
		q.Set("collection_id", collectionID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if e := q.Encode(); e != "" {
		u += "?" + e
	}
	var resp struct {
		Memories []types.Memory `json:"memories"`
	}
	if err := c.do(ctx, http.MethodGet, u, nil, &resp, ""); err != nil {
		return nil, err
	}
	return resp.Memories, nil
}

// ---- Recall ----

func (c *Client) Recall(ctx context.Context, ws string, req api.RecallRequest) (*api.RecallResponse, error) {
	var out api.RecallResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/recall", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Context Graph ----

func (c *Client) Link(ctx context.Context, ws, srcMemID string, req api.LinkRequest) (*api.LinkResponse, error) {
	var out api.LinkResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/memories/"+srcMemID+"/edges", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Unlink(ctx context.Context, ws, edgeID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/workspaces/"+ws+"/edges/"+edgeID, nil, nil, "")
}

func (c *Client) Neighbors(ctx context.Context, ws string, req api.NeighborsRequest) (*api.NeighborsResponse, error) {
	var out api.NeighborsResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/graph/neighbors", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Traverse(ctx context.Context, ws string, req api.TraverseRequest) (*api.TraverseResponse, error) {
	var out api.TraverseResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/graph/traverse", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GraphStats(ctx context.Context, ws string) (*api.GraphStatsResponse, error) {
	var out api.GraphStatsResponse
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+ws+"/graph/stats", nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Agents ----

func (c *Client) RegisterAgent(ctx context.Context, ws string, req api.RegisterAgentRequest) (*types.Agent, error) {
	var out types.Agent
	if err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+ws+"/agents", req, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListAgents(ctx context.Context, ws string) ([]types.Agent, error) {
	var resp struct {
		Agents []types.Agent `json:"agents"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+ws+"/agents", nil, &resp, ""); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// ---- Health ----

func (c *Client) Health(ctx context.Context) (*api.HealthResponse, error) {
	var out api.HealthResponse
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Ready(ctx context.Context) (*api.HealthResponse, error) {
	var out api.HealthResponse
	if err := c.do(ctx, http.MethodGet, "/readyz", nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Watermarks ----

func (c *Client) WatermarkHistory(ctx context.Context, ws, memID string) ([]types.WatermarkHistoryEntry, error) {
	var resp struct {
		Watermarks []types.WatermarkHistoryEntry `json:"watermarks"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+ws+"/memories/"+memID+"/watermarks", nil, &resp, ""); err != nil {
		return nil, err
	}
	return resp.Watermarks, nil
}

// ---- Ledger ----

func (c *Client) LedgerQuery(ctx context.Context, ws string, q api.LedgerQueryRequest) (*api.LedgerResponse, error) {
	var out api.LedgerResponse
	u := "/v1/workspaces/" + ws + "/ledger"
	if q.Limit > 0 {
		u += "?limit=" + strconv.Itoa(q.Limit)
	}
	if err := c.do(ctx, http.MethodGet, u, nil, &out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}
