package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func (h *Handler) handleMemoryImprint(w http.ResponseWriter, r *http.Request) {
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

	content := r.FormValue("content")
	if strings.TrimSpace(content) == "" {
		h.writeFormError(w, "Content is required")
		return
	}

	req := api.ImprintRequest{
		Content:      content,
		CollectionID: strings.TrimSpace(r.FormValue("collection_id")),
		ChunkerID:    strings.TrimSpace(r.FormValue("chunker_id")),
	}

	tags := parseTags(r)
	if len(tags) > 0 {
		req.Tags = tags
	}

	resp, err := h.data.ImprintMemory(r.Context(), wsID, req)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	recallBadge := `<span class="badge badge-ok">Yes</span>`
	if !resp.RecallReady {
		recallBadge = `<span class="badge badge-warn">Pending</span>`
	}
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Memory Created</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td></tr>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(resp.MemoryID), template.HTMLEscapeString(resp.MemoryID))
	fmt.Fprintf(w, `<tr><td><strong>Watermark</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.Watermark))
	fmt.Fprintf(w, `<tr><td><strong>Cells Created</strong></td><td>%d</td></tr>`, resp.CellsCreated)
	fmt.Fprintf(w, `<tr><td><strong>Recall Ready</strong></td><td>%s</td></tr>`, recallBadge)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `<tr><td><strong>Latency</strong></td><td>%d ms</td></tr>`, resp.LatencyMS)
	fmt.Fprintf(w, `</table></div>`)
}

func (h *Handler) partialMemoryImprintForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	if wsID == "" {
		h.writeFormError(w, "Missing workspace ID")
		return
	}

	var collOptions string
	if h.data != nil {
		colls, _ := h.data.ListCollections(r.Context(), wsID)
		for _, c := range colls {
			collOptions += fmt.Sprintf(`<option value="%s">%s</option>`,
				template.HTMLEscapeString(c.ID),
				template.HTMLEscapeString(c.Name))
		}
	}

	fmt.Fprintf(w, `<dialog id="mem-imprint-modal" class="modal" open>
<form hx-post="/ui/api/memories/imprint" hx-target="#imprint-result" hx-swap="innerHTML" class="modal-form" style="max-width:600px">
  <h3>Imprint Memory</h3>
  <div id="imprint-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <label>Content <span class="text-muted">(required)</span>
    <textarea name="content" required rows="8" style="width:100%%;font-family:var(--font-mono);font-size:0.9rem" placeholder="Enter memory content..."></textarea>
  </label>
  <label>Collection
    <select name="collection_id">
      <option value="">(none)</option>
      %s
    </select>
  </label>
  <label>Chunker
    <select name="chunker_id">
      <option value="">default</option>
      <option value="markdown">markdown</option>
      <option value="noop">no-chunk</option>
    </select>
  </label>
  <fieldset style="border:1px solid var(--border);border-radius:6px;padding:0.75rem;margin-top:0.5rem">
    <legend style="font-size:0.9rem;font-weight:600;padding:0 0.25rem">Tags</legend>
    <div id="tag-rows">
      <div style="display:flex;gap:0.5rem;margin-bottom:0.25rem">
        <input type="text" name="tag_key" placeholder="key" style="flex:1">
        <input type="text" name="tag_value" placeholder="value" style="flex:1">
      </div>
    </div>
    <button type="button" class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.5rem" onclick="var d=document.createElement('div');d.style.cssText='display:flex;gap:0.5rem;margin-bottom:0.25rem';d.innerHTML='<input type=\'text\' name=\'tag_key\' placeholder=\'key\' style=\'flex:1\'><input type=\'text\' name=\'tag_value\' placeholder=\'value\' style=\'flex:1\'>';document.getElementById('tag-rows').appendChild(d)">+ Add Tag</button>
  </fieldset>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Imprint</button>
  </div>
</form>
</dialog>`, template.HTMLEscapeString(wsID), collOptions)
}

func parseTags(r *http.Request) map[string]string {
	keys := r.Form["tag_key"]
	vals := r.Form["tag_value"]
	tags := make(map[string]string)
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v := ""
		if i < len(vals) {
			v = strings.TrimSpace(vals[i])
		}
		tags[k] = v
	}
	return tags
}
