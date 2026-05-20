package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var funcMap = template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	},
	"formatTimeShort": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("Jan 2 15:04")
	},
	"truncate": func(n int, s string) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
	"pluralize": func(count int, singular, plural string) string {
		if count == 1 {
			return singular
		}
		return plural
	},
	"join": strings.Join,
}

type pageData struct {
	Title string
	Nav   string
	Data  any
}

type Handler struct {
	pages    map[string]*template.Template
	loginTmpl *template.Template
	staticFS http.Handler
	authCfg  AuthConfig
	data       DataSource
	settings   *SettingsInfo
	federation *FederationInfo
}

func (h *Handler) SetDataSource(ds DataSource)    { h.data = ds }
func (h *Handler) SetSettings(s *SettingsInfo)     { h.settings = s }
func (h *Handler) SetFederation(f *FederationInfo) { h.federation = f }

func NewHandler() (*Handler, error) {
	layoutBytes, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	layoutSrc := string(layoutBytes)

	pageNames := []string{"home", "workspaces", "memories", "federation", "audit", "settings"}
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		pageSrc, err := templateFS.ReadFile(fmt.Sprintf("templates/%s.html", name))
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", name, err)
		}
		tmpl, err := template.New("layout").Funcs(funcMap).Parse(layoutSrc)
		if err != nil {
			return nil, fmt.Errorf("parse layout for %s: %w", name, err)
		}
		if _, err := tmpl.Parse(string(pageSrc)); err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		pages[name] = tmpl
	}

	loginSrc, err := templateFS.ReadFile("templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("read login template: %w", err)
	}
	loginTmpl, err := template.New("login").Parse(string(loginSrc))
	if err != nil {
		return nil, fmt.Errorf("parse login template: %w", err)
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static sub-fs: %w", err)
	}

	return &Handler{
		pages:    pages,
		loginTmpl: loginTmpl,
		staticFS: http.StripPrefix("/ui/static/", http.FileServer(http.FS(sub))),
	}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/ui/login", h.handleLogin)
	mux.HandleFunc("/ui/logout", h.handleLogout)
	mux.Handle("/ui/static/", h.staticFS)

	protected := http.NewServeMux()
	protected.HandleFunc("/ui/", h.handlePage)
	protected.HandleFunc("/ui/partials/dashboard-cards", h.partialDashboardCards)
	protected.HandleFunc("/ui/partials/recent-activity", h.partialRecentActivity)
	protected.HandleFunc("/ui/partials/workspace-list", h.partialWorkspaceList)
	protected.HandleFunc("/ui/partials/workspace-detail", h.partialWorkspaceDetail)
	protected.HandleFunc("/ui/partials/memory-list", h.partialMemoryList)
	protected.HandleFunc("/ui/partials/memory-detail", h.partialMemoryDetail)
	protected.HandleFunc("/ui/partials/recall-results", h.partialRecallResults)
	protected.HandleFunc("/ui/partials/recall-full", h.partialRecallFull)
	protected.HandleFunc("/ui/partials/recall-query-bar", h.partialRecallQueryBar)
	protected.HandleFunc("/ui/partials/federation-status", h.partialFederationStatus)
	protected.HandleFunc("/ui/partials/audit-list", h.partialAuditList)
	protected.HandleFunc("/ui/partials/audit-detail", h.partialAuditDetail)
	protected.HandleFunc("/ui/audit/export", h.handleAuditExport)
	protected.HandleFunc("/ui/partials/settings-detail", h.partialSettingsDetail)
	protected.HandleFunc("/ui/partials/workspace-create-form", h.partialWorkspaceCreateForm)
	protected.HandleFunc("/ui/partials/workspace-edit-form", h.partialWorkspaceEditForm)
	protected.HandleFunc("/ui/partials/workspace-delete-form", h.partialWorkspaceDeleteForm)
	protected.HandleFunc("/ui/api/workspaces/create", h.handleWorkspaceCreate)
	protected.HandleFunc("/ui/api/workspaces/update", h.handleWorkspaceUpdate)
	protected.HandleFunc("/ui/api/workspaces/delete", h.handleWorkspaceDelete)
	protected.HandleFunc("/ui/api/workspaces/config", h.handleWorkspaceConfig)
	protected.HandleFunc("/ui/partials/agent-register-form", h.partialAgentRegisterForm)
	protected.HandleFunc("/ui/partials/agent-deactivate-form", h.partialAgentDeactivateForm)
	protected.HandleFunc("/ui/api/agents/register", h.handleAgentRegister)
	protected.HandleFunc("/ui/api/agents/deactivate", h.handleAgentDeactivate)
	protected.HandleFunc("/ui/partials/collection-create-form", h.partialCollectionCreateForm)
	protected.HandleFunc("/ui/partials/collection-delete-form", h.partialCollectionDeleteForm)
	protected.HandleFunc("/ui/api/collections/create", h.handleCollectionCreate)
	protected.HandleFunc("/ui/api/collections/delete", h.handleCollectionDelete)
	protected.HandleFunc("/ui/partials/memory-imprint-form", h.partialMemoryImprintForm)
	protected.HandleFunc("/ui/partials/memory-edit-form", h.partialMemoryEditForm)
	protected.HandleFunc("/ui/api/memories/imprint", h.handleMemoryImprint)
	protected.HandleFunc("/ui/api/memories/update", h.handleMemoryUpdate)
	protected.HandleFunc("/ui/partials/memory-patch-form", h.partialMemoryPatchForm)
	protected.HandleFunc("/ui/api/memories/patch", h.handleMemoryPatch)
	protected.HandleFunc("/ui/partials/memory-append-form", h.partialMemoryAppendForm)
	protected.HandleFunc("/ui/api/memories/append", h.handleMemoryAppend)
	protected.HandleFunc("/ui/partials/memory-forget-form", h.partialMemoryForgetForm)
	protected.HandleFunc("/ui/api/memories/forget", h.handleMemoryForget)
	protected.HandleFunc("/ui/partials/upload-form", h.partialUploadForm)
	protected.HandleFunc("/ui/api/memories/upload", h.handleMemoryUpload)
	protected.HandleFunc("/ui/partials/edge-link-form", h.partialEdgeLinkForm)
	protected.HandleFunc("/ui/partials/edge-unlink-form", h.partialEdgeUnlinkForm)
	protected.HandleFunc("/ui/api/edges/link", h.handleEdgeLink)
	protected.HandleFunc("/ui/api/edges/unlink", h.handleEdgeUnlink)
	protected.HandleFunc("/ui/partials/graph-view", h.partialGraphView)
	protected.HandleFunc("/ui/api/graph/data", h.handleGraphData)
	protected.HandleFunc("/ui/api/graph/neighbors", h.handleGraphNeighbors)
	protected.HandleFunc("/ui/api/graph/traverse", h.handleGraphTraverse)
	protected.HandleFunc("/ui/api/graph/stats", h.handleGraphStats)
	protected.HandleFunc("/ui/partials/search", h.partialSearch)
	protected.HandleFunc("/ui/partials/tag-add-form", h.partialTagAddForm)
	protected.HandleFunc("/ui/partials/tag-delete-form", h.partialTagDeleteForm)
	protected.HandleFunc("/ui/api/tags/upsert", h.handleTagUpsert)
	protected.HandleFunc("/ui/api/tags/delete", h.handleTagDelete)
	protected.HandleFunc("/ui/partials/tls-rotate-form", h.partialTLSRotateForm)
	protected.HandleFunc("/ui/api/tls/rotate", h.handleTLSRotate)
	protected.HandleFunc("/ui/partials/idp-add-form", h.partialIDPAddForm)
	protected.HandleFunc("/ui/partials/idp-edit-form", h.partialIDPEditForm)
	protected.HandleFunc("/ui/partials/idp-remove-form", h.partialIDPRemoveForm)
	protected.HandleFunc("/ui/api/idp/add", h.handleIDPAdd)
	protected.HandleFunc("/ui/api/idp/update", h.handleIDPUpdate)
	protected.HandleFunc("/ui/api/idp/remove", h.handleIDPRemove)
	protected.HandleFunc("/ui/api/idp/verify", h.handleIDPVerify)
	protected.HandleFunc("/ui/partials/peer-add-form", h.partialPeerAddForm)
	protected.HandleFunc("/ui/partials/peer-edit-form", h.partialPeerEditForm)
	protected.HandleFunc("/ui/partials/peer-remove-form", h.partialPeerRemoveForm)
	protected.HandleFunc("/ui/api/peers/add", h.handlePeerAdd)
	protected.HandleFunc("/ui/api/peers/update", h.handlePeerUpdate)
	protected.HandleFunc("/ui/api/peers/remove", h.handlePeerRemove)
	protected.HandleFunc("/ui/partials/pin-list", h.partialPinList)
	protected.HandleFunc("/ui/partials/pin-create-form", h.partialPinCreateForm)
	protected.HandleFunc("/ui/partials/pin-delete-form", h.partialPinDeleteForm)
	protected.HandleFunc("/ui/api/pins/create", h.handlePinCreate)
	protected.HandleFunc("/ui/api/pins/delete", h.handlePinDelete)
	protected.HandleFunc("/ui/partials/bulk-ops-form", h.partialBulkOpsForm)
	protected.HandleFunc("/ui/api/bulk/seed", h.handleBulkSeed)
	protected.HandleFunc("/ui/api/bulk/dump", h.handleBulkDump)
	protected.HandleFunc("/ui/api/bulk/import", h.handleBulkImport)
	protected.HandleFunc("/ui/api/bulk/replay", h.handleBulkReplay)
	protected.HandleFunc("/ui/events/activity", h.handleActivitySSE)

	mux.Handle("/ui/partials/", h.authMiddleware(protected))
	mux.Handle("/ui/", h.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protected.ServeHTTP(w, r)
	})))
}

