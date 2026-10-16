package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

func (h *Handler) partialSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || q == "" {
		fmt.Fprint(w, `<p class="text-muted">Type to search workspaces, agents, and memories.</p>`)
		return
	}

	var results []searchResult

	workspaces, _ := h.data.ListWorkspaces(r.Context())
	for _, ws := range workspaces {
		if containsFold(ws.ID, q) || containsFold(ws.Name, q) {
			results = append(results, searchResult{
				Type:  "Workspace",
				Label: ws.Name,
				ID:    ws.ID,
				URL:   "/ui/workspaces/" + ws.ID,
			})
		}
	}

	for _, ws := range workspaces {
		agents, _ := h.data.ListAgents(r.Context(), ws.ID)
		for _, a := range agents {
			if containsFold(a.AgentID, q) || containsFold(a.DisplayName, q) {
				results = append(results, searchResult{
					Type:  "Agent",
					Label: a.DisplayName,
					ID:    a.AgentID,
					URL:   "/ui/workspaces/" + ws.ID + "?tab=agents",
				})
			}
		}

		mems, _ := h.data.ListMemories(r.Context(), ws.ID, 200)
		for _, m := range mems {
			if containsFold(m.ID, q) || containsFold(m.Content, q) {
				results = append(results, searchResult{
					Type:  "Memory",
					Label: truncateStr(m.Content, 60),
					ID:    m.ID,
					URL:   "/ui/workspaces/" + ws.ID + "/memories/" + m.ID,
				})
			}
		}
	}

	if len(results) == 0 {
		fmt.Fprintf(w, `<p class="text-muted">No results for "%s".</p>`, template.HTMLEscapeString(q))
		return
	}

	if len(results) > 20 {
		results = results[:20]
	}

	currentType := ""
	for _, r := range results {
		if r.Type != currentType {
			if currentType != "" {
				fmt.Fprint(w, `</div>`)
			}
			fmt.Fprintf(w, `<div class="search-group"><p class="search-group-label">%s</p>`, template.HTMLEscapeString(r.Type))
			currentType = r.Type
		}
		fmt.Fprintf(w, `<a href="%s" class="search-result"><span class="mono">%s</span> <span class="text-muted">%s</span></a>`,
			template.HTMLEscapeString(r.URL),
			template.HTMLEscapeString(truncateStr(r.ID, 20)),
			template.HTMLEscapeString(r.Label),
		)
	}
	if currentType != "" {
		fmt.Fprint(w, `</div>`)
	}
}

type searchResult struct {
	Type  string
	Label string
	ID    string
	URL   string
}

func (h *Handler) partialMemorySuggest(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" || len(q) < 2 {
		return
	}

	mems, _ := h.data.ListMemories(r.Context(), wsID, 200)
	n := 0
	for _, m := range mems {
		if n >= 10 {
			break
		}
		if containsFold(m.ID, q) || containsFold(m.Content, q) {
			label := m.ID + " — " + truncateStr(m.Content, 40)
			fmt.Fprintf(w, `<option value="%s">%s</option>`,
				template.HTMLEscapeString(m.ID),
				template.HTMLEscapeString(label))
			n++
		}
	}
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
