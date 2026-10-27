package ui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestNewHandler(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.pages) != 6 {
		t.Errorf("expected 6 pages, got %d", len(h.pages))
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

func mustHandler(t *testing.T) *Handler {
	t.Helper()
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type mockDataSource struct {
	stats        DashboardStats
	entries      []api.LedgerEntry
	workspaces   []WorkspaceSummary
	workspace    *WorkspaceDetail
	agents       []AgentSummary
	collections  []CollectionSummary
	memories     []MemorySummary
	memory       *MemorySummary
	cells        []CellSummary
	edges        []EdgeSummary
	recallResp   *api.RecallResponse
	graphData    *GraphData
	watermarks   []types.WatermarkHistoryEntry
	auditEntries []api.LedgerEntry
	auditCursor  string
	pins         []PinSummary
	err          error
}

func (m *mockDataSource) DashboardStats(_ context.Context) (DashboardStats, error) {
	return m.stats, m.err
}
func (m *mockDataSource) RecentLedgerEntries(_ context.Context, limit int) ([]api.LedgerEntry, error) {
	if len(m.entries) > limit {
		return m.entries[:limit], m.err
	}
	return m.entries, m.err
}
func (m *mockDataSource) LedgerEntriesSince(_ context.Context, _ time.Time, _ []string, limit int) ([]api.LedgerEntry, error) {
	return m.RecentLedgerEntries(context.TODO(), limit)
}
func (m *mockDataSource) ListWorkspaces(_ context.Context) ([]WorkspaceSummary, error) {
	return m.workspaces, m.err
}
func (m *mockDataSource) GetWorkspace(_ context.Context, id string) (*WorkspaceDetail, error) {
	if m.workspace != nil {
		return m.workspace, m.err
	}
	return nil, fmt.Errorf("workspace %s not found", id)
}
func (m *mockDataSource) ListAgents(_ context.Context, _ string) ([]AgentSummary, error) {
	return m.agents, m.err
}
func (m *mockDataSource) RegisterAgent(_ context.Context, _ string, _ RegisterAgentInput) error {
	return m.err
}
func (m *mockDataSource) DeactivateAgent(_ context.Context, _, _ string) error {
	return m.err
}
func (m *mockDataSource) ListCollections(_ context.Context, _ string) ([]CollectionSummary, error) {
	return m.collections, m.err
}
func (m *mockDataSource) ListMemories(_ context.Context, _ string, _ int) ([]MemorySummary, error) {
	return m.memories, m.err
}
func (m *mockDataSource) GetMemory(_ context.Context, _, id string) (*MemorySummary, error) {
	if m.memory != nil {
		return m.memory, m.err
	}
	return nil, fmt.Errorf("memory %s not found", id)
}
func (m *mockDataSource) GetCells(_ context.Context, _, _ string) ([]CellSummary, error) {
	return m.cells, m.err
}
func (m *mockDataSource) GetEdges(_ context.Context, _, _ string) ([]EdgeSummary, error) {
	return m.edges, m.err
}
func (m *mockDataSource) Recall(_ context.Context, _ string, _ string, _ string, _ int) (*api.RecallResponse, error) {
	if m.recallResp != nil {
		return m.recallResp, m.err
	}
	return nil, fmt.Errorf("recall not configured")
}
func (m *mockDataSource) RecallFull(_ context.Context, _ string, _ api.RecallRequest) (*api.RecallResponse, error) {
	if m.recallResp != nil {
		return m.recallResp, m.err
	}
	return nil, fmt.Errorf("recall not configured")
}
func (m *mockDataSource) CreateWorkspace(_ context.Context, input CreateWorkspaceInput) (string, error) {
	return "35e81a82-f317-42ba-9a87-83c5eec2d61d", m.err
}
func (m *mockDataSource) UpdateWorkspace(_ context.Context, _ string, _ UpdateWorkspaceInput) error {
	return m.err
}
func (m *mockDataSource) DeleteWorkspace(_ context.Context, _ string) error {
	return m.err
}
func (m *mockDataSource) CreateCollection(_ context.Context, input CreateCollectionInput) (string, error) {
	return "2fdd89f4-8797-4e45-abc8-a799dc24bda6", m.err
}
func (m *mockDataSource) DeleteCollection(_ context.Context, _ string) error {
	return m.err
}
func (m *mockDataSource) ImprintMemory(_ context.Context, _ string, _ api.ImprintRequest) (*api.ImprintResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.ImprintResponse{
		ID:           "a84a5cec-4093-4c63-b0e4-7d3c6057e081",
		Watermark:    "wm_abc",
		CellsCreated: 3,
		RecallReady:  true,
		LedgerID:     "led_xyz",
		LatencyMS:    42,
	}, nil
}
func (m *mockDataSource) AppendMemory(_ context.Context, _, _ string, _ api.AppendRequest) (*api.AppendResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.AppendResponse{
		ID:         "a84a5cec-4093-4c63-b0e4-7d3c6057e081",
		Watermark:  "wm_appended",
		CellsAdded: 2,
		LedgerID:   "led_app",
	}, nil
}
func (m *mockDataSource) ForgetMemory(_ context.Context, _, _ string) (*api.ForgetResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.ForgetResponse{
		ID:            "a84a5cec-4093-4c63-b0e4-7d3c6057e081",
		Watermark:     "wm_forgotten",
		CascadedEdges: 3,
		LedgerID:      "led_forget",
	}, nil
}
func (m *mockDataSource) PatchMemory(_ context.Context, _, _ string, _ api.PatchRequest) (*api.PatchResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.PatchResponse{
		ID:             "a84a5cec-4093-4c63-b0e4-7d3c6057e081",
		Watermark:      "wm_patched",
		PatchesApplied: 2,
		CellsReembed:   1,
		CellsSkipped:   2,
		LedgerID:       "led_patch",
	}, nil
}
func (m *mockDataSource) UpdateMemory(_ context.Context, _, _ string, _ api.UpdateRequest) (*api.UpdateResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.UpdateResponse{
		ID:           "a84a5cec-4093-4c63-b0e4-7d3c6057e081",
		Watermark:    "wm_def",
		CellsReembed: 2,
		CellsSkipped: 1,
		LedgerID:     "led_uvw",
	}, nil
}
func (m *mockDataSource) GraphData(_ context.Context, _ string, _ string, _ int) (*GraphData, error) {
	if m.graphData != nil {
		return m.graphData, m.err
	}
	return &GraphData{Nodes: nil, Edges: nil}, m.err
}
func (m *mockDataSource) GraphNeighbors(_ context.Context, _, _, _ string, _ []string, _ int) (*GraphData, error) {
	if m.graphData != nil {
		return m.graphData, m.err
	}
	return &GraphData{Nodes: nil, Edges: nil}, m.err
}
func (m *mockDataSource) GraphTraverse(_ context.Context, _, _, _ string, _ []string, _ int) (*GraphData, error) {
	if m.graphData != nil {
		return m.graphData, m.err
	}
	return &GraphData{Nodes: nil, Edges: nil}, m.err
}
func (m *mockDataSource) GraphStats(_ context.Context, _ string) (int, map[string]int, error) {
	return 5, map[string]int{"references": 3, "derived_from": 2}, m.err
}
func (m *mockDataSource) LinkEdge(_ context.Context, _, _, _, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "6b9ee74a-f21a-45c6-893e-02b24f8b1d55", nil
}
func (m *mockDataSource) UnlinkEdge(_ context.Context, _, _ string) error {
	return m.err
}
func (m *mockDataSource) GetWatermarkHistory(_ context.Context, _, _ string, _ time.Time) ([]types.WatermarkHistoryEntry, error) {
	return m.watermarks, m.err
}
func (m *mockDataSource) UpsertTag(_ context.Context, _, _, _, _ string) error { return m.err }
func (m *mockDataSource) DeleteTag(_ context.Context, _, _, _ string) error    { return m.err }
func (m *mockDataSource) AuditQuery(_ context.Context, _, _ string, _ []string, _, _ *time.Time, _ string, _ int) ([]api.LedgerEntry, string, error) {
	return m.auditEntries, m.auditCursor, m.err
}
func (m *mockDataSource) ListPins(_ context.Context, _ string) ([]PinSummary, error) {
	return m.pins, m.err
}
func (m *mockDataSource) CreatePin(_ context.Context, _, _, _ string, _ int, _, _ string) (string, error) {
	return "ac594a2e-78d9-4b5c-ae8c-3c3f81f73745", m.err
}
func (m *mockDataSource) DeletePin(_ context.Context, _, _ string) error { return m.err }
func (m *mockDataSource) HealthStatus(_ context.Context) (*HealthInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &HealthInfo{OK: true, Status: "ready", Adapters: []AdapterHealth{
		{Name: "metadata", Status: "ok"},
	}}, nil
}

func TestDashboardCards_WithData(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		stats: DashboardStats{
			WorkspaceCount:  3,
			MemoryCount:     42,
			RecallReadyPct:  95,
			EmbedQueueDepth: 7,
		},
	})
	h.SetFederation(&FederationInfo{FederationID: "test-fed"})
	h.SetPeers(realPeers(t, "node2", "node3"))

	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{"3", "42", "95%", "7", ">2<"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard cards missing %q in body: %s", want, body)
		}
	}
	for _, link := range []string{`href="/ui/workspaces"`, `href="/ui/federation"`} {
		if !strings.Contains(body, link) {
			t.Errorf("dashboard cards missing clickable link %q", link)
		}
	}
}

func TestDashboardCards_NoDataSource(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "—") {
		t.Error("expected fallback dash values")
	}
}

func TestRecentActivity_WithEntries(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		entries: []api.LedgerEntry{
			{Op: "imprint", Target: "735d3a41-398f-4d41-bef8-9773d48fa02f", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5", Timestamp: time.Now()},
			{Op: "recall", Target: "d2e68fff-497d-4c81-93e8-e6a9388c2442", AgentID: "cfbc51d8-74fc-4d7c-bc7d-5cbaa40cf2ba", Timestamp: time.Now()},
		},
	})

	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/recent-activity", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "imprint") || !strings.Contains(body, "recall") {
		t.Errorf("recent activity missing entries: %s", body)
	}
	if !strings.Contains(body, "<table>") {
		t.Error("expected table in recent activity")
	}
}

func TestRecentActivity_Empty(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})

	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/recent-activity", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "No recent activity") {
		t.Error("expected 'No recent activity' message")
	}
}

