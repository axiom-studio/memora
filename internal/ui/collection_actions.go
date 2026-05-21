package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

func (h *Handler) handleCollectionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeFormError(w, "Workspace ID is required")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.writeFormError(w, "Name is required")
		return
	}

	input := CreateCollectionInput{
		WorkspaceID: wsID,
		Name:        name,
	}
	if _, err := h.data.CreateCollection(r.Context(), input); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/workspaces/"+wsID+"?tab=collections")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleCollectionDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	collID := strings.TrimSpace(r.FormValue("id"))
	if collID == "" {
		h.writeFormError(w, "Collection ID is required")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeFormError(w, "Workspace ID is required")
		return
	}

	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != collID {
		h.writeFormError(w, "Type the collection ID to confirm deletion")
		return
	}

	if err := h.data.DeleteCollection(r.Context(), collID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/workspaces/"+wsID+"?tab=collections")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialCollectionCreateForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	if wsID == "" {
		h.writeFormError(w, "Missing workspace ID")
		return
	}
	fmt.Fprintf(w, `<dialog id="coll-create-modal" class="modal" aria-labelledby="coll-create-title">
<form hx-post="/ui/api/collections/create" hx-target="#coll-form-errors" hx-swap="innerHTML" class="modal-form">
  <h3 id="coll-create-title">Create Collection</h3>
  <div id="coll-form-errors"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <label>Name <span class="text-muted">(required)</span>
    <input type="text" name="name" required autofocus placeholder="my-collection">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Create</button>
  </div>
</form>
</dialog>`, template.HTMLEscapeString(wsID))
}

func (h *Handler) partialCollectionDeleteForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	collID := r.URL.Query().Get("id")
	wsID := r.URL.Query().Get("ws")
	if collID == "" || wsID == "" {
		h.writeFormError(w, "Collection not found")
		return
	}

	colls, err := h.data.ListCollections(r.Context(), wsID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}
	var coll *CollectionSummary
	for i := range colls {
		if colls[i].ID == collID {
			coll = &colls[i]
			break
		}
	}
	if coll == nil {
		h.writeFormError(w, "Collection not found")
		return
	}

	fmt.Fprintf(w, `<dialog id="coll-delete-modal" class="modal" aria-labelledby="coll-delete-title">
<form hx-post="/ui/api/collections/delete" hx-target="#coll-delete-form-errors" hx-swap="innerHTML" class="modal-form">
  <h3 id="coll-delete-title">Delete Collection</h3>
  <div id="coll-delete-form-errors"></div>
  <input type="hidden" name="id" value="%s">
  <input type="hidden" name="workspace_id" value="%s">
  <p>This will permanently delete collection <strong>%s</strong>.</p>
  <p class="text-muted">%d memories in this collection.</p>
  <label>Type <code>%s</code> to confirm
    <input type="text" name="confirm" required autocomplete="off" placeholder="%s">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-danger-fill">Delete</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(coll.ID),
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(coll.Name),
		coll.MemoryCount,
		template.HTMLEscapeString(coll.ID),
		template.HTMLEscapeString(coll.ID),
	)
}
