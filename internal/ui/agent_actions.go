package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

var identityProviders = []struct{ Value, Label string }{
	{"opaque", "opaque"},
	{"anthropic_session", "anthropic_session"},
	{"a2a", "a2a"},
	{"did", "did"},
	{"oauth_agent", "oauth_agent"},
	{"oidc_agent", "oidc_agent"},
}

func (h *Handler) partialAgentRegisterForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	if wsID == "" {
		h.writeFormError(w, "Missing workspace ID")
		return
	}

	var providerOpts string
	for _, p := range identityProviders {
		sel := ""
		if p.Value == "opaque" {
			sel = " selected"
		}
		providerOpts += fmt.Sprintf(`<option value="%s"%s>%s</option>`, p.Value, sel, p.Label)
	}

	fmt.Fprintf(w, `<dialog id="agent-register-modal" class="modal" open>
<form hx-post="/ui/api/agents/register" hx-target="#agent-result" hx-swap="innerHTML" class="modal-form">
  <h3>Register Agent</h3>
  <div id="agent-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <label>Agent ID <span class="text-muted">(must start with agent_)</span>
    <input type="text" name="agent_id" placeholder="agent_my_bot" required pattern="agent_.*">
  </label>
  <label>Display Name
    <input type="text" name="display_name" placeholder="My Bot">
  </label>
  <label>Identity Provider
    <select name="identity_provider" required>%s</select>
  </label>
  <label>Agent Type
    <input type="text" name="agent_type" placeholder="e.g. assistant, tool, pipeline">
  </label>
  <label>Model
    <input type="text" name="model" placeholder="e.g. claude-sonnet-4-6">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Register</button>
  </div>
</form>
</dialog>`, template.HTMLEscapeString(wsID), providerOpts)
}

func (h *Handler) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	provider := strings.TrimSpace(r.FormValue("identity_provider"))
	agentType := strings.TrimSpace(r.FormValue("agent_type"))
	model := strings.TrimSpace(r.FormValue("model"))

	if wsID == "" || agentID == "" {
		h.writeFormError(w, "Workspace ID and Agent ID are required")
		return
	}
	if !strings.HasPrefix(agentID, "agent_") {
		h.writeFormError(w, "Agent ID must start with agent_")
		return
	}
	if provider == "" {
		provider = "opaque"
	}

	err := h.data.RegisterAgent(r.Context(), wsID, RegisterAgentInput{
		AgentID:          agentID,
		DisplayName:      displayName,
		IdentityProvider: provider,
		AgentType:        agentType,
		Model:            model,
	})
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/ui/workspaces/%s?tab=agents", wsID))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialAgentDeactivateForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	agentID := r.URL.Query().Get("id")
	if wsID == "" || agentID == "" {
		h.writeFormError(w, "Missing workspace or agent ID")
		return
	}

	fmt.Fprintf(w, `<dialog id="agent-deactivate-modal" class="modal" open>
<form hx-post="/ui/api/agents/deactivate" hx-target="#agent-deact-result" hx-swap="innerHTML" class="modal-form">
  <h3>Deactivate Agent</h3>
  <div id="agent-deact-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="agent_id" value="%s">
  <p>Deactivating <strong class="mono">%s</strong> prevents it from writing new memories. Existing memories and ledger entries are preserved.</p>
  <label>Type the agent ID to confirm
    <input type="text" name="confirm" placeholder="%s" required>
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Deactivate</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(agentID),
		template.HTMLEscapeString(agentID),
		template.HTMLEscapeString(agentID))
}

func (h *Handler) handleAgentDeactivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	confirm := strings.TrimSpace(r.FormValue("confirm"))

	if wsID == "" || agentID == "" {
		h.writeFormError(w, "Missing workspace or agent ID")
		return
	}
	if confirm != agentID {
		h.writeFormError(w, "Confirmation does not match agent ID")
		return
	}

	if err := h.data.DeactivateAgent(r.Context(), wsID, agentID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/ui/workspaces/%s?tab=agents", wsID))
	w.WriteHeader(http.StatusOK)
}
