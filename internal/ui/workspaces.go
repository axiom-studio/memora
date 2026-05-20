package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handler) partialWorkspaceList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if h.data == nil {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Data</h3><p>Data source not configured.</p></div>`)
		return
	}
	workspaces, err := h.data.ListWorkspaces(r.Context())
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error loading workspaces: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	fmt.Fprint(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/workspace-create-form" hx-target="#modal-container" hx-swap="innerHTML">Create Workspace</button></div>`)
	fmt.Fprint(w, `<div id="modal-container"></div>`)

	if len(workspaces) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Workspaces</h3><p>Click "Create Workspace" above to get started.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>ID</th><th>Name</th><th>Embedding Model</th><th>Agents</th><th>Memories</th><th>Created</th></tr></thead><tbody>`)
	for _, ws := range workspaces {
		fmt.Fprintf(w, `<tr><td class="mono"><a href="/ui/workspaces/%s">%s</a></td><td>%s</td><td class="mono">%s</td><td class="text-right">%d</td><td class="text-right">%d</td><td>%s</td></tr>`,
			template.HTMLEscapeString(ws.ID),
			template.HTMLEscapeString(truncateStr(ws.ID, 16)),
			template.HTMLEscapeString(ws.Name),
			template.HTMLEscapeString(orDash(ws.EmbeddingModel)),
			ws.AgentCount,
			ws.MemoryCount,
			template.HTMLEscapeString(ws.CreatedAt.UTC().Format("2006-01-02")),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) handleWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	wsID := strings.TrimPrefix(r.URL.Path, "/ui/workspaces/")
	wsID = strings.TrimSuffix(wsID, "/")
	if i := strings.IndexByte(wsID, '/'); i >= 0 {
		wsID = wsID[:i]
	}

	if wsID == "" {
		http.NotFound(w, r)
		return
	}

	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}

	tmpl, ok := h.pages["workspaces"]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, pageData{
		Title: "Workspace " + truncateStr(wsID, 12),
		Nav:   "workspaces",
		Data:  map[string]string{"wsID": wsID, "tab": tab},
	})
}

func (h *Handler) partialWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>Workspace not found.</p></div>`)
		return
	}

	switch tab {
	case "overview":
		h.renderWorkspaceOverview(w, r, wsID)
	case "agents":
		h.renderWorkspaceAgents(w, r, wsID)
	case "collections":
		h.renderWorkspaceCollections(w, r, wsID)
	case "config":
		h.renderWorkspaceConfig(w, r, wsID)
	default:
		h.renderWorkspaceOverview(w, r, wsID)
	}
}

