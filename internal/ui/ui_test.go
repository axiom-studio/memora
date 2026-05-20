package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	stats       DashboardStats
	entries     []api.LedgerEntry
	workspaces  []WorkspaceSummary
	workspace   *WorkspaceDetail
	agents      []AgentSummary
	collections []CollectionSummary
	memories    []MemorySummary
	memory      *MemorySummary
	cells       []CellSummary
	edges       []EdgeSummary
	recallResp   *api.RecallResponse
	graphData    *GraphData
	watermarks   []types.WatermarkHistoryEntry
	auditEntries []api.LedgerEntry
	auditCursor  string
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
	return m.RecentLedgerEntries(nil, limit)
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
func (m *mockDataSource) GetMemory(_ context.Context, id string) (*MemorySummary, error) {
	if m.memory != nil {
		return m.memory, m.err
	}
	return nil, fmt.Errorf("memory %s not found", id)
}
func (m *mockDataSource) GetCells(_ context.Context, _ string) ([]CellSummary, error) {
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
	return "ws_test_new", m.err
}
func (m *mockDataSource) UpdateWorkspace(_ context.Context, _ string, _ UpdateWorkspaceInput) error {
	return m.err
}
func (m *mockDataSource) DeleteWorkspace(_ context.Context, _ string) error {
	return m.err
}
func (m *mockDataSource) CreateCollection(_ context.Context, input CreateCollectionInput) (string, error) {
	return "coll_test_new", m.err
}
func (m *mockDataSource) DeleteCollection(_ context.Context, _ string) error {
	return m.err
}
func (m *mockDataSource) ImprintMemory(_ context.Context, _ string, _ api.ImprintRequest) (*api.ImprintResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &api.ImprintResponse{
		MemoryID:     "mem_test_123",
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
		MemoryID:   "mem_test_123",
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
		MemoryID:      "mem_test_123",
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
		MemoryID:       "mem_test_123",
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
		MemoryID:     "mem_test_123",
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
	return "edge_test_new", nil
}
func (m *mockDataSource) UnlinkEdge(_ context.Context, _ string) error {
	return m.err
}
func (m *mockDataSource) GetWatermarkHistory(_ context.Context, _, _ string, _ time.Time) ([]types.WatermarkHistoryEntry, error) {
	return m.watermarks, m.err
}
func (m *mockDataSource) AuditQuery(_ context.Context, _, _ string, _ []string, _, _ *time.Time, _ string, _ int) ([]api.LedgerEntry, string, error) {
	return m.auditEntries, m.auditCursor, m.err
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
			FederationPeers: 2,
			HealthyPeers:    1,
		},
	})

	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/dashboard-cards", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{"3", "42", "95%", "7", "1 / 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard cards missing %q in body: %s", want, body)
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
			{Op: "imprint", Target: "mem_abc", AgentID: "agent_1", Timestamp: time.Now()},
			{Op: "recall", Target: "mem_xyz", AgentID: "agent_2", Timestamp: time.Now()},
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
			{ID: "ws_abc", Name: "Production", EmbeddingModel: "text-embedding-3-small", AgentCount: 3, MemoryCount: 42, CreatedAt: time.Now()},
			{ID: "ws_def", Name: "Staging", EmbeddingModel: "text-embedding-3-small", AgentCount: 1, MemoryCount: 5, CreatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "ws_abc") || !strings.Contains(body, "Production") {
		t.Errorf("workspace list missing first workspace: %s", body)
	}
	if !strings.Contains(body, "ws_def") || !strings.Contains(body, "Staging") {
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
				ID: "ws_abc", Name: "Production", EmbeddingModel: "text-embedding-3-small",
				AgentCount: 3, MemoryCount: 42, CreatedAt: time.Now(),
			},
			Region: "us-east-1", ChunkerID: "paragraph", AutoLinkEnabled: true,
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=ws_abc&tab=overview", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{"ws_abc", "Production", "us-east-1", "paragraph", "Enabled"} {
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
			{AgentID: "agent_1", DisplayName: "Bot A", IdentityProvider: "opaque", AgentType: "chat"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=ws_abc&tab=agents", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "agent_1") || !strings.Contains(body, "Bot A") {
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
			{ID: "coll_1", Name: "Knowledge Base", CreatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/workspace-detail?id=ws_abc&tab=collections", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, "coll_1") || !strings.Contains(body, "Knowledge Base") {
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
			{ID: "mem_abc", AgentID: "agent_1", RecallReady: true, Content: "Hello world", UpdatedAt: time.Now()},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-list?ws=ws_test", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "mem_abc") {
		t.Error("memory list missing mem_abc")
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
			ID: "mem_abc", Content: "Full content here", ContentMD5: "abc123",
			Watermark: "wmk_1", AgentID: "agent_1", RecallReady: true,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=mem_abc&ws=ws_test&tab=content", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"mem_abc", "Full content here", "abc123", "wmk_1"} {
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

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=mem_abc&ws=ws_test&tab=cells", nil)
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
			{EdgeID: "edge_1", SourceMemoryID: "mem_abc", TargetMemoryID: "mem_def", EdgeType: "related", AgentID: "agent_1"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=mem_abc&ws=ws_test&tab=edges", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "edge_1") || !strings.Contains(body, "related") {
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
				TargetID:        "mem_abc",
				Watermark:       "wmk_01EXAMPLE",
				Op:              "imprint",
				AgentID:         "agent_test",
				CreatedAt:       time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
				ContentMD5After: "abc123",
			},
			{
				TargetID:         "mem_abc",
				Watermark:        "wmk_02EXAMPLE",
				Op:               "update",
				AgentID:          "agent_test",
				CreatedAt:        time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC),
				ContentMD5Before: "abc123",
				ContentMD5After:  "def456",
			},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=mem_abc&ws=ws_test&tab=watermarks", nil)
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
	if !strings.Contains(body, "agent_test") {
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

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/memory-detail?id=mem_abc&ws=ws_test&tab=watermarks", nil)
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
				{MemoryID: "mem_abc", Score: 0.95, Text: "match text", Via: "seed"},
			},
			TotalCandidatesScanned: 100,
			LatencyMS:              15,
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/recall-results?ws=ws_test&q=test+query&mode=hybrid", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "mem_abc") || !strings.Contains(body, "0.95") {
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

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/ws_abc/memories", nil)
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

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/ws_abc/memories/mem_123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "mem_123") {
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

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/ws_abc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ws_abc") {
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
			{LedgerID: "led-1", Op: "imprint", Target: "mem-abc", AgentID: "agent-1", WorkspaceID: "ws-1", LatencyMS: 42, Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)},
			{LedgerID: "led-2", Op: "forget", Target: "mem-xyz", AgentID: "agent-2", WorkspaceID: "ws-1", LatencyMS: 7, Timestamp: time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)},
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
			{LedgerID: "led-1", Op: "imprint", Target: "mem-abc", AgentID: "agent-1", WorkspaceID: "ws-1", LatencyMS: 42, Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)},
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
		ServerAddr:     ":7777",
		ServerMode:     "single-tenant",
		MCPEnabled:     true,
		TLSEnabled:     true,
		TLSCertFile:    "/etc/certs/server.crt",
		DataDir:        "/var/lib/memora",
		MetadataDriver: "sqlite",
		VectorDriver:   "sqlitevec",
		LedgerDriver:   "sqlite",
		GraphDriver:    "sqlite",
		ContentDriver:  "file",
		EmbeddingModel: "nomic-embed-text",
		FederationEnabled: true,
		FederationID:      "fed-abc",
		PeerCount:         2,
		TelemetryLogLevel: "info",
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
	h.SetFederation(&FederationInfo{
		FederationID: "fed-test-123",
		Peers: []PeerInfo{
			{ID: "peer-0", Name: "us-east", Endpoint: "https://east.example.com", TrustMode: "mtls"},
			{ID: "peer-1", Name: "eu-west", Endpoint: "https://west.example.com", TrustMode: "api_key", Workspaces: []string{"ws-1", "ws-2"}},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/ui/partials/federation-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"fed-test-123", "us-east", "eu-west", "mtls", "api_key", "east.example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in federation status", want)
		}
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

	r := httptest.NewRequest(http.MethodGet, "/ui/workspaces/ws-1/graph", nil)
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
			{ID: "ws-alpha", Name: "Alpha Workspace"},
			{ID: "ws-beta", Name: "Beta Workspace"},
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
	if !strings.Contains(body, "ws-alpha") {
		t.Error("missing workspace result")
	}
	if strings.Contains(body, "ws-beta") {
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
		workspaces: []WorkspaceSummary{{ID: "ws-1", Name: "Test"}},
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
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "/ui/workspaces/ws_test_new") {
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
	form := strings.NewReader("id=ws_123&name=updated-ws")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/workspaces/update", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleWorkspaceUpdate(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "/ui/workspaces/ws_123") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestWorkspaceDelete(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	form := strings.NewReader("id=ws_123&confirm=ws_123")
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
	form := strings.NewReader("id=ws_123&confirm=wrong")
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
			WorkspaceSummary: WorkspaceSummary{ID: "ws_123", Name: "test-ws", EmbeddingModel: "noop:default"},
			Region:           "us-east-1",
			ChunkerID:        "default",
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-edit-form?id=ws_123", nil)
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
			WorkspaceSummary: WorkspaceSummary{ID: "ws_123", Name: "test-ws", MemoryCount: 5, AgentCount: 2},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-delete-form?id=ws_123", nil)
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
		workspaces: []WorkspaceSummary{{ID: "ws_1", Name: "test"}},
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
	r := httptest.NewRequest("GET", "/ui/partials/collection-create-form?ws=ws_abc", nil)
	h.partialCollectionCreateForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Collection") {
		t.Error("missing Create Collection heading")
	}
	if !strings.Contains(body, `value="ws_abc"`) {
		t.Error("missing workspace_id hidden field")
	}
}

