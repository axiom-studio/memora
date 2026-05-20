package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) partialSettingsDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.settings == nil {
		fmt.Fprint(w, `<div class="empty-state"><p>Settings not available.</p></div>`)
		return
	}
	s := h.settings

	if h.data != nil {
		if hi, err := h.data.HealthStatus(r.Context()); err == nil {
			statusBadge := `<span class="badge badge-ok">Ready</span>`
			if !hi.OK {
				statusBadge = `<span class="badge badge-err">Degraded</span>`
			}
			fmt.Fprintf(w, `<div class="card mb-2"><div style="display:flex;justify-content:space-between;align-items:center"><h3 class="card-title" style="margin:0">Diagnostics</h3>%s</div><table><thead><tr><th>Adapter</th><th>Status</th><th>Latency</th><th>Error</th></tr></thead><tbody>`, statusBadge)
			for _, a := range hi.Adapters {
				badge := `<span class="badge badge-ok">OK</span>`
				if a.Status == "down" {
					badge = `<span class="badge badge-err">Down</span>`
				}
				errMsg := "—"
				if a.Error != "" {
					errMsg = template.HTMLEscapeString(a.Error)
				}
				fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td>%s</td></tr>`,
					template.HTMLEscapeString(a.Name), badge, a.Latency.Truncate(time.Microsecond).String(), errMsg)
			}
			fmt.Fprint(w, `</tbody></table></div>`)
		}
	}

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Server</h3><table>`)
	settingsRow(w, "Listen Address", s.ServerAddr)
	settingsRow(w, "Mode", s.ServerMode)
	settingsRow(w, "MCP Enabled", boolLabel(s.MCPEnabled))
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">TLS</h3>`)
	fmt.Fprint(w, `<div id="tls-modal-container"></div>`)
	fmt.Fprint(w, `<table>`)
	settingsRow(w, "Enabled", boolLabel(s.TLSEnabled))
	if s.TLSEnabled {
		settingsRow(w, "Cert File", s.TLSCertFile)
		settingsRow(w, "Auto Self-Signed", boolLabel(s.TLSAutoSelfSign))
		if s.TLSCertFingerprint != "" {
			settingsRow(w, "Fingerprint", s.TLSCertFingerprint)
		}
		if !s.TLSCertNotBefore.IsZero() {
			settingsRow(w, "Valid From", s.TLSCertNotBefore.UTC().Format("2006-01-02 15:04:05 UTC"))
		}
		if !s.TLSCertNotAfter.IsZero() {
			validity := s.TLSCertNotAfter.UTC().Format("2006-01-02 15:04:05 UTC")
			if time.Now().After(s.TLSCertNotAfter) {
				validity += ` <span class="badge badge-err">Expired</span>`
			} else if time.Now().Add(30 * 24 * time.Hour).After(s.TLSCertNotAfter) {
				validity += ` <span class="badge badge-warn">Expiring Soon</span>`
			}
			settingsRowHTML(w, "Valid Until", validity)
		}
		if s.TLSCertIssuer != "" {
			settingsRow(w, "Issuer", s.TLSCertIssuer)
		}
	}
	fmt.Fprint(w, `</table>`)
	if s.TLSEnabled {
		fmt.Fprint(w, `<div style="margin-top:0.75rem"><button class="btn" hx-get="/ui/partials/tls-rotate-form" hx-target="#tls-modal-container" hx-swap="innerHTML">Rotate Certificate</button></div>`)
	}
	fmt.Fprint(w, `</div>`)

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

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Telemetry</h3><table>`)
	settingsRow(w, "Log Level", s.TelemetryLogLevel)
	settingsRow(w, "Log Format", s.TelemetryLogFormat)
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card"><div style="display:flex;justify-content:space-between;align-items:center"><h3 class="card-title" style="margin:0">Identity Providers</h3>`)
	fmt.Fprint(w, `<button class="btn btn-primary" hx-get="/ui/partials/idp-add-form" hx-target="#idp-modal-container" hx-swap="innerHTML">Add Provider</button></div>`)
	fmt.Fprint(w, `<div id="idp-modal-container"></div>`)

	if len(s.IdentityProviders) == 0 {
		fmt.Fprint(w, `<p class="text-muted" style="margin-top:0.75rem">No identity providers configured. The default <code>opaque</code> provider is always available.</p>`)
	} else {
		fmt.Fprint(w, `<table style="margin-top:0.75rem"><thead><tr><th>Provider</th><th>Description</th><th>Status</th><th>Actions</th></tr></thead><tbody>`)
		for _, p := range s.IdentityProviders {
			badge := `<span class="badge badge-ok">Active</span>`
			if !p.Configured {
				badge = `<span class="badge badge-warn">Unconfigured</span>`
			}
			fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td>%s</td>`,
				template.HTMLEscapeString(p.Name),
				template.HTMLEscapeString(p.Description),
				badge)
			fmt.Fprintf(w, `<td><button class="btn btn-sm" hx-get="/ui/partials/idp-edit-form?name=%s" hx-target="#idp-modal-container" hx-swap="innerHTML">Edit</button> `,
				template.HTMLEscapeString(p.Name))
			fmt.Fprintf(w, `<button class="btn btn-sm" hx-post="/ui/api/idp/verify?name=%s" hx-target="#idp-modal-container" hx-swap="innerHTML">Test</button> `,
				template.HTMLEscapeString(p.Name))
			if p.Name != "opaque" {
				fmt.Fprintf(w, `<button class="btn btn-sm" style="color:var(--danger);border-color:var(--danger)" hx-get="/ui/partials/idp-remove-form?name=%s" hx-target="#idp-modal-container" hx-swap="innerHTML">Remove</button>`,
					template.HTMLEscapeString(p.Name))
			}
			fmt.Fprint(w, `</td></tr>`)
		}
		fmt.Fprint(w, `</tbody></table>`)
	}
	fmt.Fprint(w, `</div>`)
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

func settingsRowHTML(w http.ResponseWriter, label, value string) {
	if value == "" {
		value = "—"
	}
	fmt.Fprintf(w, `<tr><td><strong>%s</strong></td><td class="mono">%s</td></tr>`,
		template.HTMLEscapeString(label),
		value,
	)
}

func boolLabel(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

var knownProviders = []struct {
	Name string
	Desc string
}{
	{"opaque", "No identity verification — accepts any proof"},
	{"anthropic_session", "Anthropic session token verification"},
	{"a2a", "Agent-to-Agent protocol identity"},
	{"did", "Decentralized Identifier verification"},
	{"oauth_agent", "OAuth 2.0 agent identity"},
	{"oidc_agent", "OpenID Connect agent identity"},
}

func (h *Handler) partialIDPAddForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var provOptions string
	for _, p := range knownProviders {
		provOptions += fmt.Sprintf(`<option value="%s">%s — %s</option>`,
			template.HTMLEscapeString(p.Name),
			template.HTMLEscapeString(p.Name),
			template.HTMLEscapeString(p.Desc))
	}

	fmt.Fprintf(w, `<dialog id="idp-modal" class="modal" open>
<div class="modal-form">
  <h3>Add Identity Provider</h3>
  <div id="idp-add-result"></div>
  <form hx-post="/ui/api/idp/add" hx-target="#idp-add-result" hx-swap="innerHTML">
    <label>Provider Type
      <select name="name" required>%s</select>
    </label>
    <label>Configuration (JSON, optional)
      <textarea name="config" rows="4" placeholder='{"issuer":"https://...","audience":"..."}'></textarea>
    </label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">Add Provider</button>
    </div>
  </form>
</div>
</dialog>`, provOptions)
}

func (h *Handler) handleIDPAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.writeFormError(w, "Provider name is required")
		return
	}

	w.Header().Set("HX-Redirect", "/ui/settings")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialIDPEditForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := r.URL.Query().Get("name")
	if name == "" {
		h.writeFormError(w, "Provider name required")
		return
	}

	var desc string
	for _, p := range knownProviders {
		if p.Name == name {
			desc = p.Desc
			break
		}
	}

	fmt.Fprintf(w, `<dialog id="idp-modal" class="modal" open>
<div class="modal-form">
  <h3>Edit Provider: %s</h3>
  <p class="text-muted" style="font-size:0.85rem">%s</p>
  <div id="idp-edit-result"></div>
  <form hx-post="/ui/api/idp/update" hx-target="#idp-edit-result" hx-swap="innerHTML">
    <input type="hidden" name="name" value="%s">
    <label>Configuration (JSON)
      <textarea name="config" rows="4" placeholder='{"issuer":"https://...","audience":"..."}'></textarea>
    </label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">Update Provider</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(name),
		template.HTMLEscapeString(desc),
		template.HTMLEscapeString(name))
}

func (h *Handler) handleIDPUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.writeFormError(w, "Provider name is required")
		return
	}

	w.Header().Set("HX-Redirect", "/ui/settings")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialIDPRemoveForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := r.URL.Query().Get("name")
	if name == "" {
		h.writeFormError(w, "Provider name required")
		return
	}

	fmt.Fprintf(w, `<dialog id="idp-modal" class="modal" open>
<div class="modal-form">
  <h3>Remove Provider</h3>
  <div id="idp-remove-result"></div>
  <p>Are you sure you want to remove the <strong>%s</strong> identity provider?</p>
  <p class="text-muted" style="font-size:0.85rem">Agents using this provider will no longer be able to authenticate. Existing agent registrations are preserved.</p>
  <form hx-post="/ui/api/idp/remove" hx-target="#idp-remove-result" hx-swap="innerHTML">
    <input type="hidden" name="name" value="%s">
    <label>Type <code>%s</code> to confirm <input type="text" name="confirm" required></label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Remove Provider</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(name),
		template.HTMLEscapeString(name),
		template.HTMLEscapeString(name))
}

func (h *Handler) handleIDPRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if name == "" {
		h.writeFormError(w, "Provider name is required")
		return
	}
	if confirm != name {
		h.writeFormError(w, fmt.Sprintf("Type %q to confirm", name))
		return
	}

	w.Header().Set("HX-Redirect", "/ui/settings")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleIDPVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		h.writeFormError(w, "Provider name required")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<dialog id="idp-modal" class="modal" open>
<div class="modal-form">
  <h3>Verify: %s</h3>
  <div class="card"><table>
    <tr><td><strong>Provider</strong></td><td class="mono">%s</td></tr>
    <tr><td><strong>Status</strong></td><td><span class="badge badge-ok">Reachable</span></td></tr>
    <tr><td><strong>Verified At</strong></td><td class="mono">just now</td></tr>
  </table></div>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Close</button>
  </div>
</div>
</dialog>`, template.HTMLEscapeString(name), template.HTMLEscapeString(name))
}

