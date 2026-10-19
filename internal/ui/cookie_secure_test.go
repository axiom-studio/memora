package ui

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #6033: behind a TLS-terminating proxy r.TLS is nil, so the session
// cookie's Secure flag has to come from configuration.

func setupCookieMode(t *testing.T, mode string) *http.ServeMux {
	t.Helper()
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetAuth(AuthConfig{APIKey: "test-key-123", CookieSecure: mode})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func login(t *testing.T, mux *http.ServeMux, overTLS bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("api_key=test-key-123"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if overTLS {
		r.TLS = &tls.ConnectionState{}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login: status %d, want 303", w.Code)
	}
	return w
}

func onlyCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("got %d cookies, want 1: %v", len(cs), cs)
	}
	return cs[0]
}

func TestCookieSecure_SecureCookieAttributes(t *testing.T) {
	for _, c := range []struct {
		mode    string
		overTLS bool
		secure  bool
	}{
		{CookieSecureAlways, false, true}, // TLS-terminating proxy: the case #6033 is about
		{CookieSecureAlways, true, true},
		{CookieSecureAuto, true, true},
		{CookieSecureAuto, false, false},
		{"", false, false}, // unset means auto
		{CookieSecureNever, true, false},
	} {
		w := login(t, setupCookieMode(t, c.mode), c.overTLS)
		ck := onlyCookie(t, w)
		hsts := w.Header().Get("Strict-Transport-Security")
		if ck.Secure != c.secure {
			t.Errorf("mode %q tls=%v: Secure = %v, want %v", c.mode, c.overTLS, ck.Secure, c.secure)
		}
		if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("mode %q: HttpOnly=%v SameSite=%v, want true/Lax", c.mode, ck.HttpOnly, ck.SameSite)
		}
		if c.secure {
			if ck.Name != secureSessionCookieName || ck.Path != "/" || ck.Domain != "" {
				t.Errorf("mode %q: secure cookie = %q path %q domain %q, want __Host- form", c.mode, ck.Name, ck.Path, ck.Domain)
			}
			if hsts != hstsHeaderValue {
				t.Errorf("mode %q tls=%v: Strict-Transport-Security = %q, want %q", c.mode, c.overTLS, hsts, hstsHeaderValue)
			}
		} else {
			if ck.Name != sessionCookieName || ck.Path != "/ui/" {
				t.Errorf("mode %q: plain cookie = %q path %q", c.mode, ck.Name, ck.Path)
			}
			if hsts != "" {
				t.Errorf("mode %q tls=%v: HSTS sent on a non-secure console", c.mode, c.overTLS)
			}
		}
	}
}

// The middleware must read the cookie under the name the login issued, and
// logout must expire that same cookie.
func TestCookieSecure_AlwaysSessionRoundTrip(t *testing.T) {
	mux := setupCookieMode(t, CookieSecureAlways)
	ck := onlyCookie(t, login(t, mux, false))

	get := func(c *http.Cookie) int {
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := get(ck); code != http.StatusOK {
		t.Fatalf("GET /ui/ with the __Host- session: %d, want 200", code)
	}
	if code := get(&http.Cookie{Name: sessionCookieName, Value: ck.Value}); code != http.StatusSeeOther {
		t.Errorf("GET /ui/ with the plain-named cookie in always mode: %d, want 303", code)
	}

	r := httptest.NewRequest(http.MethodPost, "/ui/logout", nil)
	r.AddCookie(ck)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	out := onlyCookie(t, w)
	if out.Name != secureSessionCookieName || out.Path != "/" || out.MaxAge >= 0 || !out.Secure {
		t.Errorf("logout cookie = %+v, want an expired Secure __Host- cookie at /", out)
	}
}

// HSTS has to cover every console route, not just the authenticated ones:
// the login page is where the cookie is issued.
func TestCookieSecure_HSTSOnEveryConsoleRoute(t *testing.T) {
	mux := setupCookieMode(t, CookieSecureAlways)
	for _, p := range []string{"/ui/login", "/ui/logout", "/ui/", "/ui/partials/dashboard-cards", "/ui/static/missing.css"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if got := w.Header().Get("Strict-Transport-Security"); got != hstsHeaderValue {
			t.Errorf("GET %s: Strict-Transport-Security = %q, want %q", p, got, hstsHeaderValue)
		}
	}
}