func TestCollectionCreate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/create", strings.NewReader("workspace_id=ws_abc&name=my-coll"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionCreate(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "ws_abc") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestCollectionCreate_MissingName(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/create", strings.NewReader("workspace_id=ws_abc&name="))
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
	r := httptest.NewRequest("POST", "/ui/api/collections/delete", strings.NewReader("id=coll_xyz&workspace_id=ws_abc&confirm=coll_xyz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleCollectionDelete(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "ws_abc") {
		t.Errorf("expected redirect to workspace, got %q", loc)
	}
}

func TestCollectionDelete_WrongConfirm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/collections/delete", strings.NewReader("id=coll_xyz&workspace_id=ws_abc&confirm=wrong"))
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
			{ID: "coll_xyz", Name: "test-coll", MemoryCount: 5},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/collection-delete-form?id=coll_xyz&ws=ws_abc", nil)
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
		collections: []CollectionSummary{{ID: "coll_1", Name: "test"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-detail?id=ws_abc&tab=collections", nil)
	h.partialWorkspaceDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Create Collection") {
		t.Error("collections tab missing Create Collection button")
	}
}

func TestMemoryImprintForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		collections: []CollectionSummary{{ID: "coll_1", Name: "docs"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-imprint-form?ws=ws_abc", nil)
	h.partialMemoryImprintForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Imprint Memory") {
		t.Error("missing Imprint Memory heading")
	}
	if !strings.Contains(body, `value="ws_abc"`) {
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
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=ws_abc&content=hello+world&chunker_id=markdown"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryImprint(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "mem_test_123") {
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
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=ws_abc&content="))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/imprint", strings.NewReader("workspace_id=ws_abc&content=test&tag_key=env&tag_value=prod&tag_key=team&tag_value=backend"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryImprint(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "mem_test_123") {
		t.Error("missing memory ID in response")
	}
}

func TestMemoryListHasNewMemoryButton(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-list?ws=ws_abc", nil)
	h.partialMemoryList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "New Memory") {
		t.Error("memory list missing New Memory button")
	}
}

func TestMemoryEditForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "mem_abc", Content: "hello world", Watermark: "wm_123", Tags: map[string]string{"env": "prod"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-edit-form?ws=ws_abc&id=mem_abc", nil)
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
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&content=updated+content&expected_watermark=wm_123"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&content=updated&expected_watermark=wm_old"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/update", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&content="))
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
		memory: &MemorySummary{ID: "mem_abc", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=mem_abc&ws=ws_abc&tab=content", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Edit") {
		t.Error("memory detail missing Edit button")
	}
}

func TestMemoryPatchForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "mem_abc", Content: "hello world", Watermark: "wm_123"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-patch-form?ws=ws_abc&id=mem_abc", nil)
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
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&old_string=hello&new_string=goodbye&replace_all=false&expected_watermark=wm_123"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&old_string=&new_string=test"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/patch", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&old_string=hello&new_string=bye&replace_all=false&expected_watermark=wm_old"))
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
		memory: &MemorySummary{ID: "mem_abc", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=mem_abc&ws=ws_abc&tab=content", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Patch") {
		t.Error("memory detail missing Patch button")
	}
}

func TestMemoryAppendForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		memory: &MemorySummary{ID: "mem_abc", Content: "hello", Watermark: "wm_123"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-append-form?ws=ws_abc&id=mem_abc", nil)
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
	r := httptest.NewRequest("POST", "/ui/api/memories/append", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&content=appended+text&expected_watermark=wm_123"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/append", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&content="))
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
		memory: &MemorySummary{ID: "mem_abc", Content: "test"},
		cells:  []CellSummary{{CellID: "cell_1"}, {CellID: "cell_2"}},
		edges:  []EdgeSummary{{EdgeID: "edge_1"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-forget-form?ws=ws_abc&id=mem_abc", nil)
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
	r := httptest.NewRequest("POST", "/ui/api/memories/forget", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&confirm=mem_abc"))
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
	r := httptest.NewRequest("POST", "/ui/api/memories/forget", strings.NewReader("workspace_id=ws_abc&memory_id=mem_abc&confirm=wrong"))
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
		memory: &MemorySummary{ID: "mem_abc", Content: "test"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=mem_abc&ws=ws_abc&tab=content", nil)
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
		collections: []CollectionSummary{{ID: "coll_1", Name: "docs"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/upload-form?ws=ws_abc", nil)
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
	r := httptest.NewRequest("POST", "/ui/api/memories/upload", strings.NewReader("workspace_id=ws_abc&content=uploaded+text&chunker_id=markdown"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleMemoryUpload(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Document Uploaded") {
		t.Error("missing success heading")
	}
	if !strings.Contains(body, "mem_test_123") {
		t.Error("missing memory ID")
	}
}

func TestMemoryUpload_EmptyContent(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/memories/upload", strings.NewReader("workspace_id=ws_abc&content="))
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
	r := httptest.NewRequest("GET", "/ui/partials/memory-list?ws=ws_abc", nil)
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
			{ID: "coll_1", Name: "Notes"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-query-bar?ws=ws_abc", nil)
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
				{MemoryID: "mem_1", Score: 0.95, Text: "hello world", Via: "seed"},
				{MemoryID: "mem_2", Score: 0.8, Text: "graph result", Via: "graph",
					GraphProvenance: &api.GraphProvenance{
						FromMemoryID: "mem_1", EdgeID: "e1", EdgeType: "related", Layer: 1,
					}},
			},
			TotalCandidatesScanned: 50,
			LatencyMS:              12,
			GraphNodesExpanded:     3,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=ws_abc&q=hello&mode=hybrid&k=10&graph_depth=2&graph_direction=out", nil)
	h.partialRecallFull(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"mem_1", "mem_2", "0.950", "graph nodes expanded", "Graph Path", "related", "L1"} {
		if !strings.Contains(body, want) {
			t.Errorf("recall full results missing %q", want)
		}
	}
}

func TestRecallFull_WithFilters(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		recallResp: &api.RecallResponse{
			Results:                []api.RecallHit{{MemoryID: "mem_f", Score: 0.7, Text: "filtered", Via: "seed"}},
			TotalCandidatesScanned: 10,
			LatencyMS:              5,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=ws_abc&q=test&collection_id=coll_1&agent_id=agent_x&ts_after=2025-01-01&ts_before=2025-12-31", nil)
	h.partialRecallFull(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "mem_f") {
		t.Error("expected filtered result")
	}
}

func TestRecallFull_NoQuery(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=ws_abc", nil)
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
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=ws_abc&q=nothing", nil)
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
	r := httptest.NewRequest("GET", "/ui/partials/recall-full?ws=ws_abc&q=test", nil)
	h.partialRecallFull(w, r)
	if !strings.Contains(w.Body.String(), "still being embedded") {
		t.Error("expected embedding pending notice")
	}
}

func TestAgentRegisterForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/agent-register-form?ws=ws_abc", nil)
	h.partialAgentRegisterForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"agent_id", "display_name", "identity_provider", "agent_type", "model", "Register"} {
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
	body := "workspace_id=ws_abc&agent_id=agent_test_bot&display_name=Test+Bot&identity_provider=opaque&agent_type=assistant&model=claude"
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

func TestAgentRegister_BadPrefix(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&agent_id=bad_id&identity_provider=opaque"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentRegister(w, r)
	if !strings.Contains(w.Body.String(), "must start with agent_") {
		t.Error("expected prefix validation error")
	}
}

func TestAgentRegister_MissingID(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&agent_id=&identity_provider=opaque"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/agents/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleAgentRegister(w, r)
	if !strings.Contains(w.Body.String(), "required") {
		t.Error("expected required validation error")
	}
}

func TestAgentDeactivateForm(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/agent-deactivate-form?ws=ws_abc&id=agent_bot", nil)
	h.partialAgentDeactivateForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "agent_bot") {
		t.Error("deactivate form missing agent ID")
	}
	if !strings.Contains(body, "Deactivate") {
		t.Error("deactivate form missing button")
	}
}

func TestAgentDeactivate(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&agent_id=agent_bot&confirm=agent_bot"
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
	body := "workspace_id=ws_abc&agent_id=agent_bot&confirm=wrong"
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
			{AgentID: "agent_test", DisplayName: "Test", IdentityProvider: "opaque", Deactivated: false},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/workspace-detail?id=ws_abc&tab=agents", nil)
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
	r := httptest.NewRequest("GET", "/ui/partials/graph-view?ws=ws_abc", nil)
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
			Nodes:           []GraphNode{{ID: "mem_1", Label: "mem_1", Type: "memory"}, {ID: "mem_2", Label: "mem_2", Type: "memory"}},
			Edges:           []GraphEdge{{ID: "e_1", Source: "mem_1", Target: "mem_2", Label: "related_to"}},
			NodeCount:       2,
			EdgeCountByType: map[string]int{"related_to": 1},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/data?ws=ws_abc", nil)
	h.handleGraphData(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"mem_1", "mem_2", "related_to", "node_count"} {
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
	r := httptest.NewRequest("GET", "/ui/workspaces/ws_abc/graph", nil)
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
			Nodes: []GraphNode{{ID: "mem_1", Label: "mem_1", Type: "seed"}, {ID: "mem_2", Label: "mem_2", Type: "neighbor"}},
			Edges: []GraphEdge{{ID: "e_1", Source: "mem_1", Target: "mem_2", Label: "references"}},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/neighbors?ws=ws_abc&memory_id=mem_1&direction=both&k=10", nil)
	h.handleGraphNeighbors(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, want := range []string{"mem_1", "mem_2", "references"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("neighbors response missing %q", want)
		}
	}
}

func TestGraphNeighborsEndpoint_NoMemID(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/neighbors?ws=ws_abc", nil)
	h.handleGraphNeighbors(w, r)
	if w.Code != 400 {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraphTraverseEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{
		graphData: &GraphData{
			Nodes: []GraphNode{{ID: "mem_s", Label: "mem_s", Type: "seed"}},
			Edges: nil,
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/traverse?ws=ws_abc&seed=mem_s&depth=3&direction=out", nil)
	h.handleGraphTraverse(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "mem_s") {
		t.Error("traverse response missing seed node")
	}
}

func TestGraphTraverseEndpoint_NoSeed(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/traverse?ws=ws_abc", nil)
	h.handleGraphTraverse(w, r)
	if w.Code != 400 {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraphStatsEndpoint(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/api/graph/stats?ws=ws_abc", nil)
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
	r := httptest.NewRequest("GET", "/ui/partials/graph-view?ws=ws_abc", nil)
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
	r := httptest.NewRequest("GET", "/ui/partials/edge-link-form?ws=ws_abc&source=mem_1", nil)
	h.partialEdgeLinkForm(w, r)
	body := w.Body.String()
	for _, want := range []string{"source_memory_id", "target_memory_id", "edge_type", "references", "parent_of", "mem_1"} {
		if !strings.Contains(body, want) {
			t.Errorf("link form missing %q", want)
		}
	}
}

func TestEdgeLink(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&source_memory_id=mem_1&target_memory_id=mem_2&edge_type=references"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ui/api/edges/link", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleEdgeLink(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "edge_test_new") {
		t.Error("expected edge ID in response")
	}
}

func TestEdgeLink_SelfLoop(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&source_memory_id=mem_1&target_memory_id=mem_1&edge_type=references"
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
	body := "workspace_id=ws_abc&source_memory_id=mem_1"
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
	r := httptest.NewRequest("GET", "/ui/partials/edge-unlink-form?ws=ws_abc&id=edge_123", nil)
	h.partialEdgeUnlinkForm(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "edge_123") || !strings.Contains(body, "Unlink") {
		t.Error("unlink form missing edge ID or button")
	}
}

func TestEdgeUnlink(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	body := "workspace_id=ws_abc&edge_id=edge_123&confirm=edge_123"
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
	body := "workspace_id=ws_abc&edge_id=edge_123&confirm=wrong"
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
		memory: &MemorySummary{ID: "mem_x", Content: "test"},
		edges: []EdgeSummary{
			{EdgeID: "edge_1", SourceMemoryID: "mem_x", TargetMemoryID: "mem_y", EdgeType: "references", AgentID: "agent_1"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ui/partials/memory-detail?id=mem_x&ws=ws_abc&tab=edges", nil)
	h.partialMemoryDetail(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Link Edge") {
		t.Error("edges tab missing Link Edge button")
	}
	if !strings.Contains(body, "Unlink") {
		t.Error("edges tab missing Unlink button")
	}
}