func (h *Handler) renderWorkspaceOverview(w http.ResponseWriter, r *http.Request, wsID string) {
	ws, err := h.data.GetWorkspace(r.Context(), wsID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div class="card"><div style="display:flex;justify-content:space-between;align-items:center"><h3 class="card-title" style="margin:0">Workspace Details</h3><div style="display:flex;gap:0.5rem">`)
	fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/workspace-edit-form?id=%s" hx-target="#modal-container" hx-swap="innerHTML">Edit</button>`, template.HTMLEscapeString(ws.ID))
	fmt.Fprintf(w, `<button class="btn" style="color:var(--danger);border-color:var(--danger)" hx-get="/ui/partials/workspace-delete-form?id=%s" hx-target="#modal-container" hx-swap="innerHTML">Delete</button>`, template.HTMLEscapeString(ws.ID))
	fmt.Fprintf(w, `</div></div>`)
	fmt.Fprintf(w, `<div id="modal-container"></div>`)
	fmt.Fprintf(w, `<table>`)
	fmt.Fprintf(w, `<tr><td><strong>ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(ws.ID))
	fmt.Fprintf(w, `<tr><td><strong>Name</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(ws.Name))
	fmt.Fprintf(w, `<tr><td><strong>Region</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(orDash(ws.Region)))
	fmt.Fprintf(w, `<tr><td><strong>Embedding Model</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(orDash(ws.EmbeddingModel)))
	fmt.Fprintf(w, `<tr><td><strong>Chunker</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(orDash(ws.ChunkerID)))
	fmt.Fprintf(w, `<tr><td><strong>Auto-Link</strong></td><td>%s</td></tr>`, boolBadge(ws.AutoLinkEnabled))
	fmt.Fprintf(w, `<tr><td><strong>Agents</strong></td><td>%d</td></tr>`, ws.AgentCount)
	fmt.Fprintf(w, `<tr><td><strong>Memories</strong></td><td>%d</td></tr>`, ws.MemoryCount)
	fmt.Fprintf(w, `<tr><td><strong>Created</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(ws.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC")))
	fmt.Fprintf(w, `</table></div>`)
}

func (h *Handler) renderWorkspaceAgents(w http.ResponseWriter, r *http.Request, wsID string) {
	agents, err := h.data.ListAgents(r.Context(), wsID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/agent-register-form?ws=%s" hx-target="#agent-modal-container" hx-swap="innerHTML">Register Agent</button></div>`, template.HTMLEscapeString(wsID))
	fmt.Fprint(w, `<div id="agent-modal-container"></div>`)

	if len(agents) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Agents</h3><p>Click "Register Agent" above to get started.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Agent ID</th><th>Display Name</th><th>Provider</th><th>Type</th><th>Model</th><th>Status</th><th></th></tr></thead><tbody>`)
	for _, a := range agents {
		status := `<span class="badge badge-ok">Active</span>`
		if a.Deactivated {
			status = `<span class="badge badge-err">Deactivated</span>`
		}
		var actionBtn string
		if !a.Deactivated {
			actionBtn = fmt.Sprintf(`<button class="btn" style="color:var(--danger);border-color:var(--danger);padding:0.25rem 0.5rem;font-size:0.85rem" hx-get="/ui/partials/agent-deactivate-form?ws=%s&amp;id=%s" hx-target="#agent-modal-container" hx-swap="innerHTML">Deactivate</button>`,
				template.HTMLEscapeString(wsID), template.HTMLEscapeString(a.AgentID))
		}
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td>%s</td><td>%s</td><td class="mono">%s</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(a.AgentID),
			template.HTMLEscapeString(orDash(a.DisplayName)),
			template.HTMLEscapeString(orDash(a.IdentityProvider)),
			template.HTMLEscapeString(orDash(a.AgentType)),
			template.HTMLEscapeString(orDash(a.Model)),
			status,
			actionBtn,
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) renderWorkspaceCollections(w http.ResponseWriter, r *http.Request, wsID string) {
	colls, err := h.data.ListCollections(r.Context(), wsID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/collection-create-form?ws=%s" hx-target="#coll-modal-container" hx-swap="innerHTML">Create Collection</button></div>`, template.HTMLEscapeString(wsID))
	fmt.Fprint(w, `<div id="coll-modal-container"></div>`)

	if len(colls) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Collections</h3><p>Click "Create Collection" above to get started.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>ID</th><th>Name</th><th>Memories</th><th>Created</th><th></th></tr></thead><tbody>`)
	for _, c := range colls {
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="text-right">%d</td><td>%s</td><td><button class="btn" style="color:var(--danger);border-color:var(--danger);padding:0.25rem 0.5rem;font-size:0.85rem" hx-get="/ui/partials/collection-delete-form?id=%s&amp;ws=%s" hx-target="#coll-modal-container" hx-swap="innerHTML">Delete</button></td></tr>`,
			template.HTMLEscapeString(c.ID),
			template.HTMLEscapeString(c.Name),
			c.MemoryCount,
			template.HTMLEscapeString(c.CreatedAt.UTC().Format("2006-01-02")),
			template.HTMLEscapeString(c.ID),
			template.HTMLEscapeString(wsID),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (h *Handler) renderWorkspaceConfig(w http.ResponseWriter, r *http.Request, wsID string) {
	ws, err := h.data.GetWorkspace(r.Context(), wsID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	threshold := ws.AutoLinkThreshold
	if threshold <= 0 {
		threshold = 0.7
	}
	maxEdges := ws.AutoLinkMaxEdges
	if maxEdges <= 0 {
		maxEdges = 10
	}
	maxIncoming := ws.AutoLinkMaxIncomingPerDay
	if maxIncoming <= 0 {
		maxIncoming = 100
	}

	enabledChecked := ""
	if ws.AutoLinkEnabled {
		enabledChecked = " checked"
	}

	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Auto-Link Configuration</h3>
<div id="config-result"></div>
<form hx-post="/ui/api/workspaces/config" hx-target="#config-result" hx-swap="innerHTML">
  <input type="hidden" name="id" value="%s">
  <label style="display:flex;align-items:center;gap:0.5rem">
    <input type="checkbox" name="auto_link_enabled" value="true"%s> Enable auto-linking
  </label>
  <label>Cosine Similarity Threshold
    <div style="display:flex;align-items:center;gap:0.75rem">
      <input type="range" name="auto_link_threshold" min="0" max="1" step="0.05" value="%.2f" oninput="document.getElementById('threshold-val').textContent=this.value" style="flex:1">
      <span id="threshold-val" class="mono" style="min-width:3ch">%.2f</span>
    </div>
  </label>
  <label>Max Edges per Imprint <input type="number" name="auto_link_max_edges" value="%d" min="1" max="50"></label>
  <label>Incoming Throttle (per 24h) <input type="number" name="auto_link_max_incoming" value="%d" min="1" max="10000"></label>
  <div class="modal-actions" style="margin-top:1rem">
    <button type="submit" class="btn btn-primary">Save Configuration</button>
  </div>
</form>
</div>`,
		template.HTMLEscapeString(wsID),
		enabledChecked,
		threshold, threshold,
		maxEdges,
		maxIncoming)
}

func (h *Handler) handleWorkspaceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("id"))
	if wsID == "" {
		h.writeFormError(w, "Workspace ID is required")
		return
	}

	ws, err := h.data.GetWorkspace(r.Context(), wsID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	autoLinkEnabled := r.FormValue("auto_link_enabled") == "true"
	threshold, _ := strconv.ParseFloat(r.FormValue("auto_link_threshold"), 64)
	maxEdges, _ := strconv.Atoi(r.FormValue("auto_link_max_edges"))
	maxIncoming, _ := strconv.Atoi(r.FormValue("auto_link_max_incoming"))

	if threshold < 0 || threshold > 1 {
		threshold = 0.7
	}
	if maxEdges < 1 || maxEdges > 50 {
		maxEdges = 10
	}
	if maxIncoming < 1 {
		maxIncoming = 100
	}

	input := UpdateWorkspaceInput{
		Name:                      ws.Name,
		Region:                    ws.Region,
		ChunkerID:                 ws.ChunkerID,
		EmbeddingModel:            ws.EmbeddingModel,
		AutoLinkEnabled:           autoLinkEnabled,
		AutoLinkThreshold:         threshold,
		AutoLinkMaxEdges:          maxEdges,
		AutoLinkMaxIncomingPerDay: maxIncoming,
	}

	if err := h.data.UpdateWorkspace(r.Context(), wsID, input); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<div class="card" style="background:var(--success-bg);border-color:var(--success)"><p style="margin:0">Configuration saved.</p></div>`)
}

func boolBadge(v bool) string {
	if v {
		return `<span class="badge badge-ok">Enabled</span>`
	}
	return `<span class="badge badge-warn">Disabled</span>`
}
