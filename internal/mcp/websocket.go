package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"nhooyr.io/websocket"

	"github.com/axiom-studio/memora/internal/service"
)

// WebSocketHandler returns an http.Handler that upgrades connections
// to WebSocket and runs the MCP JSON-RPC loop. Each connection gets
// its own Server with independent initialized/pinned-agent state.
//
// Auth is checked on the HTTP upgrade: the Authorization header must
// carry "Bearer <apiKey>" (constant-time compare). When apiKey is
// empty, auth is disabled (local-dev mode).
func WebSocketHandler(svc *service.Service, cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.APIKey != "" {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(cfg.APIKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		conn.SetReadLimit(4 * 1024 * 1024)

		s := NewServerWithConfig(svc, nil, cfg)
		ctx := r.Context()

		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req request
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			resp := s.handle(ctx, req)
			if len(req.ID) == 0 {
				continue
			}
			out, _ := json.Marshal(resp)
			if err := conn.Write(ctx, websocket.MessageText, out); err != nil {
				return
			}
		}
	})
}

// MountMCP registers the WebSocket MCP handler on the given mux at
// the /mcp path. Convenience for wiring into the HTTP server.
func MountMCP(mux *http.ServeMux, svc *service.Service, cfg Config) {
	mux.Handle("/mcp", WebSocketHandler(svc, cfg))
}
