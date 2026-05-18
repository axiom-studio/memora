package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"sync"

	"github.com/axiom-studio/memora/internal/service"
)

// Server hosts the MCP request handlers and the tool dispatch table.
type Server struct {
	svc    *service.Service
	logger *stdlog.Logger
	mu     sync.Mutex // serializes writes to a single transport
}

// NewServer returns a ready Server.
func NewServer(svc *service.Service, logger *stdlog.Logger) *Server {
	if logger == nil {
		logger = stdlog.Default()
	}
	return &Server{svc: svc, logger: logger}
}

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

func (s *Server) handle(ctx context.Context, req request) response {
	switch req.Method {
	case methodInitialize:
		return okResponse(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "memora-core", "version": "0.1.0"},
		})
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
