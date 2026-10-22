// Package http hosts the Memora REST surface. It wires the service
// layer to net/http endpoints with a small middleware chain
// (request-id, CORS, body-size, structured log, recover, auth, agent identity).
package http

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/memora/internal/certgen"
	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/internal/reqctx"
	"github.com/axiom-studio/memora/internal/service"
	"github.com/axiom-studio/memora/internal/ui"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TLSConfig holds TLS settings for the HTTP server.
type TLSConfig struct {
	Enabled        bool
	CertFile       string
	KeyFile        string
	AutoSelfSigned bool
	Host           string // used for auto-generated cert CN/SAN
}

// Config is the boot-time configuration for the HTTP server.
type Config struct {
	Addr           string
	APIKey         string // single-tenant default key; "" disables auth (local dev)
	Service        *service.Service
	Logger         *slog.Logger
	Timeout        time.Duration
	Mode           string   // single-tenant | multi-tenant
	MaxBodyBytes   int64    // request body size limit (0 = 8 MiB default)
	AllowNoAuth    bool     // explicit opt-in for empty MEMORA_API_KEY
	AllowedOrigins []string // CORS: origins that may call the API; empty = no CORS headers
	// UICookieSecure is the console cookie's Secure mode (ui.CookieSecure*);
	// "" resolves to always on a non-loopback Addr and auto on loopback.
	UICookieSecure string
	TLS            TLSConfig
	UISettings     *ui.SettingsInfo   // optional; nil = settings page shows empty state
	UIFederation   *ui.FederationInfo // optional; nil = federation page shows disabled
	// Federation authenticates the routes peers call (federation.IsPeerRoute),
	// which take peer credentials instead of APIKey. nil = federation off.
	Federation *federation.Registry
	// FederationPeers edits the runtime peer registry behind
	// /v1/federation/peers. nil = those routes answer federation_disabled.
	FederationPeers *federation.Manager
	// FederationInboundQPS caps each peer's calls to the peer routes
	// (0 = federation.DefaultInboundQPS).
	FederationInboundQPS float64
	// FederationRejectWindow is how long unauthenticated peer-route
	// refusals are counted before one summary ledger entry per caller
	// is written (0 = one minute).
	FederationRejectWindow time.Duration

	allowedOriginSet map[string]bool // populated by New from AllowedOrigins
}

// Server is the assembled HTTP server.
type Server struct {
	cfg Config
	mux *http.ServeMux
	srv *http.Server
	// inbound rate-limits each calling peer on the peer routes.
	inbound *federation.RateLimiterPool
	// preauth rate-limits peer-route requests per client address before
	// authentication; rejections aggregates the unauthenticated
	// refusals for the ledger (#6178).
	preauth    *federation.AddrLimiter
	rejections *federation.RejectionAggregator
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
	if cfg.UICookieSecure == "" {
		// A non-loopback bind is reached through something: assume it is
		// HTTPS (directly or via a TLS-terminating proxy), where a cookie
		// without Secure can leak over a plaintext request (#6033).
		cfg.UICookieSecure = ui.CookieSecureAuto
		if !bindsToLoopback(cfg.Addr) {
			cfg.UICookieSecure = ui.CookieSecureAlways
		}
	}
	if len(cfg.AllowedOrigins) > 0 {
		cfg.allowedOriginSet = make(map[string]bool, len(cfg.AllowedOrigins))
		for _, o := range cfg.AllowedOrigins {
			cfg.allowedOriginSet[strings.ToLower(o)] = true
		}
	}
	if cfg.TLS.Enabled {
		if err := initTLS(&cfg); err != nil {
			cfg.Logger.Error("TLS init failed", "err", err)
		}
	}

	mux := http.NewServeMux()
	s := &Server{cfg: cfg, mux: mux, inbound: federation.NewInboundPool(cfg.FederationInboundQPS)}
	inboundQPS := cfg.FederationInboundQPS
	if inboundQPS <= 0 {
		inboundQPS = federation.DefaultInboundQPS
	}
	// Twice one peer's limit, and at least twice the default: room for
	// a peer's own traffic, far below a flood. Peers behind one NAT or
	// proxy share it.
	s.preauth = federation.NewAddrLimiter(2 * max(inboundQPS, federation.DefaultInboundQPS))
	window := cfg.FederationRejectWindow
	if window <= 0 {
		window = time.Minute
	}
	s.rejections = federation.NewRejectionAggregator(window, func(e api.LedgerEntry) {
		if cfg.Service != nil {
			cfg.Service.AppendLedger(context.Background(), e)
		}
	})
	s.routes()

	uiHandler, err := ui.NewHandler()
	if err != nil {
		cfg.Logger.Error("UI handler init failed", "err", err)
	} else {
		uiHandler.SetAuth(ui.AuthConfig{APIKey: cfg.APIKey, CookieSecure: cfg.UICookieSecure})
		cfg.Logger.Info("console session cookie", "secure", cfg.UICookieSecure)
		if cfg.Service != nil {
			ds := &ui.ServiceDataSource{
				Metadata:    cfg.Service.Metadata,
				Vector:      cfg.Service.Vector,
				Ledger:      cfg.Service.Ledger,
				Content:     cfg.Service.Content,
				Graph:       cfg.Service.Graph,
				RecallFunc:  cfg.Service.Recall,
				ImprintFunc: cfg.Service.Imprint,
				UpdateFunc:  cfg.Service.Update,
				PatchFunc:   cfg.Service.Patch,
				AppendFunc:  cfg.Service.Append,
				ForgetFunc:  cfg.Service.Forget,
				UnlinkFunc:  cfg.Service.GraphUnlink,

				LinkFunc:            cfg.Service.GraphLink,
				CreatePinFunc:       cfg.Service.CreatePin,
				DeletePinFunc:       cfg.Service.DeletePin,
				RegisterAgentFunc:   cfg.Service.RegisterAgent,
				DeactivateAgentFunc: cfg.Service.DeactivateAgent,
			}
			uiHandler.SetDataSource(ds)
		}
		if cfg.UISettings != nil {
			uiHandler.SetSettings(cfg.UISettings)
		}
		if cfg.UIFederation != nil {
			uiHandler.SetFederation(cfg.UIFederation)
		}
		if cfg.FederationPeers != nil {
			uiHandler.SetPeers(cfg.FederationPeers)
		}
		uiHandler.Register(mux)
	}
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		// The request line and headers; Go's default is 1 MiB.
		MaxHeaderBytes: 64 << 10,
		ReadTimeout:    cfg.Timeout,
		WriteTimeout:   cfg.Timeout + 5*time.Second,
	}
	if cfg.TLS.Enabled && cfg.Federation != nil {
		// Ask for, but do not chain-verify, client certificates: a peer's
		// certificate is pinned by fingerprint in AuthenticatePeer, and
		// clients without one (API callers, the console) still connect.
		// Always on with federation, since pins can be added at runtime.
		s.srv.TLSConfig = &tls.Config{ClientAuth: tls.RequestClientCert}
	}
	return s
}

