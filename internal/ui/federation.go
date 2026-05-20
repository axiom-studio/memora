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
	fmt.Fprint(w, `<table><thead><tr><th>ID</th><th>Name</th><th>Endpoint</th><th>Trust Mode</th><th>Workspaces</th><th>Actions</th></tr></thead><tbody>`)
	for _, p := range f.Peers {
		wsLabel := "all"
		if len(p.Workspaces) > 0 {
			wsLabel = strings.Join(p.Workspaces, ", ")
		}
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td>%s</td><td class="mono">%s</td>`,
			template.HTMLEscapeString(truncateStr(p.ID, 20)),
			template.HTMLEscapeString(p.Name),
			template.HTMLEscapeString(p.Endpoint),
			template.HTMLEscapeString(p.TrustMode),
			template.HTMLEscapeString(truncateStr(wsLabel, 40)),
		)
		fmt.Fprintf(w, `<td><button class="btn btn-sm" hx-get="/ui/partials/peer-edit-form?id=%s" hx-target="#fed-modal-container" hx-swap="innerHTML">Edit</button> `,
			template.HTMLEscapeString(p.ID))
		fmt.Fprintf(w, `<button class="btn btn-sm btn-danger" hx-get="/ui/partials/peer-remove-form?id=%s" hx-target="#fed-modal-container" hx-swap="innerHTML">Remove</button></td></tr>`,
			template.HTMLEscapeString(p.ID))
	}
	fmt.Fprint(w, `</tbody></table></div>`)

	fmt.Fprint(w, `<div style="margin-bottom:1rem;display:flex;gap:0.5rem">`)
	fmt.Fprint(w, `<button class="btn btn-primary" hx-get="/ui/partials/peer-add-form" hx-target="#fed-modal-container" hx-swap="innerHTML">Add Peer</button>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `<div id="fed-modal-container"></div>`)

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

func (h *Handler) partialPeerAddForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var wsOptions string
	if h.data != nil {
		wss, _ := h.data.ListWorkspaces(r.Context())
		for _, ws := range wss {
			wsOptions += fmt.Sprintf(`<label style="display:flex;align-items:center;gap:0.5rem;font-weight:normal"><input type="checkbox" name="workspaces" value="%s"> %s</label>`,
				template.HTMLEscapeString(ws.ID), template.HTMLEscapeString(ws.Name))
		}
	}

	fmt.Fprintf(w, `<dialog id="peer-modal" class="modal" open>
<div class="modal-form">
  <h3>Add Federation Peer</h3>
  <div id="peer-add-result"></div>
  <form hx-post="/ui/api/peers/add" hx-target="#peer-add-result" hx-swap="innerHTML">
    <label>Name <input type="text" name="name" required placeholder="production-west"></label>
    <label>Endpoint <input type="url" name="endpoint" required placeholder="https://peer.example.com"></label>
    <label>Trust Mode
      <select name="trust_mode">
        <option value="mtls">mTLS</option>
        <option value="api_key">API Key</option>
      </select>
    </label>
    <details style="margin-bottom:0.75rem"><summary class="text-muted" style="cursor:pointer;font-size:0.85rem">Workspace Opt-In</summary>
      <p class="text-muted" style="font-size:0.85rem;margin:0.5rem 0">Leave all unchecked for full access.</p>
      %s
    </details>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">Add Peer</button>
    </div>
  </form>
</div>
</dialog>`, wsOptions)
}

