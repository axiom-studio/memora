package mcp

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"sync"
	"sync/atomic"

	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/pkg/adapter"
)

// Config configures an MCP Server. Zero-valued Config disables auth
// (local-dev convenience for single-tenant stdio use).
type Config struct {
	// APIKey is the shared secret a client must prove possession of by
	// passing `params.apiKey` on the `initialize` JSON-RPC method.
	// Empty disables auth — every initialize succeeds and every tool
	// call is dispatched. The stdio transport is intrinsically local
	// (no network), so an empty APIKey is the documented single-tenant
	// local-dev mode. Operators who run memora-core mcp in any other
	// context MUST set MEMORA_API_KEY.
	APIKey string
}

// Server hosts the MCP request handlers and the tool dispatch table.
type Server struct {
	svc         *service.Service
	logger      *stdlog.Logger
	apiKey      string
	mu          sync.Mutex  // serializes writes to a single transport
	initialized atomic.Bool // flipped by a successful initialize handshake
	pinnedAgent string      // set once at initialize when auth verifies identity; read-only thereafter
}

// NewServer returns a ready Server with auth disabled. Equivalent to
// NewServerWithConfig(svc, logger, Config{}).
func NewServer(svc *service.Service, logger *stdlog.Logger) *Server {
	return NewServerWithConfig(svc, logger, Config{})
}

// NewServerWithConfig returns a ready Server with the supplied auth
// config. When cfg.APIKey is non-empty the server requires a valid
// initialize handshake before any other JSON-RPC method is dispatched.
func NewServerWithConfig(svc *service.Service, logger *stdlog.Logger, cfg Config) *Server {
	if logger == nil {
		logger = stdlog.Default()
	}
	return &Server{svc: svc, logger: logger, apiKey: cfg.APIKey}
}

func (s *Server) requireAuth() bool { return s.apiKey != "" }

// ServeStdio reads JSON-RPC frames from r, writes responses to w,
// and blocks until r returns io.EOF.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // up to 4 MB per frame
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.logger.Printf("mcp: bad frame: %v", err)
			continue
		}
		resp := s.handle(ctx, req)
		// Notifications carry no id; skip the response in that case.
		if len(req.ID) == 0 {
			continue
		}
		s.mu.Lock()
		err := enc.Encode(resp)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// initializeParams is the subset of MCP's initialize-method params
// that this server cares about.
type initializeParams struct {
	APIKey           string         `json:"apiKey"`
	AgentID          string         `json:"agentId"`
	IdentityProvider string         `json:"identityProvider"`
	IdentityProof    map[string]any `json:"identityProof"`
}

func (s *Server) handle(ctx context.Context, req request) response {
	if req.Method == methodInitialize {
		var p initializeParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResponse(req.ID, -32602, err.Error(), nil)
		}
		if s.requireAuth() {
			if subtle.ConstantTimeCompare([]byte(p.APIKey), []byte(s.apiKey)) != 1 {
				return errResponse(req.ID, ErrCodeUnauthorized, "unauthorized: invalid api key", nil)
			}
			if p.AgentID != "" {
				prov := s.svc.IdentityFor(p.IdentityProvider)
				if err := prov.Verify(ctx, adapter.IdentityVerifyInput{
					AgentID:       p.AgentID,
					IdentityProof: p.IdentityProof,
				}); err != nil {
					return errResponse(req.ID, ErrCodeUnauthorized, "unauthorized: agent verification failed: "+err.Error(), nil)
				}
				s.pinnedAgent = p.AgentID
			}
		}
		s.initialized.Store(true)
		return okResponse(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "memora-core", "version": "0.1.0"},
		})
	}
	// All non-initialize methods require a prior successful initialize
	// when auth is enabled. The check applies to ping too — otherwise
	// it'd be a probe that confirms the server is reachable without
	// proving possession of the API key.
	if s.requireAuth() && !s.initialized.Load() {
		return errResponse(req.ID, ErrCodeUnauthorized, "unauthorized: must initialize before "+req.Method, nil)
	}
	switch req.Method {
	case methodPing:
		return okResponse(req.ID, map[string]any{})
	case methodToolsList:
		return okResponse(req.ID, map[string]any{"tools": toolCatalog()})
	case methodToolsCall:
		return s.handleToolCall(ctx, req)
	default:
		return errResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method), nil)
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, req request) response {
	var p toolCallParams
	if err := decodeParams(req.Params, &p); err != nil {
		return errResponse(req.ID, -32602, err.Error(), nil)
	}
	if s.pinnedAgent != "" {
		ctx = context.WithValue(ctx, ctxKeyAgent, s.pinnedAgent)
	}
	result, err := s.dispatchTool(ctx, p.Name, p.Arguments)
	if err != nil {
		return errResponse(req.ID, -32603, err.Error(), map[string]any{"tool": p.Name})
	}
	// MCP wraps tool output in a "content" array. We emit a single
	// text item with the JSON-encoded result so clients can JSON-parse.
	b, _ := json.Marshal(result)
	return okResponse(req.ID, map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": string(b),
		}},
		"isError": false,
	})
}