func TestWorkspaceList(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		workspaces: []WorkspaceSummary{
			{ID: "65727904-2abe-4a88-a7a5-853476162254", Name: "Production", EmbeddingModel: "text-embedding-3-small", AgentCount: 3, MemoryCount: 42, CreatedAt: time.Now()},
			{ID: "e3a6148c-6521-4200-973c-9c72b2f4e255", Name: "Staging", EmbeddingModel: "text-embedding-3-small", AgentCount: 1, MemoryCount: 5, CreatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "65727904-2abe-4a88-a7a5-853476162254") || !strings.Contains(body, "Production") {
		t.Errorf("workspace list missing first workspace: %s", body)
	}
	if !strings.Contains(body, "e3a6148c-6521-4200-973c-9c72b2f4e255") || !strings.Contains(body, "Staging") {
		t.Errorf("workspace list missing second workspace: %s", body)
	}
	if !strings.Contains(body, "<table>") {
		t.Error("expected table in workspace list")
	}
}

func TestWorkspaceList_Empty(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "No Workspaces") {
		t.Error("expected empty state message")
	}
}

func TestWorkspaceDetail_Overview(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		workspace: &WorkspaceDetail{
			WorkspaceSummary: WorkspaceSummary{
				ID: "65727904-2abe-4a88-a7a5-853476162254", Name: "Production", EmbeddingModel: "text-embedding-3-small",
				AgentCount: 3, MemoryCount: 42, CreatedAt: time.Now(),
			},
			Region: "us-east-1", ChunkerID: "paragraph", AutoLinkEnabled: true,
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=overview", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{"65727904-2abe-4a88-a7a5-853476162254", "Production", "us-east-1", "paragraph", "Enabled"} {
		if !strings.Contains(body, want) {
			t.Errorf("workspace detail missing %q", want)
		}
	}
}

func TestWorkspaceDetail_Agents(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		agents: []AgentSummary{
			{AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5", DisplayName: "Bot A", IdentityProvider: "opaque", AgentType: "chat"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=agents", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "004681e8-d399-4612-8c7b-f6b11a0428b5") || !strings.Contains(body, "Bot A") {
		t.Errorf("agent tab missing agent data: %s", body)
	}
}

func TestWorkspaceDetail_Collections(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{
			{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "Knowledge Base", CreatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=collections", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "ff6069b2-4f27-4d65-a135-f29b5e8fec4e") || !strings.Contains(body, "Knowledge Base") {
		t.Errorf("collections tab missing data: %s", body)
	}
}

func TestMemoryList(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		memories: []MemorySummary{
			{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5", RecallReady: true, Content: "Hello world", UpdatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-list?ws=759dbbe1-9fcc-4069-90ec-f799ac861b95", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "735d3a41-398f-4d41-bef8-9773d48fa02f") {
		t.Error("memory list missing 735d3a41-398f-4d41-bef8-9773d48fa02f")
	}
	if !strings.Contains(body, "Hello world") {
		t.Error("memory list missing content preview")
	}
}

func TestMemoryDetail_Content(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{
			ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "Full content here", ContentMD5: "abc123",
			Watermark: "wmk_1", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5", RecallReady: true,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&tab=content", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"735d3a41-398f-4d41-bef8-9773d48fa02f", "Full content here", "abc123", "wmk_1"} {
		if !strings.Contains(body, want) {
			t.Errorf("memory detail missing %q", want)
		}
	}
}

func TestMemoryDetail_Cells(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		cells: []CellSummary{
			{CellID: "cell_1", Text: "chunk one", TextMD5: "md5_1", Sequence: 0},
			{CellID: "cell_2", Text: "chunk two", TextMD5: "md5_2", Sequence: 1},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&tab=cells", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "cell_1") || !strings.Contains(body, "chunk one") {
		t.Error("cells tab missing cell data")
	}
}

func TestMemoryDetail_Edges(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		edges: []EdgeSummary{
			{EdgeID: "a2145e16-f3a2-48c7-b141-a0f8117f2844", SourceMemoryID: "735d3a41-398f-4d41-bef8-9773d48fa02f", TargetMemoryID: "9693d7dc-fff0-4ffd-9201-bc2da80786df", EdgeType: "related", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&tab=edges", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "a2145e16-f3a2-48c7-b141-a0f8117f2844") || !strings.Contains(body, "related") {
		t.Error("edges tab missing edge data")
	}
}

func TestMemoryDetail_Watermarks(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		watermarks: []types.WatermarkHistoryEntry{
			{
				TargetID:        "735d3a41-398f-4d41-bef8-9773d48fa02f",
				Watermark:       "wmk_01EXAMPLE",
				Op:              "imprint",
				AgentID:         "434becf0-b125-41d5-8379-5a945dc8fb5e",
				CreatedAt:       time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
				ContentMD5After: "abc123",
			},
			{
				TargetID:         "735d3a41-398f-4d41-bef8-9773d48fa02f",
				Watermark:        "wmk_02EXAMPLE",
				Op:               "update",
				AgentID:          "434becf0-b125-41d5-8379-5a945dc8fb5e",
				CreatedAt:        time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC),
				ContentMD5Before: "abc123",
				ContentMD5After:  "def456",
			},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&tab=watermarks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Watermark History") {
		t.Error("missing Watermark History heading")
	}
	if !strings.Contains(body, "wmk_01EXAMPLE") {
		t.Error("missing first watermark entry")
	}
	if !strings.Contains(body, "imprint") {
		t.Error("missing imprint op")
	}
	if !strings.Contains(body, "update") {
		t.Error("missing update op")
	}
	if !strings.Contains(body, "434becf0-b125-41d5-8") { // the table shows 20 chars
		t.Error("missing agent ID")
	}
	if !strings.Contains(body, "def456") {
		t.Error("missing content MD5 after")
	}
}

func TestMemoryDetail_WatermarksEmpty(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&tab=watermarks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "No watermark history") {
		t.Error("missing empty state message")
	}
}

func TestRecallResults(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{
			Results: []api.RecallHit{
				{MemoryID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Score: 0.95, Text: "match text", Via: "seed"},
			},
			TotalCandidatesScanned: 100,
			LatencyMS:              15,
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/recall-results?ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&q=test+query&mode=hybrid", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "735d3a41-398f-4d41-bef8-9773d48fa02f") || !strings.Contains(body, "0.95") {
		t.Errorf("recall results missing data: %s", body)
	}
	if !strings.Contains(body, "100 candidates") {
		t.Error("recall results missing stats")
	}
}

func TestMemoriesPage(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254/memories", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "recall-query-bar") {
		t.Error("memories page missing recall query bar loader")
	}
}

func TestMemoryDetailPage(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254/memories/c1c4b15b-b641-42b1-a4b2-e5302797669d", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "c1c4b15b-b641-42b1-a4b2-e5302797669d") {
		t.Error("memory detail page missing memory ID")
	}
	if !strings.Contains(body, "Content") && !strings.Contains(body, "Cells") {
		t.Error("memory detail page missing tabs")
	}
	if !strings.Contains(body, "Watermarks") {
		t.Error("memory detail page missing Watermarks tab")
	}
}

func TestWorkspaceDetailPage(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "65727904-2abe-4a88-a7a5-853476162254") {
		t.Error("workspace detail page should contain workspace ID")
	}
	if !strings.Contains(body, "Overview") {
		t.Error("workspace detail page should have Overview tab")
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

func TestAuditList(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		auditEntries: []api.LedgerEntry{
			{LedgerID: "led-1", Op: "imprint", Target: "fc340556-e833-41be-bb3d-428d3ebe2c8c", AgentID: "26096ca9-acde-4c31-bd4c-479ef452b5aa", WorkspaceID: "76944604-4ae9-4490-be45-edd729af5598", LatencyMS: 42, Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)},
			{LedgerID: "led-2", Op: "forget", Target: "5262903f-c84c-4778-86cf-40dcc88c316d", AgentID: "19577b55-5eee-4e2c-b3c0-54407686125a", WorkspaceID: "76944604-4ae9-4490-be45-edd729af5598", LatencyMS: 7, Timestamp: time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/audit-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(body, "imprint") {
		t.Error("missing op imprint")
	}
	if !strings.Contains(body, "led-1") {
		t.Error("missing ledger ID")
	}
	if !strings.Contains(body, "42ms") {
		t.Error("missing latency")
	}
}

func TestAuditList_Empty(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/audit-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "No Entries") {
		t.Error("expected empty state")
	}
}

func TestAuditList_Pagination(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		auditEntries: []api.LedgerEntry{
			{LedgerID: "led-1", Op: "imprint", Timestamp: time.Now()},
		},
		auditCursor: "next-page-cursor",
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/audit-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Load More") {
		t.Error("expected Load More button for pagination")
	}
}

func TestAuditExport_CSV(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		auditEntries: []api.LedgerEntry{
			{LedgerID: "led-1", Op: "imprint", Target: "fc340556-e833-41be-bb3d-428d3ebe2c8c", AgentID: "26096ca9-acde-4c31-bd4c-479ef452b5aa", WorkspaceID: "76944604-4ae9-4490-be45-edd729af5598", LatencyMS: 42, Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/audit/export?format=csv", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("want text/csv, got %s", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "led-1") {
		t.Error("CSV missing ledger ID")
	}
	if !strings.Contains(body, "imprint") {
		t.Error("CSV missing op")
	}
}

func TestAuditExport_NDJSON(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		auditEntries: []api.LedgerEntry{
			{LedgerID: "led-1", Op: "imprint", Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/audit/export?format=ndjson", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("want application/x-ndjson, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), `"led-1"`) {
		t.Error("NDJSON missing ledger ID")
	}
}

func TestAuditPage(t *testing.T) {
	h, _ := NewHandler()
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/audit", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Audit Log") {
		t.Error("missing page title")
	}
	if !strings.Contains(body, "Filters") {
		t.Error("missing filter bar")
	}
	if !strings.Contains(body, "Export NDJSON") {
		t.Error("missing export buttons")
	}
}

func TestSettingsDetail(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		ServerAddr:         ":7777",
		ServerMode:         "single-tenant",
		MCPEnabled:         true,
		TLSEnabled:         true,
		TLSCertFile:        "/etc/certs/server.crt",
		DataDir:            "/var/lib/memora",
		MetadataDriver:     "sqlite",
		VectorDriver:       "sqlitevec",
		LedgerDriver:       "sqlite",
		GraphDriver:        "sqlite",
		ContentDriver:      "file",
		EmbeddingModel:     "nomic-embed-text",
		FederationEnabled:  true,
		FederationID:       "fed-abc",
		PeerCount:          2,
		TelemetryLogLevel:  "info",
		TelemetryLogFormat: "json",
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"Server", "TLS", "Storage", "Embedding", "Federation", "Telemetry",
		":7777", "single-tenant", "sqlite", "nomic-embed-text", "fed-abc", "info"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in settings", want)
		}
	}
}

