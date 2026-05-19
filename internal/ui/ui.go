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
}

func NewHandler() (*Handler, error) {
	layoutBytes, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	layoutSrc := string(layoutBytes)

	pageNames := []string{"home", "workspaces", "federation", "audit", "settings"}
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
	protected.HandleFunc("/ui/partials/workspace-list", h.partialPlaceholder("Workspaces will load here."))
	protected.HandleFunc("/ui/partials/federation-status", h.partialPlaceholder("Federation status will load here."))
	protected.HandleFunc("/ui/partials/audit-list", h.partialPlaceholder("Audit log will load here."))
	protected.HandleFunc("/ui/partials/settings-detail", h.partialPlaceholder("Settings will load here."))

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

	switch {
	case path == "" || path == "index":
		// defaults
	case strings.HasPrefix(path, "workspaces"):
		name = "workspaces"
		nav = "workspaces"
		title = "Workspaces"
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
	if err := tmpl.Execute(w, pageData{Title: title, Nav: nav}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) partialDashboardCards(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<div class="card"><p class="card-title">Workspaces</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Memories</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Recall Ready</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Embed Queue</p><p class="card-value">—</p></div>`)
	fmt.Fprint(w, `<div class="card"><p class="card-title">Federation Peers</p><p class="card-value">—</p></div>`)
}

func (h *Handler) partialRecentActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<p class="text-muted">No recent activity.</p>`)
}

func (h *Handler) partialPlaceholder(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="empty-state"><h3>Coming Soon</h3><p>%s</p></div>`, template.HTMLEscapeString(msg))
	}
}
