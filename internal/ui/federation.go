package ui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// PeerManager is the runtime federation peer registry the console edits
// (federation.Manager). Keys are write-only: List never returns them.
type PeerManager interface {
	List(ctx context.Context) ([]api.PeerView, error)
	Upsert(ctx context.Context, req api.PeerRequest) (api.PeerView, error)
	Delete(ctx context.Context, nodeID string) error
}

// SetPeers wires the console's peer pages to the runtime registry.
func (h *Handler) SetPeers(m PeerManager) { h.peers = m }

// listPeers returns the registry's peers, or nil when there is none.
func (h *Handler) listPeers(ctx context.Context) []api.PeerView {
	if h.peers == nil {
		return nil
	}
	peers, _ := h.peers.List(ctx)
	return peers
}

func (h *Handler) findPeer(ctx context.Context, nodeID string) *api.PeerView {
	for _, p := range h.listPeers(ctx) {
		if p.NodeID == nodeID {
			return &p
		}
	}
	return nil
}

func (h *Handler) partialFederationStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.federation == nil || h.peers == nil {
		fmt.Fprint(w, `<div class="empty-state"><h3>Federation Disabled</h3><p>Federation is not configured for this instance.</p></div>`)
		return
	}
	peers := h.listPeers(r.Context())

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Federation Overview</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Federation ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(h.federation.FederationID))
	fmt.Fprintf(w, `<tr><td><strong>Peers</strong></td><td>%d</td></tr>`, len(peers))
	fmt.Fprint(w, `</table></div>`)

	if len(peers) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Peers</h3><p>No federation peers are registered.</p><button class="btn btn-primary" hx-get="/ui/partials/peer-add-form" hx-target="#fed-modal-container" hx-swap="innerHTML">Add Peer</button></div><div id="fed-modal-container"></div>`)
		return
	}

	fmt.Fprint(w, `<div class="card mb-2"><h3 class="card-title">Peers</h3>`)
	fmt.Fprint(w, `<table><thead><tr><th>Node ID</th><th>Name</th><th>Endpoint</th><th>Trust Mode</th><th>Keys</th><th>Status</th><th>Actions</th></tr></thead><tbody>`)
	for _, p := range peers {
		var keys []string
		if p.HasInboundKey {
			keys = append(keys, "inbound")
		}
		if p.HasAPIKey {
			keys = append(keys, "outbound")
		}
		if len(keys) == 0 {
			keys = []string{"none"}
		}
		status := `<span class="badge badge-ok">active</span>`
		if p.Revoked {
			status = `<span class="badge badge-err">revoked</span>`
		}
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td>%s</td><td>%s</td><td>%s</td>`,
			template.HTMLEscapeString(p.NodeID),
			template.HTMLEscapeString(p.Name),
			template.HTMLEscapeString(p.URL),
			template.HTMLEscapeString(p.TrustMode),
			template.HTMLEscapeString(strings.Join(keys, ", ")),
			status,
		)
		fmt.Fprintf(w, `<td><button class="btn btn-sm" hx-get="/ui/partials/peer-edit-form?id=%s" hx-target="#fed-modal-container" hx-swap="innerHTML">Edit</button> `,
			template.HTMLEscapeString(p.NodeID))
		fmt.Fprintf(w, `<button class="btn btn-sm btn-danger" hx-get="/ui/partials/peer-remove-form?id=%s" hx-target="#fed-modal-container" hx-swap="innerHTML">Remove</button></td></tr>`,
			template.HTMLEscapeString(p.NodeID))
	}
	fmt.Fprint(w, `</tbody></table></div>`)

	fmt.Fprint(w, `<div style="margin-bottom:1rem;display:flex;gap:0.5rem">`)
	fmt.Fprint(w, `<button class="btn btn-primary" hx-get="/ui/partials/peer-add-form" hx-target="#fed-modal-container" hx-swap="innerHTML">Add Peer</button>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `<div id="fed-modal-container"></div>`)

	if h.data != nil {
		entries, err := h.data.LedgerEntriesSince(r.Context(), time.Time{}, federation.Ops, 100)
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

// peerForm renders the add (p == nil) or edit form. Key inputs are
// password fields that are never pre-filled; blank keeps the stored key.
func peerForm(w http.ResponseWriter, p *api.PeerView) {
	title, action, submit, idField := "Add Federation Peer", "/ui/api/peers/add", "Add Peer",
		`<label>Node ID <input type="text" name="node_id" required placeholder="the peer's memora-core node id"></label>`
	name, url, timeout, keyHint, revoked := "", "", "", "", ""
	if p != nil {
		title, action, submit = "Edit Peer: "+p.Name, "/ui/api/peers/update", "Update Peer"
		idField = fmt.Sprintf(`<input type="hidden" name="node_id" value="%s"><p class="mono">%s</p>`,
			template.HTMLEscapeString(p.NodeID), template.HTMLEscapeString(p.NodeID))
		name, url = p.Name, p.URL
		if p.TimeoutMS > 0 {
			timeout = strconv.Itoa(p.TimeoutMS)
		}
		keyHint = " (blank keeps the stored key)"
		if p.Revoked {
			revoked = " checked"
		}
	}
	fmt.Fprintf(w, `<dialog id="peer-modal" class="modal" aria-labelledby="peer-title" open>
<div class="modal-form">
  <h3 id="peer-title">%s</h3>
  <div id="peer-form-result"></div>
  <form hx-post="%s" hx-target="#peer-form-result" hx-swap="innerHTML">
    %s
    <label>Name <input type="text" name="name" required value="%s"></label>
    <label>Endpoint <input type="url" name="endpoint" required value="%s" placeholder="https://peer.example.com"></label>
    <label>Inbound key%s <input type="password" name="inbound_key" autocomplete="off" placeholder="the key this peer presents to us"></label>
    <label>Outbound key%s <input type="password" name="api_key" autocomplete="off" placeholder="the key we present to this peer"></label>
    <label>Timeout (ms) <input type="number" name="timeout_ms" min="0" value="%s" placeholder="default"></label>
    <label style="display:flex;align-items:center;gap:0.5rem;font-weight:normal"><input type="checkbox" name="revoked" value="true"%s> Revoked</label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">%s</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(title), action, idField,
		template.HTMLEscapeString(name), template.HTMLEscapeString(url),
		keyHint, keyHint, timeout, revoked, submit)
}

