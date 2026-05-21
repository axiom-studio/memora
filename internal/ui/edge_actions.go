package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

var edgeTypes = []string{
	"parent_of", "derived_from", "supersedes", "references",
	"session_of", "mentions", "vector_neighbor",
}

func (h *Handler) partialEdgeLinkForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	sourceMemID := r.URL.Query().Get("source")
	if wsID == "" {
		h.writeFormError(w, "Missing workspace ID")
		return
	}

	var edgeTypeOpts string
	for _, et := range edgeTypes {
		sel := ""
		if et == "references" {
			sel = " selected"
		}
		edgeTypeOpts += fmt.Sprintf(`<option value="%s"%s>%s</option>`, et, sel, et)
	}

	fmt.Fprintf(w, `<dialog id="edge-link-modal" class="modal" open aria-labelledby="edge-link-title">
<form hx-post="/ui/api/edges/link" hx-target="#edge-result" hx-swap="innerHTML" class="modal-form">
  <h3 id="edge-link-title">Link Edge</h3>
  <div id="edge-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <label>Source Memory ID
    <input type="text" name="source_memory_id" value="%s" placeholder="mem_..." required>
  </label>
  <label>Target Memory ID
    <input type="text" name="target_memory_id" placeholder="mem_..." required>
  </label>
  <label>Edge Type
    <select name="edge_type" required>%s</select>
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Create Edge</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(sourceMemID),
		edgeTypeOpts)
}

func (h *Handler) handleEdgeLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	sourceMemID := strings.TrimSpace(r.FormValue("source_memory_id"))
	targetMemID := strings.TrimSpace(r.FormValue("target_memory_id"))
	edgeType := strings.TrimSpace(r.FormValue("edge_type"))

	if wsID == "" || sourceMemID == "" || targetMemID == "" {
		h.writeFormError(w, "Workspace, source, and target memory IDs are required")
		return
	}
	if edgeType == "" {
		h.writeFormError(w, "Edge type is required")
		return
	}
	if sourceMemID == targetMemID {
		h.writeFormError(w, "Source and target must be different (self-loops not allowed)")
		return
	}

	edgeID, err := h.data.LinkEdge(r.Context(), wsID, sourceMemID, targetMemID, edgeType)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Edge Created</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Edge ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(edgeID))
	fmt.Fprintf(w, `<tr><td><strong>Type</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(edgeType))
	fmt.Fprintf(w, `<tr><td><strong>Source</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(sourceMemID))
	fmt.Fprintf(w, `<tr><td><strong>Target</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(targetMemID))
	fmt.Fprint(w, `</table></div>`)
}

func (h *Handler) partialEdgeUnlinkForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	edgeID := r.URL.Query().Get("id")
	if wsID == "" || edgeID == "" {
		h.writeFormError(w, "Missing workspace or edge ID")
		return
	}

	fmt.Fprintf(w, `<dialog id="edge-unlink-modal" class="modal" open aria-labelledby="edge-unlink-title">
<form hx-post="/ui/api/edges/unlink" hx-target="#edge-unlink-result" hx-swap="innerHTML" class="modal-form">
  <h3 id="edge-unlink-title">Unlink Edge</h3>
  <div id="edge-unlink-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="edge_id" value="%s">
  <p>This will soft-delete edge <strong class="mono">%s</strong>. The edge will be marked as deleted but preserved in the ledger for audit.</p>
  <label>Type the edge ID to confirm
    <input type="text" name="confirm" placeholder="%s" required>
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Unlink</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(edgeID),
		template.HTMLEscapeString(edgeID),
		template.HTMLEscapeString(edgeID))
}

func (h *Handler) handleEdgeUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	edgeID := strings.TrimSpace(r.FormValue("edge_id"))
	confirm := strings.TrimSpace(r.FormValue("confirm"))

	if wsID == "" || edgeID == "" {
		h.writeFormError(w, "Missing workspace or edge ID")
		return
	}
	if confirm != edgeID {
		h.writeFormError(w, "Confirmation does not match edge ID")
		return
	}

	if err := h.data.UnlinkEdge(r.Context(), edgeID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/ui/workspaces/%s/graph", wsID))
	w.WriteHeader(http.StatusOK)
}
