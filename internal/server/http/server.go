// Package http hosts the Memora REST surface. It wires the service
// layer to net/http endpoints with a small middleware chain
// (request-id, structured log, recover, auth, agent identity).
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdlog "log"
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
	Addr         string
	APIKey       string // single-tenant default key; "" disables auth (local dev)
	Service      *service.Service
	Logger       *stdlog.Logger
	Timeout      time.Duration
	Mode         string // single-tenant | multi-tenant
}

// Server is the assembled HTTP server.
type Server struct {
	cfg Config
	mux *http.ServeMux
	srv *http.Server
}

// New returns a ready Server bound to cfg.Addr.
func New(cfg Config) *Server {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = stdlog.Default()
	}
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, mux: mux}
	s.routes()
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

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

		// Auth: API key (skipped when no key configured — local-dev mode).
		if s.cfg.APIKey != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || auth[len("Bearer "):] != s.cfg.APIKey {
				s.writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", nil)
				return
			}
		}
		// Agent identity: enforce on write verbs.
		ctx := r.Context()
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
		}
		ctx = context.WithValue(ctx, ctxKeyRequestID, reqID)

		// Wrap response writer to capture status for logs.
		rw := &recorder{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				s.cfg.Logger.Printf("PANIC %s %s req=%s: %v", r.Method, r.URL.Path, reqID, rec)
				if !rw.wroteHeader {
					s.writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprint(rec), nil)
				}
			}
			s.cfg.Logger.Printf("%s %s status=%d dur=%dms req=%s",
				r.Method, r.URL.Path, rw.status, time.Since(start).Milliseconds(), reqID)
		}()
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyAgent
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