func TestSettingsDetail_Nil(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "not available") {
		t.Error("expected empty state when settings is nil")
	}
}

func TestFederationStatus(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetFederation(&FederationInfo{FederationID: "fed-test-123"})
	pm := realPeers(t, "us-east", "eu-west")
	h.SetPeers(pm)
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/federation-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	peers, _ := pm.List(context.Background())
	for _, want := range []string{"fed-test-123", "us-east", "eu-west", "api_key", "us-east.example.com", peers[0].NodeID, "inbound, outbound"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in federation status", want)
		}
	}
	if strings.Contains(body, "key-us-east") || strings.Contains(body, "key-eu-west") {
		t.Error("federation status shows a peer key")
	}
}

func TestFederationStatus_Disabled(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/federation-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Federation Disabled") {
		t.Error("expected disabled state when federation is nil")
	}
}

func TestGraphPage(t *testing.T) {
	h, _ := NewHandler()
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/76944604-4ae9-4490-be45-edd729af5598/graph", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "graph-view") {
		t.Error("graph page should load graph-view partial")
	}
}

func TestSearch(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		workspaces: []WorkspaceSummary{
			{ID: "18b6e740-0f73-49d6-97af-2829c444660a", Name: "Alpha Workspace"},
			{ID: "bc5a2241-7082-4bff-af71-c758adec3338", Name: "Beta Workspace"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/search?q=alpha", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(body, "18b6e740-0f73-49d6-97af-2829c444660a") {
		t.Error("missing workspace result")
	}
	if strings.Contains(body, "bc5a2241-7082-4bff-af71-c758adec3338") {
		t.Error("should not contain non-matching workspace")
	}
}

func TestSearch_Empty(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/search?q=", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Type to search") {
		t.Error("expected placeholder text for empty query")
	}
}

func TestSearch_NoResults(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{
		workspaces: []WorkspaceSummary{{ID: "76944604-4ae9-4490-be45-edd729af5598", Name: "Test"}},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/search?q=zzzznotfound", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "No results") {
		t.Error("expected no results message")
	}
}

func TestLayoutHasSearchPalette(t *testing.T) {
	h, _ := NewHandler()
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "search-palette") {
		t.Error("layout missing search palette dialog")
	}
	if !strings.Contains(body, "Ctrl+K") || !strings.Contains(body, "metaKey") {
		t.Error("layout missing keyboard shortcut binding")
	}
}

func TestActivitySSE_Headers(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/ui/events/activity", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want text/event-stream, got %s", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("want no-cache, got %s", cc)
	}
}

func TestLayoutHasActivityRail(t *testing.T) {
	h, _ := NewHandler()
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "activity-rail") {
		t.Error("layout missing activity rail")
	}
	if !strings.Contains(body, "EventSource") {
		t.Error("layout missing SSE EventSource JS")
	}
}

func TestWorkspaceCreateForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-create-form", nil)
	h.partialWorkspaceCreateForm(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Create Workspace") {
		t.Error("missing Create Workspace heading")
	}
	if !strings.Contains(body, `hx-post="/ui/api/workspaces/create"`) {
		t.Error("missing create form action")
	}
}

func TestWorkspaceCreate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("name=test-ws&region=us-east-1")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/create", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceCreate(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "/ui/workspaces/35e81a82-f317-42ba-9a87-83c5eec2d61d") {
		t.Errorf("expected redirect to new workspace, got %q", loc)
	}
}

func TestWorkspaceCreate_MissingName(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("region=us-east-1")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/create", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceCreate(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Name is required") {
		t.Error("expected name-required error")
	}
}

func TestWorkspaceUpdate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("id=2b8b4d04-38d3-4ef8-b32f-63209083cb31&name=updated-ws")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/update", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceUpdate(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "/ui/workspaces/2b8b4d04-38d3-4ef8-b32f-63209083cb31") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestWorkspaceDelete(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("id=2b8b4d04-38d3-4ef8-b32f-63209083cb31&confirm=2b8b4d04-38d3-4ef8-b32f-63209083cb31")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/delete", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceDelete(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); loc != "/ui/workspaces" {
		t.Errorf("expected redirect to workspace list, got %q", loc)
	}
}

func TestWorkspaceDelete_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("id=2b8b4d04-38d3-4ef8-b32f-63209083cb31&confirm=wrong")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/delete", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceDelete(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Type the workspace ID to confirm") {
		t.Error("expected confirmation error")
	}
}

func TestWorkspaceEditForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		workspace: &WorkspaceDetail{
			WorkspaceSummary: WorkspaceSummary{ID: "2b8b4d04-38d3-4ef8-b32f-63209083cb31", Name: "test-ws", EmbeddingModel: "hash:default"},
			Region:           "us-east-1",
			ChunkerID:        "default",
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-edit-form?id=2b8b4d04-38d3-4ef8-b32f-63209083cb31", nil)
	h.partialWorkspaceEditForm(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Edit Workspace") {
		t.Error("missing Edit Workspace heading")
	}
	if !strings.Contains(body, "test-ws") {
		t.Error("missing workspace name value")
	}
}

func TestWorkspaceDeleteForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		workspace: &WorkspaceDetail{
			WorkspaceSummary: WorkspaceSummary{ID: "2b8b4d04-38d3-4ef8-b32f-63209083cb31", Name: "test-ws", MemoryCount: 5, AgentCount: 2},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-delete-form?id=2b8b4d04-38d3-4ef8-b32f-63209083cb31", nil)
	h.partialWorkspaceDeleteForm(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Delete Workspace") {
		t.Error("missing Delete Workspace heading")
	}
	if !strings.Contains(body, "5 memories") {
		t.Error("missing memory count")
	}
}

func TestWorkspaceListHasCreateButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		workspaces: []WorkspaceSummary{{ID: "ee92e879-1c13-4a7c-b122-14b795cd0d74", Name: "test"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-list", nil)
	h.partialWorkspaceList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Workspace") {
		t.Error("workspace list missing Create Workspace button")
	}
}

func TestCollectionCreateForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/collection-create-form?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialCollectionCreateForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Collection") {
		t.Error("missing Create Collection heading")
	}
	if !strings.Contains(body, `value="65727904-2abe-4a88-a7a5-853476162254"`) {
		t.Error("missing workspace_id hidden field")
	}
}

func TestCollectionCreate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/create", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&name=my-coll"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionCreate(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "65727904-2abe-4a88-a7a5-853476162254") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestCollectionCreate_MissingName(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/create", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&name="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionCreate(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Name is required") {
		t.Error("missing validation error for empty name")
	}
}

func TestCollectionDelete(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/delete", strings.NewReader("id=3ee9b357-50d1-4601-8bd6-eddecc43d94f&workspace_id=65727904-2abe-4a88-a7a5-853476162254&confirm=3ee9b357-50d1-4601-8bd6-eddecc43d94f"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionDelete(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "65727904-2abe-4a88-a7a5-853476162254") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestCollectionDelete_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/delete", strings.NewReader("id=3ee9b357-50d1-4601-8bd6-eddecc43d94f&workspace_id=65727904-2abe-4a88-a7a5-853476162254&confirm=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionDelete(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Type the collection ID to confirm") {
		t.Error("missing confirm error")
	}
}

func TestCollectionDeleteForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{
			{ID: "3ee9b357-50d1-4601-8bd6-eddecc43d94f", Name: "test-coll", MemoryCount: 5},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/collection-delete-form?id=3ee9b357-50d1-4601-8bd6-eddecc43d94f&ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialCollectionDeleteForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Delete Collection") {
		t.Error("missing Delete Collection heading")
	}
	if !strings.Contains(body, "5 memories") {
		t.Error("missing memory count")
	}
}

func TestCollectionsTabHasCreateButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "test"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=collections", nil)
	h.partialWorkspaceDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Collection") {
		t.Error("collections tab missing Create Collection button")
	}
}

func TestMemoryImprintForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "docs"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-imprint-form?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialMemoryImprintForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Imprint Memory") {
		t.Error("missing Imprint Memory heading")
	}
	if !strings.Contains(body, `value="65727904-2abe-4a88-a7a5-853476162254"`) {
		t.Error("missing workspace_id hidden field")
	}
	if !strings.Contains(body, "docs") {
		t.Error("missing collection option")
	}
}

func TestMemoryImprint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&content=hello+world&chunker_id=markdown"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryImprint(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "a84a5cec-4093-4c63-b0e4-7d3c6057e081") {
		t.Error("missing memory ID in response")
	}
	if !strings.Contains(body, "Memory Created") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "3") {
		t.Error("missing cells created count")
	}
}

func TestMemoryImprint_MissingContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&content="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryImprint(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Content is required") {
		t.Error("missing validation error for empty content")
	}
}

func TestMemoryImprint_WithTags(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&content=test&tag_key=env&tag_value=prod&tag_key=team&tag_value=backend"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryImprint(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "a84a5cec-4093-4c63-b0e4-7d3c6057e081") {
		t.Error("missing memory ID in response")
	}
}

func TestMemoryListHasNewMemoryButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-list?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialMemoryList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "New Memory") {
		t.Error("memory list missing New Memory button")
	}
}

func TestMemoryEditForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "hello world", Watermark: "wm_123", Tags: map[string]string{"env": "prod"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-edit-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=735d3a41-398f-4d41-bef8-9773d48fa02f", nil)
	h.partialMemoryEditForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Edit Memory") {
		t.Error("missing Edit Memory heading")
	}
	if !strings.Contains(body, "hello world") {
		t.Error("missing pre-filled content")
	}
	if !strings.Contains(body, "wm_123") {
		t.Error("missing expected_watermark hidden field")
	}
	if !strings.Contains(body, `value="env"`) {
		t.Error("missing pre-filled tag key")
	}
}

func TestMemoryUpdate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&content=updated+content&expected_watermark=wm_123"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpdate(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Memory Updated") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "wm_def") {
		t.Error("missing new watermark")
	}
}

func TestMemoryUpdate_CASConflict(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{err: fmt.Errorf("memora: cas conflict (head watermark moved)")})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&content=updated&expected_watermark=wm_old"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpdate(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "modified since you opened it") {
		t.Error("missing CAS conflict message")
	}
	if !strings.Contains(body, "Reload") {
		t.Error("missing reload link")
	}
}

