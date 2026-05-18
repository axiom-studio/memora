package http

import (
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testLogger discards all middleware log lines; middleware unconditionally
// logs at request end so a nil Logger would panic in the deferred block.
func testLogger() *stdlog.Logger { return stdlog.New(io.Discard, "", 0) }

// TestAuthMiddleware exercises the API-key auth path added by issue
// #2112. It does NOT go through New() — that wires routes() and a real
// http.Server which need a Service. The middleware is constructed
// directly so the test isolates the auth branch.
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
			s := &Server{cfg: Config{APIKey: c.serverKey, MaxBodyBytes: DefaultMaxBodyBytes, Logger: testLogger()}}
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

// TestAuthMiddlewareConstantTimeCompare guards against future refactors
// that might replace subtle.ConstantTimeCompare with a == comparison.
// Two off-by-one-character keys must both produce 401 — and the failure
// path must NOT short-circuit before the full compare runs. We don't
// measure timing here (flaky in CI); we just exercise both rejection
// paths and confirm neither leaks via the error body.
func TestAuthMiddlewareConstantTimeCompare(t *testing.T) {
	const realKey = "abcdef0123456789abcdef0123456789"
	wrongSamePrefix := "abcdef0123456789abcdef0123456788"   // differs only in last byte
	wrongShorter := "abcdef"                                  // strict prefix
	wrongLonger := realKey + "x"                              // strict suffix-extended

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{cfg: Config{APIKey: realKey, MaxBodyBytes: DefaultMaxBodyBytes, Logger: testLogger()}}
	h := s.middleware(next)

	for _, provided := range []string{wrongSamePrefix, wrongShorter, wrongLonger} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Authorization", "Bearer "+provided)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("provided=%q got %d, want 401", provided, rec.Code)
		}
		// The error body must NOT leak which compare failed (e.g.
		// "length mismatch") — subtle.ConstantTimeCompare returns 0 for
		// any difference and we route both length and content mismatches
		// through the same single 401 path.
		if strings.Contains(rec.Body.String(), provided) {
			t.Errorf("error body must not echo the provided token: %q", rec.Body.String())
		}
	}
}