// Handler returns the assembled http.Handler (middleware + routes) for
// use with httptest.NewServer in integration tests.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// Mux returns the underlying ServeMux so callers can register
// additional handlers (e.g. the MCP WebSocket endpoint) before the
// server starts listening.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// ListenAndServe blocks until the server fails or Shutdown is called.
// When TLS is configured it serves HTTPS; otherwise plain HTTP.
func (s *Server) ListenAndServe() error {
	if s.cfg.TLS.Enabled {
		return s.srv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	}
	return s.srv.ListenAndServe()
}

// Shutdown drains the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func initTLS(cfg *Config) error {
	if cfg.TLS.CertFile == "" && cfg.TLS.AutoSelfSigned {
		host := cfg.TLS.Host
		if host == "" {
			host = "localhost"
		}
		cfg.Logger.Info("auto-generating self-signed certificate", "host", host)
		res, err := certgen.Generate(certgen.Opts{Host: host})
		if err != nil {
			return fmt.Errorf("auto self-signed cert: %w", err)
		}
		cfg.TLS.CertFile = res.CertPath
		cfg.TLS.KeyFile = res.KeyPath
		cfg.Logger.Info("self-signed certificate ready",
			"cert", res.CertPath, "fingerprint", "SHA256:"+res.Fingerprint)
	}

	if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
		return fmt.Errorf("TLS enabled but cert_file or key_file not set")
	}

	info, err := os.Stat(cfg.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("key_file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		cfg.Logger.Warn("TLS key file has permissive mode",
			"path", cfg.TLS.KeyFile, "mode", fmt.Sprintf("%04o", info.Mode().Perm()))
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("load cert/key: %w", err)
	}
	raw := cert.Certificate[0]
	fp := sha256.Sum256(raw)
	cfg.Logger.Info("TLS certificate loaded",
		"cert", cfg.TLS.CertFile, "fingerprint", "SHA256:"+hex.EncodeToString(fp[:]))

	if !bindsToLoopback(cfg.Addr) && cfg.TLS.AutoSelfSigned {
		cfg.Logger.Warn("running with self-signed cert on a non-loopback bind",
			"addr", cfg.Addr)
	}
	return nil
}

