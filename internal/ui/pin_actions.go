package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handler) partialPinList(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>No data source.</p></div>`)
		return
	}

	pins, err := h.data.ListPins(r.Context(), wsID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/pin-create-form?ws=%s" hx-target="#pin-modal-container" hx-swap="innerHTML">Create Pin</button></div>`,
		template.HTMLEscapeString(wsID))
	fmt.Fprint(w, `<div id="pin-modal-container"></div>`)

	if len(pins) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Pins</h3><p>Pin a recall query to track results over time.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Pin ID</th><th>Label</th><th>Query</th><th>Mode</th><th>K</th><th>Watermark</th><th>Created</th><th></th></tr></thead><tbody>`)
	for _, p := range pins {
		label := p.Label
		if label == "" {
			label = "—"
		}
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td>%s</td><td>%s</td><td class="text-right">%d</td><td class="mono">%s</td><td>%s</td><td><button class="btn" style="color:var(--danger);border-color:var(--danger);padding:0.25rem 0.5rem;font-size:0.85rem" hx-get="/ui/partials/pin-delete-form?ws=%s&amp;id=%s" hx-target="#pin-modal-container" hx-swap="innerHTML">Delete</button></td></tr>`,
			template.HTMLEscapeString(truncateStr(p.PinID, 16)),
			template.HTMLEscapeString(label),
			template.HTMLEscapeString(truncateStr(p.Query, 40)),
			template.HTMLEscapeString(p.Mode),
			p.K,
			template.HTMLEscapeString(truncateStr(p.Watermark, 16)),
			template.HTMLEscapeString(p.CreatedAt.UTC().Format("Jan 2 15:04")),
			template.HTMLEscapeString(wsID),
			template.HTMLEscapeString(p.PinID),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) partialPinCreateForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	query := r.URL.Query().Get("q")
	mode := r.URL.Query().Get("mode")
	kStr := r.URL.Query().Get("k")
	if wsID == "" {
		h.writeFormError(w, "Missing workspace ID")
		return
	}
	if mode == "" {
		mode = "hybrid"
	}
	k := 10
	if v, err := strconv.Atoi(kStr); err == nil && v > 0 {
		k = v
	}

	modeOptions := ""
	for _, m := range []string{"hybrid", "vector", "keyword", "lookup"} {
		sel := ""
		if m == mode {
			sel = " selected"
		}
		modeOptions += fmt.Sprintf(`<option value="%s"%s>%s</option>`, m, sel, m)
	}

	fmt.Fprintf(w, `<dialog id="pin-modal" class="modal" open aria-labelledby="pin-create-title">
<div class="modal-form">
  <h3 id="pin-create-title">Create Recall Pin</h3>
  <div id="pin-create-result"></div>
  <form hx-post="/ui/api/pins/create" hx-target="#pin-create-result" hx-swap="innerHTML">
    <input type="hidden" name="ws" value="%s">
    <label>Query <input type="text" name="query" value="%s" required placeholder="search query..."></label>
    <label>Mode <select name="mode">%s</select></label>
    <label>K <input type="number" name="k" value="%d" min="1" max="100"></label>
    <label>Watermark <input type="text" name="watermark" placeholder="(optional)"></label>
    <label>Label <input type="text" name="label" placeholder="(optional descriptive label)"></label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn btn-primary">Create Pin</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(query),
		modeOptions,
		k)
}

func (h *Handler) handlePinCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("ws"))
	query := strings.TrimSpace(r.FormValue("query"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	kStr := strings.TrimSpace(r.FormValue("k"))
	watermark := strings.TrimSpace(r.FormValue("watermark"))
	label := strings.TrimSpace(r.FormValue("label"))

	if wsID == "" || query == "" {
		h.writeFormError(w, "Workspace and query are required")
		return
	}
	if mode == "" {
		mode = "hybrid"
	}
	k, _ := strconv.Atoi(kStr)
	if k <= 0 {
		k = 10
	}

	pinID, err := h.data.CreatePin(r.Context(), wsID, query, mode, k, watermark, label)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card" style="background:var(--success-bg);border-color:var(--success)"><p style="margin:0">Pin created: <code>%s</code></p></div>`,
		template.HTMLEscapeString(pinID))
}

func (h *Handler) partialPinDeleteForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	pinID := r.URL.Query().Get("id")
	if wsID == "" || pinID == "" {
		h.writeFormError(w, "Workspace and pin ID required")
		return
	}

	fmt.Fprintf(w, `<dialog id="pin-modal" class="modal" open aria-labelledby="pin-delete-title">
<div class="modal-form">
  <h3 id="pin-delete-title">Delete Pin</h3>
  <div id="pin-delete-result"></div>
  <p>Delete pin <code>%s</code>?</p>
  <form hx-post="/ui/api/pins/delete" hx-target="#pin-delete-result" hx-swap="innerHTML">
    <input type="hidden" name="ws" value="%s">
    <input type="hidden" name="id" value="%s">
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="submit" class="btn" style="color:var(--danger);border-color:var(--danger)">Delete Pin</button>
    </div>
  </form>
</div>
</dialog>`,
		template.HTMLEscapeString(truncateStr(pinID, 20)),
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(pinID))
}

func (h *Handler) handlePinDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("ws"))
	pinID := strings.TrimSpace(r.FormValue("id"))
	if pinID == "" {
		h.writeFormError(w, "Pin ID is required")
		return
	}

	if err := h.data.DeletePin(r.Context(), pinID); err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/ui/workspaces/%s/memories", wsID))
	w.WriteHeader(http.StatusOK)
}
