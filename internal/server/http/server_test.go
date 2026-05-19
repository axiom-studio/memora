package http

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testServer(key string) *Server {
	return &Server{cfg: Config{
		APIKey:       key,
		MaxBodyBytes: DefaultMaxBodyBytes,
		Logger:       testLogger(),
		Timeout:      5 * time.Second,
	}}
}

func TestAuthMiddleware(t *testing.T) {
	const validKey = "expected-secret-key"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	cases := []struct {
		name         string
		serverKey    string
		authHeader   string
		method, path string
		want         int
	}{
		{"valid bearer passes", validKey, "Bearer " + validKey, http.MethodGet, "/healthz", http.StatusOK},
		{"wrong bearer rejected", validKey, "Bearer not-the-key", http.MethodGet, "/healthz", http.StatusUnauthorized},
		{"missing header rejected", validKey, "", http.MethodGet, "/healthz", http.StatusUnauthorized},
		{"basic-auth scheme rejected", validKey, "Basic " + validKey, http.MethodGet, "/healthz", http.StatusUnauthorized},
		{"empty key bypasses auth (loopback dev mode)", "", "", http.MethodGet, "/healthz", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testServer(c.serverKey)
			h := s.middleware(next)
			req := httptest.NewRequest(c.method, c.path, nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestMiddlewareBodySizeLimit(t *testing.T) {
	const cap = 1024
	var readErr error
	var readN int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, err := io.ReadAll(r.Body)
		readErr = err
		readN = len(buf)
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{cfg: Config{APIKey: "", MaxBodyBytes: cap, Logger: testLogger(), Timeout: 5 * time.Second}}
	h := s.middleware(next)

	t.Run("under cap is fine", func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), cap-1)
		req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		readErr, readN = nil, 0
		h.ServeHTTP(httptest.NewRecorder(), req)
		if readErr != nil {
			t.Fatalf("under-cap read errored: %v", readErr)
		}
		if readN != len(body) {
			t.Errorf("read %d bytes, want %d", readN, len(body))
		}
	})

	t.Run("over cap errors", func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), cap+1)
		req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		readErr, readN = nil, 0
		h.ServeHTTP(httptest.NewRecorder(), req)
		if readErr == nil {
			t.Fatalf("over-cap read should have errored; got nil with %d bytes read", readN)
		}
	})
}

func TestAuthMiddlewareConstantTimeCompare(t *testing.T) {
	const realKey = "abcdef0123456789abcdef0123456789"
	wrongSamePrefix := "abcdef0123456789abcdef0123456788"
	wrongShorter := "abcdef"
	wrongLonger := realKey + "x"

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := testServer(realKey)
	h := s.middleware(next)

	for _, provided := range []string{wrongSamePrefix, wrongShorter, wrongLonger} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Authorization", "Bearer "+provided)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("provided=%q got %d, want 401", provided, rec.Code)
		}
		if strings.Contains(rec.Body.String(), provided) {
			t.Errorf("error body must not echo the provided token: %q", rec.Body.String())
		}
	}
}

func TestMiddlewarePanicRecovery(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})
	s := testServer("")
	h := s.middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env api.ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.ErrorCode != "internal_error" {
		t.Errorf("error_code = %q, want internal_error", env.ErrorCode)
	}
}

func TestCORSPreflight(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{cfg: Config{
		Logger:         testLogger(),
		MaxBodyBytes:   DefaultMaxBodyBytes,
		Timeout:        5 * time.Second,
		AllowedOrigins: []string{"https://app.example.com"},
		allowedOriginSet: map[string]bool{
			"https://app.example.com": true,
		},
	}}
	h := s.middleware(next)

	t.Run("preflight returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/v1/workspaces", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("ACAO = %q, want https://app.example.com", got)
		}
	})

	t.Run("disallowed origin gets no CORS headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "https://evil.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("should not set ACAO for disallowed origin, got %q", got)
		}
	})

	t.Run("wildcard allows any origin", func(t *testing.T) {
		s2 := &Server{cfg: Config{
			Logger:           testLogger(),
			MaxBodyBytes:     DefaultMaxBodyBytes,
			Timeout:          5 * time.Second,
			AllowedOrigins:   []string{"*"},
			allowedOriginSet: map[string]bool{"*": true},
		}}
		h2 := s2.middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "https://anything.test")
		rec := httptest.NewRecorder()
		h2.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.test" {
			t.Errorf("ACAO = %q, want https://anything.test", got)
		}
	})
}

func TestCORSPreflightBypassesAuth(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{cfg: Config{
		APIKey:       "secret",
		Logger:       testLogger(),
		MaxBodyBytes: DefaultMaxBodyBytes,
		Timeout:      5 * time.Second,
		allowedOriginSet: map[string]bool{
			"https://app.example.com": true,
		},
	}}
	h := s.middleware(next)

	req := httptest.NewRequest(http.MethodOptions, "/v1/workspaces", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight should return 204 even with APIKey set, got %d", rec.Code)
	}
}

func TestWorkspaceFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/workspaces/ws_abc/memories", "ws_abc"},
		{"/v1/workspaces/ws_abc", "ws_abc"},
		{"/healthz", ""},
		{"/v1/workspaces/", ""},
	}
	for _, c := range cases {
		got := workspaceFromPath(c.path)
		if got != c.want {
			t.Errorf("workspaceFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestMultiTenantWorkspaceHeader(t *testing.T) {
	var gotWS string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, _ := r.Context().Value(ctxKeyWorkspace).(string)
		gotWS = v
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{cfg: Config{
		Logger:       testLogger(),
		MaxBodyBytes: DefaultMaxBodyBytes,
		Timeout:      5 * time.Second,
		Mode:         "multi-tenant",
	}}
	h := s.middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Memora-Workspace", "ws_tenant42")
	gotWS = ""
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotWS != "ws_tenant42" {
		t.Errorf("workspace = %q, want ws_tenant42", gotWS)
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	s := testServer("secret")
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var env api.ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.ErrorCode != "unauthorized" {
		t.Errorf("error_code = %q", env.ErrorCode)
	}
	if env.Message == "" {
		t.Error("message should not be empty")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}