func (h *Handler) handlePage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ui")
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	name := "home"
	nav := "home"
	title := "Dashboard"
	var data any

	switch {
	case path == "" || path == "index":
		// defaults
	case strings.HasPrefix(path, "workspaces"):
		nav = "workspaces"
		title = "Workspaces"
		name = "workspaces"
		wsPath := strings.TrimPrefix(path, "workspaces")
		wsPath = strings.TrimPrefix(wsPath, "/")
		if wsPath != "" {
			parts := strings.SplitN(wsPath, "/", 3)
			wsID := parts[0]
			if len(parts) >= 2 && parts[1] == "graph" {
				data = map[string]string{"wsID": wsID, "tab": "graph"}
				title = "Context Graph"
			} else if len(parts) >= 2 && parts[1] == "memories" {
				name = "memories"
				memID := ""
				if len(parts) == 3 && parts[2] != "" {
					memID = parts[2]
				}
				tab := r.URL.Query().Get("tab")
				if tab == "" {
					tab = "content"
				}
				data = map[string]string{"wsID": wsID, "memID": memID, "tab": tab}
				if memID != "" {
					title = "Memory " + truncateStr(memID, 12)
				} else {
					title = "Memories"
				}
			} else {
				tab := r.URL.Query().Get("tab")
				if tab == "" {
					tab = "overview"
				}
				data = map[string]string{"wsID": wsID, "tab": tab}
				title = "Workspace " + truncateStr(wsID, 12)
			}
		}
	case strings.HasPrefix(path, "federation"):
		name = "federation"
		nav = "federation"
		title = "Federation"
	case strings.HasPrefix(path, "audit"):
		name = "audit"
		nav = "audit"
		title = "Audit Log"
	case strings.HasPrefix(path, "settings"):
		name = "settings"
		nav = "settings"
		title = "Settings"
	default:
		http.NotFound(w, r)
		return
	}

	tmpl, ok := h.pages[name]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, pageData{Title: title, Nav: nav, Data: data}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) partialDashboardCards(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if h.data == nil {
		h.renderFallbackCards(w)
		return
	}
	stats, err := h.data.DashboardStats(r.Context())
	if err != nil {
		h.renderFallbackCards(w)
		return
	}
	fedLabel := fmt.Sprintf("%d / %d", stats.HealthyPeers, stats.FederationPeers)
	if stats.FederationPeers == 0 {
		fedLabel = "—"
	}
	healthBadge := `<span class="badge badge-ok">Ready</span>`
	if h.data != nil {
		if hi, err := h.data.HealthStatus(r.Context()); err == nil && !hi.OK {
			healthBadge = `<span class="badge badge-err">Degraded</span>`
		}
	}
	fmt.Fprintf(w, `<a class="card" href="/ui/settings?tab=diagnostics" style="text-decoration:none;color:inherit"><p class="card-title">Health</p><p class="card-value">%s</p></a>`, healthBadge)
	fmt.Fprintf(w, `<a class="card" href="/ui/workspaces" style="text-decoration:none;color:inherit"><p class="card-title">Workspaces</p><p class="card-value">%d</p></a>`, stats.WorkspaceCount)
	fmt.Fprintf(w, `<a class="card" href="/ui/workspaces" style="text-decoration:none;color:inherit"><p class="card-title">Memories</p><p class="card-value">%d</p></a>`, stats.MemoryCount)
	fmt.Fprintf(w, `<div class="card"><p class="card-title">Recall Ready</p><p class="card-value">%d%%</p></div>`, stats.RecallReadyPct)
	fmt.Fprintf(w, `<div class="card"><p class="card-title">Embed Queue</p><p class="card-value">%d</p></div>`, stats.EmbedQueueDepth)
	fmt.Fprintf(w, `<a class="card" href="/ui/federation" style="text-decoration:none;color:inherit"><p class="card-title">Federation Peers</p><p class="card-value">%s</p></a>`, template.HTMLEscapeString(fedLabel))
}

