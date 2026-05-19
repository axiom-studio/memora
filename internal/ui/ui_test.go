package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandler(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.pages) != 5 {
		t.Errorf("expected 5 pages, got %d", len(h.pages))
	}
}

func TestPages(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	tests := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{"/ui/", 200, "Dashboard"},
		{"/ui/workspaces", 200, "Workspaces"},
		{"/ui/federation", 200, "Federation"},
		{"/ui/audit", 200, "Audit"},
		{"/ui/settings", 200, "Settings"},
		{"/ui/nonexistent", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tt.wantStatus {
				t.Errorf("path %s: want status %d, got %d", tt.path, tt.wantStatus, w.Code)
			}
			if tt.wantBody != "" && !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("path %s: body missing %q", tt.path, tt.wantBody)
			}
		})
	}
}

func TestStaticAssets(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	tests := []struct {
		path        string
		wantStatus  int
		wantContent string
	}{
		{"/ui/static/css/style.css", 200, ":root"},
		{"/ui/static/js/htmx.min.js", 200, "htmx"},
		{"/ui/static/nonexistent.js", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tt.wantStatus {
				t.Errorf("want %d, got %d", tt.wantStatus, w.Code)
			}
			if tt.wantContent != "" && !strings.Contains(w.Body.String(), tt.wantContent) {
				t.Errorf("body missing %q", tt.wantContent)
			}
		})
	}
}

func TestPartials(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	partials := []string{
		"/ui/partials/dashboard-cards",
		"/ui/partials/recent-activity",
		"/ui/partials/workspace-list",
		"/ui/partials/federation-status",
		"/ui/partials/audit-list",
		"/ui/partials/settings-detail",
	}

	for _, path := range partials {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Errorf("want 200, got %d", w.Code)
			}
			ct := w.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("want text/html, got %s", ct)
			}
		})
	}
}

func TestLayoutHTMLStructure(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()

	required := []string{
		"<!DOCTYPE html>",
		`<html lang="en">`,
		"htmx.min.js",
		"style.css",
		`class="sidebar"`,
		`class="main"`,
		`class="active"`,
	}

	for _, s := range required {
		if !strings.Contains(body, s) {
			t.Errorf("layout missing %q", s)
		}
	}
}

func TestNavActiveState(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	tests := []struct {
		path     string
		wantHref string
	}{
		{"/ui/", `/ui/"`},
		{"/ui/workspaces", `/ui/workspaces"`},
		{"/ui/audit", `/ui/audit"`},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			body := w.Body.String()
			activeIdx := strings.Index(body, `class="active"`)
			if activeIdx < 0 {
				t.Fatal("no active nav link found")
			}
		})
	}
}

func setupWithAuth(t *testing.T) (*Handler, *http.ServeMux) {
	t.Helper()
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetAuth(AuthConfig{APIKey: "test-key-123"})
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

func TestAuth_RedirectsWithoutSession(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/ui/login" {
		t.Errorf("expected redirect to /ui/login, got %s", loc)
	}
}

func TestAuth_LoginPageAccessible(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("login page: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "API Key") {
		t.Error("login page missing API Key form")
	}
}

func TestAuth_StaticAssetsNoAuth(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/static/css/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("static asset: expected 200, got %d", w.Code)
	}
}

func TestAuth_LoginInvalidKey(t *testing.T) {
	_, mux := setupWithAuth(t)

	body := strings.NewReader("api_key=wrong-key")
	r := httptest.NewRequest(http.MethodPost, "/ui/login", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid key: expected 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid API key") {
		t.Error("expected error message in response")
	}
}

func TestAuth_LoginValidKey(t *testing.T) {
	_, mux := setupWithAuth(t)

	body := strings.NewReader("api_key=test-key-123")
	r := httptest.NewRequest(http.MethodPost, "/ui/login", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("valid key: expected 303, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if sessionCookie.Path != "/ui/" {
		t.Errorf("expected cookie path /ui/, got %s", sessionCookie.Path)
	}
}

func TestAuth_SessionAccess(t *testing.T) {
	h, mux := setupWithAuth(t)

	token := h.createSession()

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("with valid session: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Dashboard") {
		t.Error("expected dashboard content with valid session")
	}
}

func TestAuth_InvalidSessionToken(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "garbage.token"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("invalid session: expected 303, got %d", w.Code)
	}
}

func TestAuth_Logout(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/logout", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("logout: expected 303, got %d", w.Code)
	}
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout should clear session cookie")
	}
}

func TestAuth_NoAuthBypass(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	// No SetAuth call — APIKey is empty, auth disabled
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("no auth: expected 200, got %d", w.Code)
	}
}

func TestAuth_PartialsRequireSession(t *testing.T) {
	_, mux := setupWithAuth(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("partials without auth: expected 303, got %d", w.Code)
	}
}

func TestFuncMap(t *testing.T) {
	fn := funcMap["truncate"].(func(int, string) string)
	if got := fn(5, "hello world"); got != "hello…" {
		t.Errorf("truncate: got %q", got)
	}
	if got := fn(20, "short"); got != "short" {
		t.Errorf("truncate no-op: got %q", got)
	}

	pl := funcMap["pluralize"].(func(int, string, string) string)
	if got := pl(1, "workspace", "workspaces"); got != "workspace" {
		t.Errorf("pluralize 1: got %q", got)
	}
	if got := pl(5, "workspace", "workspaces"); got != "workspaces" {
		t.Errorf("pluralize 5: got %q", got)
	}
}
