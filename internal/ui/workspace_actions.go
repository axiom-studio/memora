package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

func (h *Handler) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.writeFormError(w, "Name is required")
		return
	}

	input := CreateWorkspaceInput{
		Name:           name,
		Region:         strings.TrimSpace(r.FormValue("region")),
		ChunkerID:      strings.TrimSpace(r.FormValue("chunker_id")),
		EmbeddingModel: strings.TrimSpace(r.FormValue("embedding_model")),
	}
	wsID, err := h.data.CreateWorkspace(r.Context(), input)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/workspaces/"+wsID)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleWorkspaceUpdate(w http.ResponseWriter, r *http.Request) {
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

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.writeFormError(w, "Name is required")
		return
	}

	input := UpdateWorkspaceInput{
		Name:           name,
		Region:         strings.TrimSpace(r.FormValue("region")),
		ChunkerID:      strings.TrimSpace(r.FormValue("chunker_id")),
		EmbeddingModel: strings.TrimSpace(r.FormValue("embedding_model")),
	}
	if err := h.data.UpdateWorkspace(r.Context(), wsID, input); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/workspaces/"+wsID)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
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

	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != wsID {
		h.writeFormError(w, "Type the workspace ID to confirm deletion")
		return
	}

	if err := h.data.DeleteWorkspace(r.Context(), wsID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", "/ui/workspaces")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) partialWorkspaceCreateForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<dialog id="ws-create-modal" class="modal" aria-labelledby="ws-create-title">
<form hx-post="/ui/api/workspaces/create" hx-target="#form-errors" hx-swap="innerHTML" class="modal-form">
  <h3 id="ws-create-title">Create Workspace</h3>
  <div id="form-errors"></div>
  <label>Name <span class="text-muted">(required)</span>
    <input type="text" name="name" required autofocus placeholder="my-workspace">
  </label>
  <label>Region
    <input type="text" name="region" placeholder="us-east-1">
  </label>
  <label>Chunker
    <input type="text" name="chunker_id" placeholder="default">
  </label>
  <label>Embedding Model
    <input type="text" name="embedding_model" placeholder="noop:default">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Create</button>
  </div>
</form>
</dialog>`)
}

func (h *Handler) partialWorkspaceEditForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("id")
	if h.data == nil || wsID == "" {
		h.writeFormError(w, "Workspace not found")
		return
	}
	ws, err := h.data.GetWorkspace(r.Context(), wsID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}
	fmt.Fprintf(w, `<dialog id="ws-edit-modal" class="modal" aria-labelledby="ws-edit-title">
<form hx-post="/ui/api/workspaces/update" hx-target="#edit-form-errors" hx-swap="innerHTML" class="modal-form">
  <h3 id="ws-edit-title">Edit Workspace</h3>
  <div id="edit-form-errors"></div>
  <input type="hidden" name="id" value="%s">
  <label>Name <span class="text-muted">(required)</span>
    <input type="text" name="name" required value="%s">
  </label>
  <label>Region
    <input type="text" name="region" value="%s">
  </label>
  <label>Chunker
    <input type="text" name="chunker_id" value="%s">
  </label>
  <label>Embedding Model
    <input type="text" name="embedding_model" value="%s">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Save</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(ws.ID),
		template.HTMLEscapeString(ws.Name),
		template.HTMLEscapeString(ws.Region),
		template.HTMLEscapeString(ws.ChunkerID),
		template.HTMLEscapeString(ws.EmbeddingModel),
	)
}

func (h *Handler) partialWorkspaceDeleteForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("id")
	if h.data == nil || wsID == "" {
		h.writeFormError(w, "Workspace not found")
		return
	}
	ws, err := h.data.GetWorkspace(r.Context(), wsID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}
	fmt.Fprintf(w, `<dialog id="ws-delete-modal" class="modal" aria-labelledby="ws-delete-title">
<form hx-post="/ui/api/workspaces/delete" hx-target="#delete-form-errors" hx-swap="innerHTML" class="modal-form">
  <h3 id="ws-delete-title">Delete Workspace</h3>
  <div id="delete-form-errors"></div>
  <input type="hidden" name="id" value="%s">
  <p>This will permanently delete workspace <strong>%s</strong> and all its memories, agents, and collections.</p>
  <p class="text-muted">%d memories, %d agents will be deleted.</p>
  <label>Type <code>%s</code> to confirm
    <input type="text" name="confirm" required autocomplete="off" placeholder="%s">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-danger-fill">Delete</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(ws.ID),
		template.HTMLEscapeString(ws.Name),
		ws.MemoryCount,
		ws.AgentCount,
		template.HTMLEscapeString(ws.ID),
		template.HTMLEscapeString(ws.ID),
	)
}

func (h *Handler) writeFormError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="form-error">%s</div>`, template.HTMLEscapeString(msg))
}
