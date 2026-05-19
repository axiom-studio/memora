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