func (h *Handler) renderFallbackCards(w http.ResponseWriter) {
	fmt.Fprint(w, `<div class="card"><p class="card-title">Health</p><p class="card-value"><span class="badge badge-warn">Unknown</span></p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Workspaces</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Memories</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Recall Ready</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Embed Queue</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Federation Peers</p><p class="card-value">—</p></div>`)
}

func (h *Handler) partialRecentActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if h.data == nil {
		fmt.Fprint(w, `<p class="text-muted">No recent activity.</p>`)
		return
	}
	entries, err := h.data.RecentLedgerEntries(r.Context(), 20)
	if err != nil || len(entries) == 0 {
		fmt.Fprint(w, `<p class="text-muted">No recent activity.</p>`)
		return
	}
	fmt.Fprint(w, `<table><thead><tr><th>Time</th><th>Op</th><th>Target</th><th>Agent</th></tr></thead><tbody>`)
	for _, e := range entries {
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td></tr>`,
			template.HTMLEscapeString(e.Timestamp.UTC().Format("15:04:05")),
			template.HTMLEscapeString(e.Op),
			template.HTMLEscapeString(truncateStr(e.Target, 20)),
			template.HTMLEscapeString(truncateStr(e.AgentID, 20)),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func cellPreview(text string, n int) string {
	lines := strings.SplitN(text, "\n", 10)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "**") {
			continue
		}
		return truncateStr(line, n)
	}
	return truncateStr(strings.ReplaceAll(text, "\n", " "), n)
}

func (h *Handler) partialPlaceholder(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="empty-state"><h3>Coming Soon</h3><p>%s</p></div>`, template.HTMLEscapeString(msg))
	}
}
