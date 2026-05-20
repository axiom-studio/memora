package ui

import (
	"fmt"
	"html/template"
	"net/http"
)

func (h *Handler) partialSettingsDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.settings == nil {
		fmt.Fprint(w, `<div class="empty-state"><p>Settings not available.</p></div>`)
		return
	}
	s := h.settings

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Server</h3><table>`)
	settingsRow(w, "Listen Address", s.ServerAddr)
	settingsRow(w, "Mode", s.ServerMode)
	settingsRow(w, "MCP Enabled", boolLabel(s.MCPEnabled))
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">TLS</h3><table>`)
	settingsRow(w, "Enabled", boolLabel(s.TLSEnabled))
	if s.TLSEnabled {
		settingsRow(w, "Cert File", s.TLSCertFile)
		settingsRow(w, "Auto Self-Signed", boolLabel(s.TLSAutoSelfSign))
	}
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Storage</h3><table>`)
	settingsRow(w, "Data Directory", s.DataDir)
	settingsRow(w, "Metadata Driver", s.MetadataDriver)
	settingsRow(w, "Vector Driver", s.VectorDriver)
	settingsRow(w, "Ledger Driver", s.LedgerDriver)
	settingsRow(w, "Graph Driver", s.GraphDriver)
	settingsRow(w, "Content Driver", s.ContentDriver)
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Embedding</h3><table>`)
	settingsRow(w, "Model", s.EmbeddingModel)
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Federation</h3><table>`)
	settingsRow(w, "Enabled", boolLabel(s.FederationEnabled))
	if s.FederationEnabled {
		settingsRow(w, "Federation ID", s.FederationID)
		settingsRow(w, "Peer Count", fmt.Sprintf("%d", s.PeerCount))
	}
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card"><h3 class="card-title">Telemetry</h3><table>`)
	settingsRow(w, "Log Level", s.TelemetryLogLevel)
	settingsRow(w, "Log Format", s.TelemetryLogFormat)
	fmt.Fprint(w, `</table></div>`)
}

func settingsRow(w http.ResponseWriter, label, value string) {
	if value == "" {
		value = "—"
	}
	fmt.Fprintf(w, `<tr><td><strong>%s</strong></td><td class="mono">%s</td></tr>`,
		template.HTMLEscapeString(label),
		template.HTMLEscapeString(value),
	)
}

func boolLabel(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}
