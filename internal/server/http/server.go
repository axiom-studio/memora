// Package http hosts the Memora REST surface. It wires the service
// layer to net/http endpoints with a small middleware chain
// (request-id, CORS, body-size, structured log, recover, auth, agent identity).
package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Config is the boot-time configuration for the HTTP server.
type Config struct {
	Addr            string
	APIKey          string // single-tenant default key; "" disables auth (local dev)
	Service         *service.Service
	Logger          *slog.Logger
	Timeout         time.Duration
	Mode            string // single-tenant | multi-tenant
	MaxBodyBytes    int64  // request body size limit (0 = 8 MiB default)
	AllowNoAuth     bool   // explicit opt-in for empty MEMORA_API_KEY
	AllowedOrigins  []string // CORS: origins that may call the API; empty = no CORS headers

	allowedOriginSet map[string]bool // populated by New from AllowedOrigins
}

// Server is the assembled HTTP server.
type Server struct {
	cfg Config
	mux *http.ServeMux
	srv *http.Server
}

// DefaultMaxBodyBytes caps request bodies at 8 MiB. Memora's largest
// expected single-request payload is a full Memory body — anything
// larger is almost certainly an abuse vector.
const DefaultMaxBodyBytes = 8 * 1024 * 1024

// New returns a ready Server bound to cfg.Addr.
func New(cfg Config) *Server {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if cfg.APIKey == "" && !cfg.AllowNoAuth {
		host := cfg.Addr
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
			cfg.Logger.Warn("serving with no API key", "addr", cfg.Addr)
		}
	}
	if len(cfg.AllowedOrigins) > 0 {
		cfg.allowedOriginSet = make(map[string]bool, len(cfg.AllowedOrigins))
		for _, o := range cfg.AllowedOrigins {
			cfg.allowedOriginSet[strings.ToLower(o)] = true
		}
	}
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, mux: mux}
	s.routes()
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Timeout,
		WriteTimeout:      cfg.Timeout + 5*time.Second,
	}
	return s
}

// Handler returns the assembled http.Handler (middleware + routes) for
// use with httptest.NewServer in integration tests.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// ListenAndServe blocks until the server fails or Shutdown is called.
func (s *Server) ListenAndServe() error { return s.srv.ListenAndServe() }

// Shutdown drains the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = types.NewID("req_")
		}
		w.Header().Set("X-Request-ID", reqID)

		// CORS (outermost — must run before auth so preflight succeeds).
		if len(s.cfg.allowedOriginSet) > 0 {
			origin := r.Header.Get("Origin")
			if s.cfg.allowedOriginSet["*"] || s.cfg.allowedOriginSet[strings.ToLower(origin)] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Memora-Agent-Id, Memora-Identity-Provider, X-Request-ID, X-Memora-Workspace, If-Match")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// Body-size limit (defense against memory-pressure DoS via huge JSON).
		if r.Body != nil && s.cfg.MaxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}

		// Auth: API key (skipped when no key configured — local-dev mode).
		if s.cfg.APIKey != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				s.writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token", nil)
				return
			}
			provided := auth[len("Bearer "):]
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.APIKey)) != 1 {
				s.writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token", nil)
				return
			}
		}

		// Per-request timeout from Config.Timeout.
		ctx := r.Context()
		ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()

		// Multi-tenant: resolve workspace from header when in multi-tenant mode.
		if s.cfg.Mode == "multi-tenant" {
			if ws := r.Header.Get("X-Memora-Workspace"); ws != "" {
				ctx = context.WithValue(ctx, ctxKeyWorkspace, ws)
			}
		}

		// Agent identity: enforce on write verbs.
		if isWriteVerb(r.Method, r.URL.Path) {
			agentID := r.Header.Get("Memora-Agent-Id")
			if agentID == "" {
				s.writeError(w, http.StatusBadRequest, "missing_agent_id", "Memora-Agent-Id header required on writes", nil)
				return
			}
			provName := r.Header.Get("Memora-Identity-Provider")
			if provName == "" {
				provName = string(types.IdentityProviderOpaque)
			}
			prov := s.cfg.Service.IdentityFor(provName)
			if err := prov.Verify(ctx, adapter.IdentityVerifyInput{AgentID: agentID, RequestID: reqID}); err != nil {
				s.writeError(w, http.StatusUnauthorized, "agent_verification_failed", err.Error(), nil)
				return
			}
			ctx = context.WithValue(ctx, ctxKeyAgent, agentID)

			// Auto-register agent after successful verification.
			if s.cfg.Service != nil && s.cfg.Service.Primary != nil {
				wsID := workspaceFromPath(r.URL.Path)
				if wsID != "" {
					_ = s.cfg.Service.Primary.RegisterAgent(ctx, &types.Agent{
						AgentID:          agentID,
						WorkspaceID:      wsID,
						IdentityProvider: provName,
					})
				}
			}
		}
		ctx = context.WithValue(ctx, ctxKeyRequestID, reqID)

		// Wrap response writer to capture status for logs.
		rw := &recorder{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				s.cfg.Logger.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "req_id", reqID, "panic", rec)
				if !rw.wroteHeader {
					s.writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprint(rec), nil)
				}
			}
			s.cfg.Logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "dur_ms", time.Since(start).Milliseconds(), "req_id", reqID)
		}()
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyAgent
	ctxKeyWorkspace
)

func agentFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAgent).(string)
	return v
}

type recorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

// Determine whether a request is a memory/edge write that must carry
// Memora-Agent-Id. Workspace / Collection / Agent admin endpoints are
// deployer-controlled and don't require agent attribution.
func isWriteVerb(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if strings.HasPrefix(path, "/healthz") || strings.HasPrefix(path, "/readyz") || strings.HasPrefix(path, "/metrics") {
		return false
	}
	// Workspace / collection / agent CRUD: no agent_id required.
	// Heuristic: anything ending with /memories or /memories/... and any /edges* / /graph/* / /recall is an agent write.
	if strings.Contains(path, "/memories") || strings.Contains(path, "/edges") || strings.Contains(path, "/graph/") {
		return true
	}
	return false
}

// workspaceFromPath extracts the workspace ID from /v1/workspaces/{ws_id}/...
func workspaceFromPath(path string) string {
	const prefix = "/v1/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return rest
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.ErrorEnvelope{ErrorCode: code, Message: message, Details: details})
}

func (s *Server) writeErrorFromService(w http.ResponseWriter, err error) {
	status := service.HTTPStatus(err)
	s.writeError(w, status, service.ErrorCode(err), err.Error(), nil)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeJSON(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}