func TestMemoryUpdate_MissingContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&content="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpdate(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Content is required") {
		t.Error("missing validation error")
	}
}

func TestMemoryDetailHasEditButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=65727904-2abe-4a88-a7a5-853476162254&tab=content", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Edit") {
		t.Error("memory detail missing Edit button")
	}
}

// Issue #5994: memory content is embedded in an inline <script>; it must not
// be able to close the element and inject markup.
func TestMemoryPatchForm_ContentCannotBreakOutOfScript(t *testing.T) {
	payload := "a</script><img src=x onerror=alert(1)>\u2028b&c"
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: payload, Watermark: "wm_123"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-patch-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=735d3a41-398f-4d41-bef8-9773d48fa02f", nil)
	h.partialMemoryPatchForm(w, r)
	body := w.Body.String()

	if strings.Contains(body, "</script><img") {
		t.Fatal("memory content closed the inline <script> element (stored XSS)")
	}
	start := strings.Index(body, "var patchContent = ")
	if start < 0 {
		t.Fatal("patchContent assignment missing")
	}
	lit := body[start+len("var patchContent = "):]
	lit = lit[:strings.Index(lit, ";\n")]
	var decoded string
	if err := json.Unmarshal([]byte(lit), &decoded); err != nil {
		t.Fatalf("patchContent literal %q is not a valid JS/JSON string: %v", lit, err)
	}
	if decoded != payload {
		t.Fatalf("patchContent decodes to %q, want the original content %q", decoded, payload)
	}
}

func TestMemoryPatchForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "hello world", Watermark: "wm_123"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-patch-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=735d3a41-398f-4d41-bef8-9773d48fa02f", nil)
	h.partialMemoryPatchForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Patch Memory") {
		t.Error("missing Patch Memory heading")
	}
	if !strings.Contains(body, "hello world") {
		t.Error("missing content preview")
	}
	if !strings.Contains(body, "wm_123") {
		t.Error("missing watermark")
	}
	if !strings.Contains(body, "checkAnchor") {
		t.Error("missing live anchor checking JS")
	}
}

func TestMemoryPatch(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&old_string=hello&new_string=goodbye&replace_all=false&expected_watermark=wm_123"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryPatch(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Patch Applied") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "wm_patched") {
		t.Error("missing new watermark")
	}
	if !strings.Contains(body, "2") {
		t.Error("missing patches applied count")
	}
}

func TestMemoryPatch_NoOps(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&old_string=&new_string=test"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryPatch(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "At least one patch operation is required") {
		t.Error("missing validation error")
	}
}

func TestMemoryPatch_CASConflict(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{err: fmt.Errorf("memora: cas conflict (head watermark moved)")})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&old_string=hello&new_string=bye&replace_all=false&expected_watermark=wm_old"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryPatch(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "modified since you opened it") {
		t.Error("missing CAS conflict message")
	}
}

func TestMemoryDetailHasPatchButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=65727904-2abe-4a88-a7a5-853476162254&tab=content", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Patch") {
		t.Error("memory detail missing Patch button")
	}
}

func TestMemoryAppendForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "hello", Watermark: "wm_123"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-append-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=735d3a41-398f-4d41-bef8-9773d48fa02f", nil)
	h.partialMemoryAppendForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Append to Memory") {
		t.Error("missing Append to Memory heading")
	}
	if !strings.Contains(body, "wm_123") {
		t.Error("missing watermark")
	}
}

func TestMemoryAppend(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/append", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&content=appended+text&expected_watermark=wm_123"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryAppend(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Content Appended") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "wm_appended") {
		t.Error("missing new watermark")
	}
}

func TestMemoryAppend_MissingContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/append", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&content="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryAppend(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Content is required") {
		t.Error("missing validation error")
	}
}

func TestMemoryForgetForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "test"},
		cells:  []CellSummary{{CellID: "cell_1"}, {CellID: "cell_2"}},
		edges:  []EdgeSummary{{EdgeID: "a2145e16-f3a2-48c7-b141-a0f8117f2844"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-forget-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=735d3a41-398f-4d41-bef8-9773d48fa02f", nil)
	h.partialMemoryForgetForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Forget Memory") {
		t.Error("missing Forget Memory heading")
	}
	if !strings.Contains(body, "2 cells") {
		t.Error("missing cell count in cascade preview")
	}
	if !strings.Contains(body, "1 edges") {
		t.Error("missing edge count in cascade preview")
	}
}

func TestMemoryForget(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/forget", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&confirm=735d3a41-398f-4d41-bef8-9773d48fa02f"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryForget(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Memory Forgotten") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "3") {
		t.Error("missing cascaded edges count")
	}
}

func TestMemoryForget_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/forget", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&memory_id=735d3a41-398f-4d41-bef8-9773d48fa02f&confirm=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryForget(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Type the memory ID to confirm") {
		t.Error("missing confirm error")
	}
}

func TestMemoryDetailHasAppendAndForgetButtons(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "735d3a41-398f-4d41-bef8-9773d48fa02f", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=735d3a41-398f-4d41-bef8-9773d48fa02f&ws=65727904-2abe-4a88-a7a5-853476162254&tab=content", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Append") {
		t.Error("memory detail missing Append button")
	}
	if !strings.Contains(body, "Forget") {
		t.Error("memory detail missing Forget button")
	}
}

func TestUploadForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "docs"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/upload-form?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialUploadForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Upload Document") {
		t.Error("missing Upload Document heading")
	}
	if !strings.Contains(body, "drop-zone") {
		t.Error("missing drop zone")
	}
	if !strings.Contains(body, "docs") {
		t.Error("missing collection option")
	}
	if !strings.Contains(body, "handleBatchFiles") {
		t.Error("missing batch file handler JS")
	}
	if !strings.Contains(body, "startBatchUpload") {
		t.Error("missing batch upload JS")
	}
	if !strings.Contains(body, "batch-progress") {
		t.Error("missing batch progress bar")
	}
	if !strings.Contains(body, `multiple`) {
		t.Error("missing multiple file input attribute")
	}
	if !strings.Contains(body, "CONCURRENCY") {
		t.Error("missing concurrency constant")
	}
}

func TestMemoryUpload_ViaContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/upload", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&content=uploaded+text&chunker_id=markdown"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpload(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Document Uploaded") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "a84a5cec-4093-4c63-b0e4-7d3c6057e081") {
		t.Error("missing memory ID")
	}
}

func TestMemoryUpload_EmptyContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/upload", strings.NewReader("workspace_id=65727904-2abe-4a88-a7a5-853476162254&content="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpload(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "No file content") {
		t.Error("missing validation error")
	}
}

func TestMemoryListHasUploadButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-list?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialMemoryList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Upload Document") {
		t.Error("memory list missing Upload Document button")
	}
}

func TestRecallQueryBar(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{
			{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "Notes"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-query-bar?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialRecallQueryBar(w, r)
	body := w.Body.String()
	for _, want := range []string{"recall-q", "recall-mode", "recall-k", "recall-filters", "graph_depth", "graph_direction", "include_cells", "Notes", "recall-full"} {
		if !strings.Contains(body, want) {
			t.Errorf("query bar missing %q", want)
		}
	}
}

func TestRecallQueryBar_NoWS(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-query-bar", nil)
	h.partialRecallQueryBar(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "No workspace") {
		t.Error("expected empty state for missing ws")
	}
}

func TestRecallFull_WithResults(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{
			Results: []api.RecallHit{
				{MemoryID: "22132404-d6ef-407b-9604-29c0ba7289c5", Score: 0.95, Text: "hello world", Via: "seed"},
				{MemoryID: "ba4e82bd-10ab-4902-b12b-be6f442152da", Score: 0.8, Text: "graph result", Via: "graph",
					GraphProvenance: &api.GraphProvenance{
						FromMemoryID: "22132404-d6ef-407b-9604-29c0ba7289c5", EdgeID: "e1", EdgeType: "related", Layer: 1,
					}},
			},
			TotalCandidatesScanned: 50,
			LatencyMS:              12,
			GraphNodesExpanded:     3,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=65727904-2abe-4a88-a7a5-853476162254&q=hello&mode=hybrid&k=10&graph_depth=2&graph_direction=out", nil)
	h.partialRecallFull(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"22132404-d6ef-407b-9604-29c0ba7289c5", "ba4e82bd-10ab-4902-b12b-be6f442152da", "0.950", "graph nodes expanded", "Graph Path", "related", "L1"} {
		if !strings.Contains(body, want) {
			t.Errorf("recall full results missing %q", want)
		}
	}
}

func TestRecallFull_WithFilters(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{
			Results:                []api.RecallHit{{MemoryID: "7f3bab74-547d-4adc-8aec-ea8993ff5b26", Score: 0.7, Text: "filtered", Via: "seed"}},
			TotalCandidatesScanned: 10,
			LatencyMS:              5,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=65727904-2abe-4a88-a7a5-853476162254&q=test&collection_id=ff6069b2-4f27-4d65-a135-f29b5e8fec4e&agent_id=b88830ed-9673-4e88-a9ad-8a7f6d73c620&ts_after=2025-01-01&ts_before=2025-12-31", nil)
	h.partialRecallFull(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "7f3bab74-547d-4adc-8aec-ea8993ff5b26") {
		t.Error("expected filtered result")
	}
}

func TestRecallFull_NoQuery(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialRecallFull(w, r)
	if !strings.Contains(w.Body.String(), "Enter a query") {
		t.Error("expected prompt for empty query")
	}
}

func TestRecallFull_NoResults(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{Results: nil, TotalCandidatesScanned: 5, LatencyMS: 3},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=65727904-2abe-4a88-a7a5-853476162254&q=nothing", nil)
	h.partialRecallFull(w, r)
	if !strings.Contains(w.Body.String(), "No Results") {
		t.Error("expected no results message")
	}
}

func TestRecallFull_EmbeddingPending(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{
			Results:          []api.RecallHit{{MemoryID: "m1", Score: 0.5, Text: "pending", Via: "seed"}},
			LatencyMS:        2,
			EmbeddingPending: true,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=65727904-2abe-4a88-a7a5-853476162254&q=test", nil)
	h.partialRecallFull(w, r)
	if !strings.Contains(w.Body.String(), "still being embedded") {
		t.Error("expected embedding pending notice")
	}
}

func TestAgentRegisterForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/agent-register-form?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialAgentRegisterForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"agent_id", "leave empty to generate one", "identity_provider", "agent_type", "model", "Register"} {
		if !strings.Contains(body, want) {
			t.Errorf("register form missing %q", want)
		}
	}
	for _, prov := range []string{"opaque", "anthropic_session", "a2a", "did", "oauth_agent", "oidc_agent"} {
		if !strings.Contains(body, prov) {
			t.Errorf("register form missing provider %q", prov)
		}
	}
}

func TestAgentRegisterForm_NoWS(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/agent-register-form", nil)
	h.partialAgentRegisterForm(w, r)
	if !strings.Contains(w.Body.String(), "Missing workspace") {
		t.Error("expected error for missing ws")
	}
}

func TestAgentRegister(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&agent_id=8f14e45f-ceea-467a-9575-12345678abcd&display_name=Test+Bot&identity_provider=opaque&agent_type=assistant&model=claude"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentRegister(w, r)
	if w.Header().Get("HX-Redirect") == "" {
		t.Error("expected HX-Redirect after register")
	}
	if !strings.Contains(w.Header().Get("HX-Redirect"), "tab=agents") {
		t.Error("redirect should go to agents tab")
	}
}

func TestAgentRegister_NonUUIDRejected(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&agent_id=agent_my_bot&identity_provider=opaque"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentRegister(w, r)
	if !strings.Contains(w.Body.String(), "Agent ID must be a UUID") {
		t.Errorf("expected UUID validation error, got %q", w.Body.String())
	}
}

func TestAgentRegister_EmptyIDGeneratesOne(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&agent_id=&identity_provider=opaque"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentRegister(w, r)
	if w.Header().Get("HX-Redirect") == "" {
		t.Errorf("empty agent_id should register with a generated UUID, got %q", w.Body.String())
	}
}

func TestAgentDeactivateForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/agent-deactivate-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=c7892920-fc50-4acb-94df-29de66f1b542", nil)
	h.partialAgentDeactivateForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "c7892920-fc50-4acb-94df-29de66f1b542") {
		t.Error("deactivate form missing agent ID")
	}
	if !strings.Contains(body, "Deactivate") {
		t.Error("deactivate form missing button")
	}
}

func TestAgentDeactivate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&agent_id=b7b5439a-c4c9-405e-85d1-cdcb38f81877&confirm=b7b5439a-c4c9-405e-85d1-cdcb38f81877"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/deactivate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentDeactivate(w, r)
	if w.Header().Get("HX-Redirect") == "" {
		t.Error("expected HX-Redirect after deactivate")
	}
}