func bindsToLoopback(addr string) bool {
	if addr == "" {
		return true
	}
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// maxRequestIDLen bounds a client-supplied X-Request-ID; anything longer
// is replaced rather than carried into logs and the ledger.
const maxRequestIDLen = 128

// requestID returns the client's X-Request-ID when it is a safe token
// (1–128 chars of [A-Za-z0-9._:-]) and a fresh UUIDv4 otherwise. A bad
// value is replaced, not rejected: request IDs are a correlation aid,
// and the response header tells the client which one was used.
func requestID(clientID string) string {
	if clientID == "" || len(clientID) > maxRequestIDLen {
		return types.NewID()
	}
	for _, c := range clientID {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			return types.NewID()
		}
	}
	return clientID
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := requestID(r.Header.Get("X-Request-ID"))
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

		// Auth: the routes peers call take peer credentials, never the API
		// key, and are refused even in no-auth local-dev mode. Everything
		// else takes the API key (skipped for /ui/ paths — UI has its own
		// session auth; skipped when no key configured — local-dev mode).
		var peer *federation.InboundAuthResult
		routeChecked := false
		if federation.IsPeerRoute(r.URL.Path) {
			if s.cfg.Federation == nil {
				s.writeError(w, http.StatusNotFound, "federation_disabled", "federation is not enabled on this node", nil)
				return
			}
			// Peer routes are reachable without the API key, so a
			// caller is limited per address before anything is checked
			// or written (#6178), and a malformed path is refused before
			// it can reach the audit.
			if s.preauth != nil && !s.preauth.Allow(r.RemoteAddr) {
				s.observeInbound("", federation.ReasonRateLimited)
				w.Header().Set("Retry-After", retryAfter(s.preauth.QPS()))
				s.writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil)
				return
			}
			if !s.checkRoute(w, r) {
				return
			}
			routeChecked = true
			res, err := federation.AuthenticatePeer(r, s.cfg.Federation)
			if err != nil {
				// Audit with the reason; the caller only sees the
				// uniform error. A claimed node ID is recorded only
				// when it is well-formed. A caller that proved nothing
				// is counted into one summary entry per window instead
				// of a row per request.
				claimed := r.Header.Get(federation.HeaderLocalInstanceID)
				if _, perr := types.ParseID("node_id", claimed); perr != nil {
					claimed = ""
				}
				reason := federation.RejectReason(err)
				if federation.Unauthenticated(reason) && s.rejections != nil {
					s.rejections.Record(reason, claimed, r.RemoteAddr, federation.PeerRouteTemplate(r.URL.Path))
				} else {
					s.ledgerRejected(r, claimed, reason)
				}
				s.observeInbound(claimed, reason)
				s.writeErrorFromService(w, err)
				return
			}
			peer = res
		} else if s.cfg.APIKey != "" && r.URL.Path != "/ui" && !strings.HasPrefix(r.URL.Path, "/ui/") {
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

		// Route table: unknown /v1 routes, wrong methods and malformed
		// ID path parameters stop here (routetable.go).
		if !routeChecked && strings.HasPrefix(r.URL.Path, "/v1/") && !s.checkRoute(w, r) {
			return
		}

		// Per-request timeout from Config.Timeout.
		ctx := r.Context()
		ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
		if peer != nil {
			ctx = context.WithValue(ctx, ctxKeyPeer, peer)
		}

		// Multi-tenant: resolve workspace from header when in multi-tenant mode.
		if s.cfg.Mode == "multi-tenant" {
			if ws := r.Header.Get("X-Memora-Workspace"); ws != "" {
				if _, err := types.ParseID("X-Memora-Workspace", ws); err != nil {
					s.writeErrorFromService(w, err)
					return
				}
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
			if _, err := types.ParseID("agent_id", agentID); err != nil {
				s.writeErrorFromService(w, err)
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

			// Record the agent in the workspace after verification:
			// registered on first use, otherwise only last_seen_at moves.
			// It never reactivates a deactivated agent; the service then
			// refuses its writes (#6169).
			if s.cfg.Service != nil && s.cfg.Service.Metadata != nil {
				if wsID := workspaceFromPath(r.URL.Path); wsID != "" {
					_ = s.cfg.Service.TouchAgent(ctx, wsID, agentID, provName)
				}
			}
		}
		ctx = reqctx.WithRequestID(ctx, reqID)

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
	ctxKeyAgent ctxKey = iota
	ctxKeyWorkspace
	ctxKeyPeer
)

// peerFrom returns the authenticated calling peer on a peer route, or nil.
func peerFrom(ctx context.Context) *federation.InboundAuthResult {
	v, _ := ctx.Value(ctxKeyPeer).(*federation.InboundAuthResult)
	return v
}

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

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

func (r *recorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Determine whether a request is a REST memory/edge write that must
// carry Memora-Agent-Id. Only /v1/workspaces/{ws}/... routes qualify: the
// console (/ui/...) authenticates by session and attributes its writes
// to types.ConsoleAgentID itself (#6183), and peer routes are reads made
// by another node. Workspace / collection / agent admin endpoints are
// deployer-controlled and don't require agent attribution.
func isWriteVerb(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	rest, ok := strings.CutPrefix(path, "/v1/workspaces/")
	if !ok {
		return false
	}
	// rest is "{ws}/{resource}[/...]". Agent writes: memories (with
	// :append and @watermark forms), edges, graph and recall/pins (pin
	// create/delete). Plain POST /recall is a query sent as POST, so it
	// is not a write (#5984).
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "memories", "edges", "graph":
		return true
	case "recall":
		return len(parts) == 3 && (parts[2] == "pins" || strings.HasPrefix(parts[2], "pins/"))
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