func (h *Handler) partialPeerAddForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	peerForm(w, nil)
}

func (h *Handler) partialPeerEditForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p := h.findPeer(r.Context(), r.URL.Query().Get("id"))
	if p == nil {
		h.writeFormError(w, "Peer not found")
		return
	}
	peerForm(w, p)
}

func (h *Handler) handlePeerAdd(w http.ResponseWriter, r *http.Request)    { h.savePeer(w, r) }
func (h *Handler) handlePeerUpdate(w http.ResponseWriter, r *http.Request) { h.savePeer(w, r) }

// savePeer upserts the peer the add or edit form describes.
func (h *Handler) savePeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.peers == nil {
		h.writeFormError(w, "Federation is not enabled")
		return
	}
	req := api.PeerRequest{
		NodeID:     strings.TrimSpace(r.FormValue("node_id")),
		Name:       strings.TrimSpace(r.FormValue("name")),
		URL:        strings.TrimSpace(r.FormValue("endpoint")),
		InboundKey: r.FormValue("inbound_key"),
		APIKey:     r.FormValue("api_key"),
		Revoked:    r.FormValue("revoked") == "true",
	}
	if req.NodeID == "" || req.Name == "" || req.URL == "" {
		h.writeFormError(w, "Node ID, name and endpoint are required")
		return
	}
	if t := strings.TrimSpace(r.FormValue("timeout_ms")); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			h.writeFormError(w, "Timeout must be a non-negative number of milliseconds")
			return
		}
		req.TimeoutMS = n
	}
	if _, err := h.peers.Upsert(r.Context(), req); err != nil {
		h.writeFormError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/ui/federation")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialPeerRemoveForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p := h.findPeer(r.Context(), r.URL.Query().Get("id"))
	if p == nil {
		h.writeFormError(w, "Peer not found")
		return
	}
	fmt.Fprintf(w, `<dialog id="peer-modal" class="modal" aria-labelledby="peer-title" open>
<div class="modal-form">
  <h3 id="peer-title">Remove Peer</h3>
  <div id="peer-remove-result"></div>
  <p>Are you sure you want to remove peer <strong>%s</strong> (%s)?</p>
  <p class="text-muted" style="font-size:0.85rem">This node stops searching it and refuses its calls at once. Memories already received are not deleted.</p>
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
		template.HTMLEscapeString(p.Name),
		template.HTMLEscapeString(p.NodeID),
		template.HTMLEscapeString(p.NodeID),
		template.HTMLEscapeString(p.Name))
}

func (h *Handler) handlePeerRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.peers == nil {
		h.writeFormError(w, "Federation is not enabled")
		return
	}
	p := h.findPeer(r.Context(), strings.TrimSpace(r.FormValue("id")))
	if p == nil {
		h.writeFormError(w, "Peer not found")
		return
	}
	if strings.TrimSpace(r.FormValue("confirm")) != p.Name {
		h.writeFormError(w, fmt.Sprintf("Type %q to confirm", p.Name))
		return
	}
	if err := h.peers.Delete(r.Context(), p.NodeID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/ui/federation")
	w.WriteHeader(http.StatusOK)
}