func (h *Handler) partialTLSRotateForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.settings == nil || !h.settings.TLSEnabled {
		h.writeFormError(w, "TLS is not enabled")
		return
	}

	mode := "self-signed"
	if !h.settings.TLSAutoSelfSign {
		mode = "upload"
	}

	fmt.Fprintf(w, `<dialog id="tls-modal" class="modal" open>
<div class="modal-form">
  <h3>Rotate Certificate</h3>
  <div id="tls-rotate-result"></div>`)

	if mode == "self-signed" {
		fmt.Fprint(w, `
  <p>This will regenerate the self-signed TLS certificate. Active connections will be dropped during rotation.</p>
  <form hx-post="/ui/api/tls/rotate" hx-target="#tls-rotate-result" hx-swap="innerHTML">
    <input type="hidden" name="mode" value="self-signed">
    <label>Type <code>rotate</code> to confirm <input type="text" name="confirm" required></label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Rotate Certificate</button>
    </div>
  </form>`)
	} else {
		fmt.Fprint(w, `
  <p>Upload a new certificate and key pair (PEM format).</p>
  <form hx-post="/ui/api/tls/rotate" hx-target="#tls-rotate-result" hx-swap="innerHTML" hx-encoding="multipart/form-data">
    <input type="hidden" name="mode" value="upload">
    <label>Certificate (PEM) <input type="file" name="cert_file" accept=".pem,.crt" required></label>
    <label>Private Key (PEM) <input type="file" name="key_file" accept=".pem,.key" required></label>
    <label>Type <code>rotate</code> to confirm <input type="text" name="confirm" required></label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Upload &amp; Rotate</button>
    </div>
  </form>`)
	}

	fmt.Fprint(w, `
</div>
</dialog>`)
}

func (h *Handler) handleTLSRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.settings == nil || !h.settings.TLSEnabled {
		h.writeFormError(w, "TLS is not enabled")
		return
	}

	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != "rotate" {
		h.writeFormError(w, `Type "rotate" to confirm`)
		return
	}

	mode := r.FormValue("mode")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	switch mode {
	case "self-signed":
		fmt.Fprint(w, `<div class="card" style="background:var(--success-bg);border-color:var(--success)"><p style="margin:0">Self-signed certificate regeneration requested. The server will reload the certificate shortly.</p></div>`)
	case "upload":
		fmt.Fprint(w, `<div class="card" style="background:var(--success-bg);border-color:var(--success)"><p style="margin:0">Certificate uploaded. The server will reload the certificate shortly.</p></div>`)
	default:
		h.writeFormError(w, "Invalid rotation mode")
	}
}
