package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) partialFederationStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.federation == nil {
		fmt.Fprint(w, `<div class="empty-state"><h3>Federation Disabled</h3><p>Federation is not configured for this instance.</p></div>`)
		return
	}
	f := h.federation

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Federation Overview</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Federation ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(f.FederationID))
	fmt.Fprintf(w, `<tr><td><strong>Configured Peers</strong></td><td>%d</td></tr>`, len(f.Peers))
	fmt.Fprint(w, `</table></div>`)

	if len(f.Peers) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Peers</h3><p>No federation peers are configured.</p></div>`)
		return
	}

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Peers</h3>`)
	fmt.Fprint(w, `<table><thead><tr><th>ID</th><th>Name</th><th>Endpoint</th><th>Trust Mode</th><th>Workspaces</th></tr></thead><tbody>`)
	for _, p := range f.Peers {
		wsLabel := "all"
		if len(p.Workspaces) > 0 {
			wsLabel = strings.Join(p.Workspaces, ", ")
		}
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td>%s</td><td class="mono">%s</td></tr>`,
			template.HTMLEscapeString(truncateStr(p.ID, 20)),
			template.HTMLEscapeString(p.Name),
			template.HTMLEscapeString(p.Endpoint),
			template.HTMLEscapeString(p.TrustMode),
			template.HTMLEscapeString(truncateStr(wsLabel, 40)),
		)
	}
	fmt.Fprint(w, `</tbody></table></div>`)

	if h.data != nil {
		entries, err := h.data.LedgerEntriesSince(r.Context(), time.Time{}, []string{"federation_recall", "federation_inbound"}, 100)
		if err == nil && len(entries) > 0 {
			fmt.Fprint(w, `<div class="card"><h3 class="card-title">Recent Federation Activity</h3>`)
			fmt.Fprint(w, `<table><thead><tr><th>Time</th><th>Op</th><th>Target</th><th>Agent</th></tr></thead><tbody>`)
			for _, e := range entries {
				fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td></tr>`,
					template.HTMLEscapeString(e.Timestamp.UTC().Format("2006-01-02 15:04:05")),
					template.HTMLEscapeString(e.Op),
					template.HTMLEscapeString(truncateStr(e.Target, 24)),
					template.HTMLEscapeString(truncateStr(e.AgentID, 20)),
				)
			}
			fmt.Fprint(w, `</tbody></table></div>`)
		}
	}
}