func TestAgentDeactivate_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&agent_id=b7b5439a-c4c9-405e-85d1-cdcb38f81877&confirm=wrong"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/deactivate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentDeactivate(w, r)
	if !strings.Contains(w.Body.String(), "does not match") {
		t.Error("expected confirmation mismatch error")
	}
}

func TestWorkspaceAgentsHasRegisterButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		agents: []AgentSummary{
			{AgentID: "434becf0-b125-41d5-8379-5a945dc8fb5e", DisplayName: "Test", IdentityProvider: "opaque", Deactivated: false},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=agents", nil)
	h.partialWorkspaceDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Register Agent") {
		t.Error("agents tab missing Register Agent button")
	}
	if !strings.Contains(body, "Deactivate") {
		t.Error("active agent missing Deactivate button")
	}
}

func TestGraphView(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/graph-view?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialGraphView(w, r)
	body := w.Body.String()
	for _, want := range []string{"graph-container", "graph-canvas", "graph-seed", "graph-depth", "loadGraph", "Context Graph"} {
		if !strings.Contains(body, want) {
			t.Errorf("graph view missing %q", want)
		}
	}
}

func TestGraphView_NoWS(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/graph-view", nil)
	h.partialGraphView(w, r)
	if !strings.Contains(w.Body.String(), "No workspace") {
		t.Error("expected empty state for missing ws")
	}
}

func TestGraphDataEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		graphData: &GraphData{
			Nodes:           []GraphNode{{ID: "22132404-d6ef-407b-9604-29c0ba7289c5", Label: "22132404-d6ef-407b-9604-29c0ba7289c5", Type: "memory"}, {ID: "ba4e82bd-10ab-4902-b12b-be6f442152da", Label: "ba4e82bd-10ab-4902-b12b-be6f442152da", Type: "memory"}},
			Edges:           []GraphEdge{{ID: "e_1", Source: "22132404-d6ef-407b-9604-29c0ba7289c5", Target: "ba4e82bd-10ab-4902-b12b-be6f442152da", Label: "related_to"}},
			NodeCount:       2,
			EdgeCountByType: map[string]int{"related_to": 1},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/data?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.handleGraphData(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"22132404-d6ef-407b-9604-29c0ba7289c5", "ba4e82bd-10ab-4902-b12b-be6f442152da", "related_to", "node_count"} {
		if !strings.Contains(body, want) {
			t.Errorf("graph data response missing %q", want)
		}
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected JSON content type, got %q", w.Header().Get("Content-Type"))
	}
}

func TestGraphDataEndpoint_NoWS(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/data", nil)
	h.handleGraphData(w, r)
	if w.Code != 400 {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraphTabInWorkspacePage(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254/graph", nil)
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "graph-view") {
		t.Error("graph page should load graph-view partial")
	}
}

func TestGraphNeighborsEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		graphData: &GraphData{
			Nodes: []GraphNode{{ID: "22132404-d6ef-407b-9604-29c0ba7289c5", Label: "22132404-d6ef-407b-9604-29c0ba7289c5", Type: "seed"}, {ID: "ba4e82bd-10ab-4902-b12b-be6f442152da", Label: "ba4e82bd-10ab-4902-b12b-be6f442152da", Type: "neighbor"}},
			Edges: []GraphEdge{{ID: "e_1", Source: "22132404-d6ef-407b-9604-29c0ba7289c5", Target: "ba4e82bd-10ab-4902-b12b-be6f442152da", Label: "references"}},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/neighbors?ws=65727904-2abe-4a88-a7a5-853476162254&memory_id=22132404-d6ef-407b-9604-29c0ba7289c5&direction=both&k=10", nil)
	h.handleGraphNeighbors(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"22132404-d6ef-407b-9604-29c0ba7289c5", "ba4e82bd-10ab-4902-b12b-be6f442152da", "references"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("neighbors response missing %q", want)
		}
	}
}

func TestGraphNeighborsEndpoint_NoMemID(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/neighbors?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.handleGraphNeighbors(w, r)
	if w.Code != 400 {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraphTraverseEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		graphData: &GraphData{
			Nodes: []GraphNode{{ID: "0b32159a-5184-42a9-9fe5-e919c2b605c9", Label: "0b32159a-5184-42a9-9fe5-e919c2b605c9", Type: "seed"}},
			Edges: nil,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/traverse?ws=65727904-2abe-4a88-a7a5-853476162254&seed=0b32159a-5184-42a9-9fe5-e919c2b605c9&depth=3&direction=out", nil)
	h.handleGraphTraverse(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "0b32159a-5184-42a9-9fe5-e919c2b605c9") {
		t.Error("traverse response missing seed node")
	}
}

func TestGraphTraverseEndpoint_NoSeed(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/traverse?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.handleGraphTraverse(w, r)
	if w.Code != 400 {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraphStatsEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/stats?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.handleGraphStats(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "node_count") || !strings.Contains(body, "references") {
		t.Error("stats response missing expected fields")
	}
}

func TestGraphViewHasQueryTabs(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/graph-view?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialGraphView(w, r)
	body := w.Body.String()
	for _, want := range []string{"graph-tab-overview", "graph-tab-neighbors", "graph-tab-traverse", "showGraphTab", "loadNeighbors", "loadTraverse", "nb-memory-id", "tr-seed", "edge-type-filter"} {
		if !strings.Contains(body, want) {
			t.Errorf("graph view missing %q", want)
		}
	}
}

func TestEdgeLinkForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/edge-link-form?ws=65727904-2abe-4a88-a7a5-853476162254&source=22132404-d6ef-407b-9604-29c0ba7289c5", nil)
	h.partialEdgeLinkForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"source_memory_id", "target_memory_id", "edge_type", "references", "parent_of", "22132404-d6ef-407b-9604-29c0ba7289c5"} {
		if !strings.Contains(body, want) {
			t.Errorf("link form missing %q", want)
		}
	}
}

func TestEdgeLink(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&source_memory_id=22132404-d6ef-407b-9604-29c0ba7289c5&target_memory_id=ba4e82bd-10ab-4902-b12b-be6f442152da&edge_type=references"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeLink(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "6b9ee74a-f21a-45c6-893e-02b24f8b1d55") {
		t.Error("expected edge ID in response")
	}
}

func TestEdgeLink_SelfLoop(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&source_memory_id=22132404-d6ef-407b-9604-29c0ba7289c5&target_memory_id=22132404-d6ef-407b-9604-29c0ba7289c5&edge_type=references"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeLink(w, r)
	if !strings.Contains(w.Body.String(), "self-loop") {
		t.Error("expected self-loop rejection")
	}
}

func TestEdgeLink_MissingFields(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&source_memory_id=22132404-d6ef-407b-9604-29c0ba7289c5"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeLink(w, r)
	if !strings.Contains(w.Body.String(), "required") {
		t.Error("expected validation error")
	}
}

func TestEdgeUnlinkForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/edge-unlink-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=163e9aec-caa3-403b-a6d1-6247040f989d", nil)
	h.partialEdgeUnlinkForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "163e9aec-caa3-403b-a6d1-6247040f989d") || !strings.Contains(body, "Unlink") {
		t.Error("unlink form missing edge ID or button")
	}
}

func TestEdgeUnlink(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&edge_id=163e9aec-caa3-403b-a6d1-6247040f989d&confirm=163e9aec-caa3-403b-a6d1-6247040f989d"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/unlink", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeUnlink(w, r)
	if w.Header().Get("HX-Redirect") == "" {
		t.Error("expected HX-Redirect after unlink")
	}
}

func TestEdgeUnlink_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&edge_id=163e9aec-caa3-403b-a6d1-6247040f989d&confirm=wrong"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/unlink", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeUnlink(w, r)
	if !strings.Contains(w.Body.String(), "does not match") {
		t.Error("expected confirmation mismatch error")
	}
}

func TestEdgesTabHasLinkButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "af76efb0-26c1-4901-a106-cdfe1acf9a64", Content: "test"},
		edges: []EdgeSummary{
			{EdgeID: "a2145e16-f3a2-48c7-b141-a0f8117f2844", SourceMemoryID: "af76efb0-26c1-4901-a106-cdfe1acf9a64", TargetMemoryID: "692a4100-24f3-4d9f-b3ba-12d75a7826df", EdgeType: "references", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=af76efb0-26c1-4901-a106-cdfe1acf9a64&ws=65727904-2abe-4a88-a7a5-853476162254&tab=edges", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Link Edge") {
		t.Error("edges tab missing Link Edge button")
	}
	if !strings.Contains(body, "Unlink") {
		t.Error("edges tab missing Unlink button")
	}
}

func TestBulkOpsForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{{ID: "ff6069b2-4f27-4d65-a135-f29b5e8fec4e", Name: "docs"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/bulk-ops-form?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialBulkOpsForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"Bulk Operations", "Seed", "Dump", "Import", "Replay", "showBulkTab", "streamBulkOp", "docs"} {
		if !strings.Contains(body, want) {
			t.Errorf("bulk ops form missing %q", want)
		}
	}
}

func TestBulkOpsForm_MissingWS(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/bulk-ops-form", nil)
	h.partialBulkOpsForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Missing workspace ID") {
		t.Error("expected error for missing workspace ID")
	}
}

func TestBulkSeed(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	records := `[{"content":"hello world","tags":{"k":"v"}}]`
	body := fmt.Sprintf("workspace_id=65727904-2abe-4a88-a7a5-853476162254&collection_id=ff6069b2-4f27-4d65-a135-f29b5e8fec4e&chunker_id=markdown&auto_link=true&records=%s", records)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/seed", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkSeed(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, `"type":"complete"`) {
		t.Errorf("expected complete event, got: %s", resp)
	}
	if !strings.Contains(resp, `"succeeded":1`) {
		t.Error("expected 1 succeeded")
	}
}

func TestBulkSeed_EmptyRecords(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=65727904-2abe-4a88-a7a5-853476162254&records=[]"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/seed", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkSeed(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, "No records to seed") {
		t.Error("expected empty records error")
	}
}

func TestBulkDump(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memories: []MemorySummary{
			{ID: "22132404-d6ef-407b-9604-29c0ba7289c5", Content: "test content", ContentMD5: "abc", AgentID: "004681e8-d399-4612-8c7b-f6b11a0428b5"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/bulk/dump?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.handleBulkDump(w, r)
	if w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Errorf("expected ndjson content type, got %s", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "22132404-d6ef-407b-9604-29c0ba7289c5") {
		t.Error("dump missing memory ID")
	}
	if !strings.Contains(w.Body.String(), "test content") {
		t.Error("dump missing content")
	}
}

func TestBulkImport(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	records := `[{"content":"imported text","content_md5":"new123"}]`
	body := fmt.Sprintf("workspace_id=65727904-2abe-4a88-a7a5-853476162254&conflict=skip&records=%s", records)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/import", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkImport(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, `"type":"complete"`) {
		t.Errorf("expected complete event, got: %s", resp)
	}
	if !strings.Contains(resp, `"succeeded":1`) {
		t.Error("expected 1 succeeded")
	}
}

func TestBulkImport_SkipDuplicate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memories: []MemorySummary{
			{ID: "80d98601-8488-4c2d-901c-27ead2416df9", ContentMD5: "dup123"},
		},
	})
	records := `[{"content":"dup content","content_md5":"dup123"}]`
	body := fmt.Sprintf("workspace_id=65727904-2abe-4a88-a7a5-853476162254&conflict=skip&records=%s", records)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/import", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkImport(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, `"skipped":1`) {
		t.Errorf("expected 1 skipped, got: %s", resp)
	}
}

func TestBulkReplay(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	records := `[{"op":"imprint","content":"replayed content"}]`
	body := fmt.Sprintf("workspace_id=65727904-2abe-4a88-a7a5-853476162254&records=%s", records)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/replay", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkReplay(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, `"type":"complete"`) {
		t.Errorf("expected complete event, got: %s", resp)
	}
	if !strings.Contains(resp, `"succeeded":1`) {
		t.Error("expected 1 succeeded")
	}
}

func TestBulkReplay_UnsupportedOp(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	records := `[{"op":"unknown_op","content":"foo"}]`
	body := fmt.Sprintf("workspace_id=65727904-2abe-4a88-a7a5-853476162254&records=%s", records)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/bulk/replay", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleBulkReplay(w, r)
	resp := w.Body.String()
	if !strings.Contains(resp, `"failed":1`) {
		t.Errorf("expected 1 failed, got: %s", resp)
	}
}

func TestMemoryListHasBulkOpsButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-list?ws=65727904-2abe-4a88-a7a5-853476162254", nil)
	h.partialMemoryList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Bulk Ops") {
		t.Error("memory list missing Bulk Ops button")
	}
	if !strings.Contains(body, "bulk-ops-form") {
		t.Error("memory list missing bulk-ops-form link")
	}
}