func (h *Handler) handlePeerAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	endpoint := strings.TrimSpace(r.FormValue("endpoint"))
	trustMode := strings.TrimSpace(r.FormValue("trust_mode"))

	if name == "" || endpoint == "" {
		h.writeFormError(w, "Name and endpoint are required")
		return
	}

	_ = r.ParseForm()
	workspaces := r.Form["workspaces"]

	peer := PeerInfo{
		Name:       name,
		Endpoint:   endpoint,
		TrustMode:  trustMode,
		Workspaces: workspaces,
	}

	if err := h.data.AddPeer(r.Context(), peer); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/federation")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialPeerEditForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	peerID := r.URL.Query().Get("id")
	if peerID == "" {
		h.writeFormError(w, "Peer ID required")
		return
	}

	var peer *PeerInfo
	if h.federation != nil {
		for _, p := range h.federation.Peers {
			if p.ID == peerID {
				peer = &p
				break
			}
		}
	}
	if peer == nil {
		h.writeFormError(w, "Peer not found")
		return
	}

	peerWSSet := map[string]bool{}
	for _, ws := range peer.Workspaces {
		peerWSSet[ws] = true
	}

	var wsOptions string
	if h.data != nil {
		wss, _ := h.data.ListWorkspaces(r.Context())
		for _, ws := range wss {
			checked := ""
			if peerWSSet[ws.ID] {
				checked = " checked"
			}
			wsOptions += fmt.Sprintf(`<label style="display:flex;align-items:center;gap:0.5rem;font-weight:normal"><input type="checkbox" name="workspaces" value="%s"%s> %s</label>`,
				template.HTMLEscapeString(ws.ID), checked, template.HTMLEscapeString(ws.Name))
		}
	}

	mtlsSel := ""
	apiKeySel := ""
	if peer.TrustMode == "api_key" {
		apiKeySel = " selected"
	} else {
		mtlsSel = " selected"
	}

	fmt.Fprintf(w, `<dialog id="peer-modal" class="modal" open>
<div class="modal-form">
  <h3>Edit Peer: %s</h3>
  <div id="peer-edit-result"></div>
  <form hx-post="/ui/api/peers/update" hx-target="#peer-edit-result" hx-swap="innerHTML">
    <input type="hidden" name="id" value="%s">
    <label>Name <input type="text" name="name" required value="%s"></label>
    <label>Endpoint <input type="url" name="endpoint" required value="%s"></label>
    <label>Trust Mode
      <select name="trust_mode">
        <option value="mtls"%s>mTLS</option>
        <option value="api_key"%s>API Key</option>
      </select>
    </label>
    <details style="margin-bottom:0.75rem"><summary class="text-muted" style="cursor:pointer;font-size:0.85rem">Workspace Opt-In</summary>
      <p class="text-muted" style="font-size:0.85rem;margin:0.5rem 0">Leave all unchecked for full access.</p>
      %s
    </details>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">Update Peer</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(peer.Name),
		template.HTMLEscapeString(peer.ID),
		template.HTMLEscapeString(peer.Name),
		template.HTMLEscapeString(peer.Endpoint),
		mtlsSel, apiKeySel,
		wsOptions)
}

func (h *Handler) handlePeerUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	peerID := strings.TrimSpace(r.FormValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	endpoint := strings.TrimSpace(r.FormValue("endpoint"))
	trustMode := strings.TrimSpace(r.FormValue("trust_mode"))

	if peerID == "" || name == "" || endpoint == "" {
		h.writeFormError(w, "All fields are required")
		return
	}

	_ = r.ParseForm()
	workspaces := r.Form["workspaces"]

	peer := PeerInfo{
		ID:         peerID,
		Name:       name,
		Endpoint:   endpoint,
		TrustMode:  trustMode,
		Workspaces: workspaces,
	}

	if err := h.data.UpdatePeer(r.Context(), peerID, peer); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/federation")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialPeerRemoveForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	peerID := r.URL.Query().Get("id")
	if peerID == "" {
		h.writeFormError(w, "Peer ID required")
		return
	}

	var peerName string
	if h.federation != nil {
		for _, p := range h.federation.Peers {
			if p.ID == peerID {
				peerName = p.Name
				break
			}
		}
	}

	fmt.Fprintf(w, `<dialog id="peer-modal" class="modal" open>
<div class="modal-form">
  <h3>Remove Peer</h3>
  <div id="peer-remove-result"></div>
  <p>Are you sure you want to remove peer <strong>%s</strong> (%s)?</p>
  <p class="text-muted" style="font-size:0.85rem">This will stop all federation traffic with this peer. Memories already received will not be deleted.</p>
  <form hx-post="/ui/api/peers/remove" hx-target="#peer-remove-result" hx-swap="innerHTML">
    <input type="hidden" name="id" value="%s">
    <label>Type <code>%s</code> to confirm <input type="text" name="confirm" required></label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-danger">Remove Peer</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(peerName),
		template.HTMLEscapeString(peerID),
		template.HTMLEscapeString(peerID),
		template.HTMLEscapeString(peerName))
}

func (h *Handler) handlePeerRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	peerID := strings.TrimSpace(r.FormValue("id"))
	confirm := strings.TrimSpace(r.FormValue("confirm"))

	if peerID == "" {
		h.writeFormError(w, "Peer ID required")
		return
	}

	var expectedName string
	if h.federation != nil {
		for _, p := range h.federation.Peers {
			if p.ID == peerID {
				expectedName = p.Name
				break
			}
		}
	}

	if confirm != expectedName {
		h.writeFormError(w, fmt.Sprintf("Type %q to confirm", expectedName))
		return
	}

	if err := h.data.RemovePeer(r.Context(), peerID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/federation")
	w.WriteHeader(http.StatusOK)
}
