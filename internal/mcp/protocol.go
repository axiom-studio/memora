// Package mcp implements the Model Context Protocol server bundled
// with memora-core. v0.1 ships stdio + WebSocket transports and
// exposes one tool per public REST verb (25 tools total).
package mcp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 wire types.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCP method names.
const (
	methodInitialize = "initialize"
	methodToolsList  = "tools/list"
	methodToolsCall  = "tools/call"
	methodPing       = "ping"
)

// Tool is the MCP-facing tool descriptor.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func okResponse(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func errResponse(id json.RawMessage, code int, msg string, data any) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

func decodeParams(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}
