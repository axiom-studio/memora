package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(body, "Recall") {
		t.Error("memories page missing recall section")
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

func TestGraphStubPage(t *testing.T) {
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
	if !strings.Contains(body, "coming in v0.5") {
		t.Error("missing graph stub message")
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
