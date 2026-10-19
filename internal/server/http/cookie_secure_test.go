package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #6033: an unset ui_cookie_secure means Secure on any non-loopback bind,
// where the console is reached through something (often a TLS-terminating
// proxy, which leaves r.TLS nil), and request-scheme-following on loopback.
func TestUICookieSecure_DefaultFollowsBind(t *testing.T) {
	for _, c := range []struct {
		addr, mode string
		secure     bool
	}{
		{":7777", "", true},
		{"0.0.0.0:7777", "", true},
		{"127.0.0.1:7777", "", false},
		{"localhost:7777", "", false},
		{":7777", "auto", false},
		{"127.0.0.1:7777", "always", true},
	} {
		srv := New(Config{
			Addr: c.addr, APIKey: "k", UICookieSecure: c.mode,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		r := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("api_key=k"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.Mux().ServeHTTP(w, r)
		cs := w.Result().Cookies()
		if len(cs) != 1 {
			t.Fatalf("addr %q mode %q: got %d cookies (status %d)", c.addr, c.mode, len(cs), w.Code)
		}
		if cs[0].Secure != c.secure {
			t.Errorf("addr %q mode %q: Secure = %v, want %v", c.addr, c.mode, cs[0].Secure, c.secure)
		}
	}
}