// realPeers returns a federation.Manager on a real SQLite store with one
// peer per name (node IDs minted, keys "key-<name>").
func realPeers(t *testing.T, names ...string) *federation.Manager {
	t.Helper()
	ctx := context.Background()
	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{DSN: filepath.Join(t.TempDir(), "peers.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true}, types.NewID())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sealer, err := federation.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	m := federation.NewManager(reg, meta, sealer)
	for _, n := range names {
		if _, err := m.Upsert(ctx, api.PeerRequest{
			NodeID: types.NewID(), Name: n, URL: "https://" + n + ".example.com",
			InboundKey: "key-" + n, APIKey: "key-" + n,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func postForm(h func(http.ResponseWriter, *http.Request), path, form string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", path, strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(w, r)
	return w
}

func TestPeerAddForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	h.partialPeerAddForm(w, httptest.NewRequest("GET", "/ui/partials/peer-add-form", nil))
	body := w.Body.String()
	for _, want := range []string{"Add Federation Peer", `name="node_id"`, `name="inbound_key"`, `name="api_key"`, `type="password"`, `name="timeout_ms"`} {
		if !strings.Contains(body, want) {
			t.Errorf("peer add form missing %q", want)
		}
	}
}

// TestPeerAdd_ReachesRegistry: the console's add form creates the peer in
// the runtime registry, keys write-only.
func TestPeerAdd_ReachesRegistry(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t)
	h.SetPeers(pm)
	id := types.NewID()
	w := postForm(h.handlePeerAdd, "/ui/api/peers/add",
		"node_id="+id+"&name=west-peer&endpoint=https://peer.example.com&inbound_key=in-secret&api_key=out-secret&timeout_ms=2500")
	if w.Header().Get("HX-Redirect") != "/ui/federation" {
		t.Fatalf("add: %q %s", w.Header().Get("HX-Redirect"), w.Body.String())
	}
	peers, _ := pm.List(context.Background())
	if len(peers) != 1 || peers[0].NodeID != id || peers[0].Name != "west-peer" || !peers[0].HasInboundKey || !peers[0].HasAPIKey || peers[0].TimeoutMS != 2500 {
		t.Fatalf("registry = %+v", peers)
	}
}

func TestPeerAdd_Validation(t *testing.T) {
	h := mustHandler(t)
	h.SetPeers(realPeers(t))
	if w := postForm(h.handlePeerAdd, "/ui/api/peers/add", "endpoint=https://peer.example.com"); !strings.Contains(w.Body.String(), "Node ID, name and endpoint are required") {
		t.Errorf("missing fields: %s", w.Body.String())
	}
	if w := postForm(h.handlePeerAdd, "/ui/api/peers/add", "node_id=peer_1&name=x&endpoint=https://x"); !strings.Contains(w.Body.String(), "node_id must be a UUID") {
		t.Errorf("bad node id: %s", w.Body.String())
	}
}

func TestPeerEditForm(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t, "west")
	h.SetPeers(pm)
	peers, _ := pm.List(context.Background())
	w := httptest.NewRecorder()
	h.partialPeerEditForm(w, httptest.NewRequest("GET", "/ui/partials/peer-edit-form?id="+peers[0].NodeID, nil))
	body := w.Body.String()
	for _, want := range []string{"Edit Peer", "west", "https://west.example.com", peers[0].NodeID, "blank keeps the stored key", "required when the endpoint changes"} {
		if !strings.Contains(body, want) {
			t.Errorf("peer edit form missing %q", want)
		}
	}
	if strings.Contains(body, "key-west") {
		t.Error("edit form shows a stored key")
	}
}

// TestPeerUpdate_KeepsKeys: editing without retyping keys keeps them and
// can revoke the peer.
func TestPeerUpdate_KeepsKeys(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t, "west")
	h.SetPeers(pm)
	peers, _ := pm.List(context.Background())
	id := peers[0].NodeID
	w := postForm(h.handlePeerUpdate, "/ui/api/peers/update", "node_id="+id+"&name=west-2&endpoint=https://west.example.com&revoked=true")
	if w.Header().Get("HX-Redirect") != "/ui/federation" {
		t.Fatalf("update: %s", w.Body.String())
	}
	peers, _ = pm.List(context.Background())
	if p := peers[0]; p.Name != "west-2" || p.URL != "https://west.example.com" || !p.Revoked || !p.HasInboundKey || !p.HasAPIKey {
		t.Fatalf("after update = %+v, want renamed, revoked, keys kept", p)
	}
	// A new endpoint never inherits the key given for the old one.
	w = postForm(h.handlePeerUpdate, "/ui/api/peers/update", "node_id="+id+"&name=west-2&endpoint=https://west2.example.com")
	if w.Header().Get("HX-Redirect") != "" || !strings.Contains(w.Body.String(), "needs its api_key again") {
		t.Fatalf("endpoint change without a key: %s", w.Body.String())
	}
	w = postForm(h.handlePeerUpdate, "/ui/api/peers/update", "node_id="+id+"&name=west-2&endpoint=https://west2.example.com&api_key=key-west2")
	if w.Header().Get("HX-Redirect") != "/ui/federation" {
		t.Fatalf("endpoint change with a key: %s", w.Body.String())
	}
	if peers, _ = pm.List(context.Background()); peers[0].URL != "https://west2.example.com" {
		t.Fatalf("after endpoint change = %+v", peers[0])
	}
}

// TestPeerUpdate_KeepsLimitsAndPin (#6350): the edit form shows a peer's
// QPS limits and client certificate pin, so saving it unchanged keeps
// them; a bad QPS is refused.
func TestPeerUpdate_KeepsLimitsAndPin(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t)
	pm.AllowClientCertPins = true
	h.SetPeers(pm)
	id, pin := types.NewID(), strings.Repeat("ab", 32)
	if _, err := pm.Upsert(context.Background(), api.PeerRequest{
		NodeID: id, Name: "west", URL: "https://west.example.com", InboundKey: "key-west", APIKey: "key-west",
		ClientCertSHA256: pin, RateLimitQPS: 2.5, InboundQPS: 7,
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.partialPeerEditForm(w, httptest.NewRequest("GET", "/ui/partials/peer-edit-form?id="+id, nil))
	for _, want := range []string{`name="rate_limit_qps" min="0" step="any" value="2.5"`, `name="inbound_qps" min="0" step="any" value="7"`, `name="client_cert_sha256" autocomplete="off" value="` + pin + `"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("edit form missing %q", want)
		}
	}

	w = postForm(h.handlePeerUpdate, "/ui/api/peers/update",
		"node_id="+id+"&name=west&endpoint=https://west.example.com&rate_limit_qps=2.5&inbound_qps=7&client_cert_sha256="+pin)
	if w.Header().Get("HX-Redirect") != "/ui/federation" {
		t.Fatalf("update: %s", w.Body.String())
	}
	peers, _ := pm.List(context.Background())
	if p := peers[0]; p.RateLimitQPS != 2.5 || p.InboundQPS != 7 || p.ClientCertSHA256 != pin || !p.HasInboundKey || !p.HasAPIKey {
		t.Fatalf("after an unchanged update = %+v, want limits, pin and keys kept", p)
	}

	for _, bad := range []string{"rate_limit_qps=-1", "inbound_qps=fast", "inbound_qps=Inf"} {
		w = postForm(h.handlePeerUpdate, "/ui/api/peers/update", "node_id="+id+"&name=west&endpoint=https://west.example.com&"+bad)
		if w.Header().Get("HX-Redirect") != "" || !strings.Contains(w.Body.String(), "must be a non-negative number") {
			t.Fatalf("%s: %s", bad, w.Body.String())
		}
	}
}

// TestPeerAdd_ExistingNodeIDRefused (#6350): the blank add form cannot
// overwrite a stored peer.
func TestPeerAdd_ExistingNodeIDRefused(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t, "west")
	h.SetPeers(pm)
	peers, _ := pm.List(context.Background())
	w := postForm(h.handlePeerAdd, "/ui/api/peers/add", "node_id="+peers[0].NodeID+"&name=impostor&endpoint=https://west.example.com")
	if w.Header().Get("HX-Redirect") != "" || !strings.Contains(w.Body.String(), "already exists") {
		t.Fatalf("add over an existing peer: %s", w.Body.String())
	}
	if after, _ := pm.List(context.Background()); after[0].Name != "west" {
		t.Fatalf("stored peer changed: %+v", after[0])
	}
}

func TestPeerEditForm_NotFound(t *testing.T) {
	h := mustHandler(t)
	h.SetPeers(realPeers(t))
	w := httptest.NewRecorder()
	h.partialPeerEditForm(w, httptest.NewRequest("GET", "/ui/partials/peer-edit-form?id="+types.NewID(), nil))
	if !strings.Contains(w.Body.String(), "Peer not found") {
		t.Error("expected peer not found error")
	}
}

func TestPeerRemoveForm(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t, "west")
	h.SetPeers(pm)
	peers, _ := pm.List(context.Background())
	w := httptest.NewRecorder()
	h.partialPeerRemoveForm(w, httptest.NewRequest("GET", "/ui/partials/peer-remove-form?id="+peers[0].NodeID, nil))
	body := w.Body.String()
	if !strings.Contains(body, "Remove Peer") || !strings.Contains(body, "west") || !strings.Contains(body, peers[0].NodeID) {
		t.Errorf("remove form = %s", body)
	}
}

func TestPeerRemove(t *testing.T) {
	h := mustHandler(t)
	pm := realPeers(t, "west")
	h.SetPeers(pm)
	peers, _ := pm.List(context.Background())
	id := peers[0].NodeID
	if w := postForm(h.handlePeerRemove, "/ui/api/peers/remove", "id="+id+"&confirm=wrong"); !strings.Contains(w.Body.String(), "Type") {
		t.Errorf("wrong confirm: %s", w.Body.String())
	}
	if peers, _ := pm.List(context.Background()); len(peers) != 1 {
		t.Fatal("a wrong confirmation removed the peer")
	}
	w := postForm(h.handlePeerRemove, "/ui/api/peers/remove", "id="+id+"&confirm=west")
	if w.Header().Get("HX-Redirect") != "/ui/federation" {
		t.Fatalf("remove: %s", w.Body.String())
	}
	if peers, _ := pm.List(context.Background()); len(peers) != 0 {
		t.Fatalf("after remove: %+v", peers)
	}
}

func TestFederationStatusHasCRUDButtons(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	h.SetFederation(&FederationInfo{FederationID: "fed_1"})
	h.SetPeers(realPeers(t, "west"))
	w := httptest.NewRecorder()
	h.partialFederationStatus(w, httptest.NewRequest("GET", "/ui/partials/federation-status", nil))
	body := w.Body.String()
	for _, want := range []string{"Add Peer", "Edit", "Remove", "fed-modal-container", "active"} {
		if !strings.Contains(body, want) {
			t.Errorf("federation status missing %q", want)
		}
	}
}

func TestIDPAddForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/idp-add-form", nil)
	h.partialIDPAddForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"Add Identity Provider", "opaque", "anthropic_session", "a2a", "did", "oauth_agent", "oidc_agent"} {
		if !strings.Contains(body, want) {
			t.Errorf("IDP add form missing %q", want)
		}
	}
}

func TestIDPAdd(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/idp/add", strings.NewReader("name=a2a"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleIDPAdd(w, r)
	if w.Header().Get("HX-Redirect") != "/ui/settings" {
		t.Errorf("expected redirect to /ui/settings, got %q", w.Header().Get("HX-Redirect"))
	}
}

func TestIDPEditForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/idp-edit-form?name=anthropic_session", nil)
	h.partialIDPEditForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Edit Provider") {
		t.Error("missing Edit Provider heading")
	}
	if !strings.Contains(body, "anthropic_session") {
		t.Error("missing provider name")
	}
}

func TestIDPRemoveForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/idp-remove-form?name=a2a", nil)
	h.partialIDPRemoveForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Remove Provider") {
		t.Error("missing Remove Provider heading")
	}
	if !strings.Contains(body, "a2a") {
		t.Error("missing provider name in confirm")
	}
}

func TestIDPRemove_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/idp/remove", strings.NewReader("name=a2a&confirm=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleIDPRemove(w, r)
	if !strings.Contains(w.Body.String(), "Type") {
		t.Error("expected confirmation error")
	}
}

func TestIDPRemove_Correct(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/idp/remove", strings.NewReader("name=a2a&confirm=a2a"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleIDPRemove(w, r)
	if w.Header().Get("HX-Redirect") != "/ui/settings" {
		t.Errorf("expected redirect to /ui/settings, got %q", w.Header().Get("HX-Redirect"))
	}
}

func TestIDPVerify(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/idp/verify?name=opaque", nil)
	h.handleIDPVerify(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Reachable") {
		t.Error("missing Reachable status")
	}
	if !strings.Contains(body, "opaque") {
		t.Error("missing provider name")
	}
}

func TestSettingsHasIDPSection(t *testing.T) {
	h := mustHandler(t)
	h.SetSettings(&SettingsInfo{
		ServerAddr: ":8080",
		IdentityProviders: []IdentityProviderSummary{
			{Name: "opaque", Description: "No verification", Configured: true},
			{Name: "a2a", Description: "Agent-to-Agent", Configured: false},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/settings-detail", nil)
	h.partialSettingsDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Identity Providers") {
		t.Error("missing Identity Providers section")
	}
	if !strings.Contains(body, "Add Provider") {
		t.Error("missing Add Provider button")
	}
	if !strings.Contains(body, "opaque") {
		t.Error("missing opaque provider")
	}
	if !strings.Contains(body, "a2a") {
		t.Error("missing a2a provider")
	}
	if !strings.Contains(body, "Active") {
		t.Error("missing Active badge")
	}
	if !strings.Contains(body, "Unconfigured") {
		t.Error("missing Unconfigured badge")
	}
	if !strings.Contains(body, "Test") {
		t.Error("missing Test button")
	}
}

func TestTagAddForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/tag-add-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=c1c4b15b-b641-42b1-a4b2-e5302797669d", nil)
	h.partialTagAddForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Add Tag") {
		t.Error("missing Add Tag heading")
	}
	if !strings.Contains(body, "65727904-2abe-4a88-a7a5-853476162254") {
		t.Error("missing workspace ID")
	}
	if !strings.Contains(body, "c1c4b15b-b641-42b1-a4b2-e5302797669d") {
		t.Error("missing memory ID")
	}
}

func TestTagUpsert(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/tags/upsert", strings.NewReader("ws=65727904-2abe-4a88-a7a5-853476162254&id=c1c4b15b-b641-42b1-a4b2-e5302797669d&key=source&value=upload"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleTagUpsert(w, r)
	redir := w.Header().Get("HX-Redirect")
	if !strings.Contains(redir, "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254/memories/c1c4b15b-b641-42b1-a4b2-e5302797669d") {
		t.Errorf("expected redirect to memory detail, got %q", redir)
	}
}

func TestTagUpsert_MissingKey(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/tags/upsert", strings.NewReader("ws=65727904-2abe-4a88-a7a5-853476162254&id=c1c4b15b-b641-42b1-a4b2-e5302797669d&value=test"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleTagUpsert(w, r)
	if !strings.Contains(w.Body.String(), "key are required") {
		t.Error("expected validation error for missing key")
	}
}

func TestTagDeleteForm(t *testing.T) {
	h := mustHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/tag-delete-form?ws=65727904-2abe-4a88-a7a5-853476162254&id=c1c4b15b-b641-42b1-a4b2-e5302797669d&key=source", nil)
	h.partialTagDeleteForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Delete Tag") {
		t.Error("missing Delete Tag heading")
	}
	if !strings.Contains(body, "source") {
		t.Error("missing tag key")
	}
}

func TestTagDelete(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/tags/delete", strings.NewReader("ws=65727904-2abe-4a88-a7a5-853476162254&id=c1c4b15b-b641-42b1-a4b2-e5302797669d&key=source"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleTagDelete(w, r)
	redir := w.Header().Get("HX-Redirect")
	if !strings.Contains(redir, "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254/memories/c1c4b15b-b641-42b1-a4b2-e5302797669d") {
		t.Errorf("expected redirect to memory detail, got %q", redir)
	}
}

func TestMemoryContentHasTagsCRUD(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{
			ID:      "c1c4b15b-b641-42b1-a4b2-e5302797669d",
			Content: "test",
			Tags:    map[string]string{"source": "upload", "type": "doc"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=c1c4b15b-b641-42b1-a4b2-e5302797669d&ws=65727904-2abe-4a88-a7a5-853476162254&tab=content", nil)
	mux := http.NewServeMux()
	h.Register(mux)
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Add Tag") {
		t.Error("missing Add Tag button")
	}
	if !strings.Contains(body, "tag-add-form") {
		t.Error("missing tag-add-form link")
	}
	if !strings.Contains(body, "Delete") {
		t.Error("missing Delete button on tags")
	}
	if !strings.Contains(body, "source") {
		t.Error("missing tag key 'source'")
	}
	if !strings.Contains(body, "upload") {
		t.Error("missing tag value 'upload'")
	}
}

func TestWorkspaceConfigTab(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		workspace: &WorkspaceDetail{
			WorkspaceSummary:          WorkspaceSummary{ID: "65727904-2abe-4a88-a7a5-853476162254", Name: "test"},
			AutoLinkEnabled:           true,
			AutoLinkThreshold:         0.8,
			AutoLinkMaxEdges:          15,
			AutoLinkMaxIncomingPerDay: 200,
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-detail?id=65727904-2abe-4a88-a7a5-853476162254&tab=config", nil)
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Auto-Link Configuration", "auto_link_enabled", "auto_link_threshold", "auto_link_max_edges", "auto_link_max_incoming", "0.80", "15", "200", "checked"} {
		if !strings.Contains(body, want) {
			t.Errorf("config tab missing %q", want)
		}
	}
}

func TestWorkspaceConfigUpdate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		workspace: &WorkspaceDetail{
			WorkspaceSummary: WorkspaceSummary{ID: "65727904-2abe-4a88-a7a5-853476162254", Name: "test"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/config", strings.NewReader("id=65727904-2abe-4a88-a7a5-853476162254&auto_link_enabled=true&auto_link_threshold=0.75&auto_link_max_edges=20&auto_link_max_incoming=150"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceConfig(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Configuration saved") {
		t.Errorf("expected success message, got: %s", body)
	}
}

func TestWorkspaceConfigUpdate_MissingID(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/config", strings.NewReader("auto_link_enabled=true"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceConfig(w, r)
	if !strings.Contains(w.Body.String(), "Workspace ID is required") {
		t.Error("expected validation error for missing workspace ID")
	}
}

func TestWorkspaceDetailHasConfigTab(t *testing.T) {
	h := mustHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/workspaces/65727904-2abe-4a88-a7a5-853476162254", nil)
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Config") {
		t.Error("workspace detail page missing Config tab")
	}
}

func TestSettingsTLSCertDetails(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		TLSEnabled:         true,
		TLSCertFile:        "/etc/certs/server.crt",
		TLSAutoSelfSign:    true,
		TLSCertFingerprint: "SHA256:AB:CD:EF:01:23",
		TLSCertNotBefore:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TLSCertNotAfter:    time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		TLSCertIssuer:      "CN=memora-self-signed",
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"Fingerprint", "SHA256:AB:CD:EF:01:23", "Valid From", "Valid Until", "Issuer", "CN=memora-self-signed", "Rotate Certificate"} {
		if !strings.Contains(body, want) {
			t.Errorf("TLS section missing %q", want)
		}
	}
}

func TestSettingsTLSCertExpired(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		TLSEnabled:      true,
		TLSCertFile:     "/etc/certs/server.crt",
		TLSCertNotAfter: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Expired") {
		t.Error("expected Expired badge for past cert")
	}
}

func TestSettingsTLSDisabled_NoRotateButton(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		TLSEnabled: false,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if strings.Contains(body, "Rotate Certificate") {
		t.Error("Rotate button should not appear when TLS is disabled")
	}
}

func TestTLSRotateForm_SelfSigned(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		TLSEnabled:      true,
		TLSAutoSelfSign: true,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/tls-rotate-form", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"Rotate Certificate", "self-signed", "rotate", "confirm"} {
		if !strings.Contains(body, want) {
			t.Errorf("rotate form missing %q", want)
		}
	}
}

func TestTLSRotateForm_Upload(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		TLSEnabled:      true,
		TLSAutoSelfSign: false,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/tls-rotate-form", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "cert_file") {
		t.Error("upload mode should show file input for cert")
	}
	if !strings.Contains(body, "key_file") {
		t.Error("upload mode should show file input for key")
	}
}

func TestTLSRotateForm_TLSDisabled(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: false})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/tls-rotate-form", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "not enabled") {
		t.Error("expected error when TLS disabled")
	}
}

func TestTLSRotate_SelfSigned(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: true, TLSAutoSelfSign: true})
	mux := http.NewServeMux()
	h.Register(mux)

	body := strings.NewReader("mode=self-signed&confirm=rotate")
	r := httptest.NewRequest(http.MethodPost, "/ui/api/tls/rotate", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "regeneration requested") {
		t.Error("expected success message for self-signed rotation")
	}
}

func TestTLSRotate_BadConfirm(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: true})
	mux := http.NewServeMux()
	h.Register(mux)

	body := strings.NewReader("mode=self-signed&confirm=wrong")
	r := httptest.NewRequest(http.MethodPost, "/ui/api/tls/rotate", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "rotate") {
		t.Error("expected confirmation error")
	}
}

func TestTLSRotate_Upload(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: true})
	mux := http.NewServeMux()
	h.Register(mux)

	body := strings.NewReader("mode=upload&confirm=rotate")
	r := httptest.NewRequest(http.MethodPost, "/ui/api/tls/rotate", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "uploaded") {
		t.Error("expected upload success message")
	}
}

func TestTLSRotate_TLSDisabled(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: false})
	mux := http.NewServeMux()
	h.Register(mux)

	body := strings.NewReader("mode=self-signed&confirm=rotate")
	r := httptest.NewRequest(http.MethodPost, "/ui/api/tls/rotate", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "not enabled") {
		t.Error("expected error when TLS disabled")
	}
}

func TestTLSRotate_MethodNotAllowed(t *testing.T) {
	h, _ := NewHandler()
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{TLSEnabled: true})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/api/tls/rotate", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ----- Pin CRUD Tests -----

func TestPinList_Empty(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/pin-list?ws=759dbbe1-9fcc-4069-90ec-f799ac861b95", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "No Pins") {
		t.Error("expected empty state message")
	}
	if !strings.Contains(body, "Create Pin") {
		t.Error("expected Create Pin button")
	}
}

func TestPinList_WithPins(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		pins: []PinSummary{
			{PinID: "43dbf603-78de-4f3a-b028-7b99858194a0", Query: "test query", Mode: "hybrid", K: 10, Label: "my pin", CreatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/pin-list?ws=759dbbe1-9fcc-4069-90ec-f799ac861b95", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "43dbf603-78de-4f3a-b028-7b99858194a0") {
		t.Error("expected pin ID in list")
	}
	if !strings.Contains(body, "my pin") {
		t.Error("expected pin label")
	}
	if !strings.Contains(body, "Delete") {
		t.Error("expected Delete button")
	}
}

func TestPinCreateForm(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/pin-create-form?ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&q=hello&mode=vector&k=5", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Recall Pin") {
		t.Error("expected form title")
	}
	if !strings.Contains(body, "hello") {
		t.Error("expected query value prefilled")
	}
}

func TestPinCreate_Success(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodPost, "/ui/api/pins/create", strings.NewReader("ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&query=hello+world&mode=hybrid&k=10"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ac594a2e-78d9-4b5c-ae8c-3c3f81f73745") {
		t.Error("expected pin ID in response")
	}
}

func TestPinCreate_MissingQuery(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodPost, "/ui/api/pins/create", strings.NewReader("ws=759dbbe1-9fcc-4069-90ec-f799ac861b95"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "required") {
		t.Error("expected validation error")
	}
}

func TestPinDelete_Success(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodPost, "/ui/api/pins/delete", strings.NewReader("ws=759dbbe1-9fcc-4069-90ec-f799ac861b95&id=43dbf603-78de-4f3a-b028-7b99858194a0"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Header().Get("HX-Redirect") == "" {
		t.Error("expected HX-Redirect header after delete")
	}
}

func TestPinDelete_MethodNotAllowed(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/api/pins/delete", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestDashboardCards_HealthBadgeReady(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{
		stats: DashboardStats{WorkspaceCount: 1},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Ready") {
		t.Error("expected Ready badge on dashboard")
	}
	if !strings.Contains(body, `href="/ui/settings?tab=diagnostics"`) {
		t.Error("expected health badge to link to settings diagnostics")
	}
}

func TestDashboardCards_FallbackIncludesHealth(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Health") {
		t.Error("expected Health card in fallback dashboard")
	}
	if !strings.Contains(body, "Unknown") {
		t.Error("expected Unknown badge in fallback dashboard")
	}
}

func TestSettingsDetail_DiagnosticsSection(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h.SetDataSource(&mockDataSource{})
	h.SetSettings(&SettingsInfo{
		ServerAddr: ":7777",
		ServerMode: "single-tenant",
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/settings-detail", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Diagnostics") {
		t.Error("expected Diagnostics section in settings")
	}
	if !strings.Contains(body, "metadata") {
		t.Error("expected metadata adapter in diagnostics")
	}
	if !strings.Contains(body, `badge-ok`) {
		t.Error("expected OK badge for healthy adapter")
	}
}

func TestCellPreview(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"plain", "Hello world", "Hello world"},
		{"heading then body", "# Title\nSome body content here", "Some body content here"},
		{"heading+bold then body", "# Title\n**Subtitle**\nActual content", "Actual content"},
		{"all headings", "# A\n## B\n### C", "# A ## B ### C"},
		{"empty lines", "\n\n# Heading\n\nBody text", "Body text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cellPreview(tt.text, 80)
			if !strings.Contains(got, tt.want[:min(len(tt.want), 20)]) {
				t.Errorf("cellPreview(%q) = %q, want substring %q", tt.text, got, tt.want)
			}
		})
	}
}
