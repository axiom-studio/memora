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
	w.Header().Set("HX-Trigger", "memoryListChanged")
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

func (h *Handler) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	memID := strings.TrimSpace(r.FormValue("memory_id"))
	if wsID == "" || memID == "" {
		h.writeFormError(w, "Workspace ID and Memory ID are required")
		return
	}

	content := r.FormValue("content")
	if strings.TrimSpace(content) == "" {
		h.writeFormError(w, "Content is required")
		return
	}

	req := api.UpdateRequest{
		Content:           content,
		ExpectedWatermark: strings.TrimSpace(r.FormValue("expected_watermark")),
	}

	tags := parseTags(r)
	if len(tags) > 0 {
		req.Tags = tags
	}

	resp, err := h.data.UpdateMemory(r.Context(), wsID, memID, req)
	if err != nil {
		if strings.Contains(err.Error(), "cas conflict") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<div class="form-error">This memory was modified since you opened it. <a href="/ui/workspaces/%s/memories/%s">Reload</a> and reapply your changes.</div>`,
				template.HTMLEscapeString(wsID), template.HTMLEscapeString(memID))
			return
		}
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Memory Updated</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td></tr>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(resp.MemoryID), template.HTMLEscapeString(resp.MemoryID))
	fmt.Fprintf(w, `<tr><td><strong>New Watermark</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.Watermark))
	fmt.Fprintf(w, `<tr><td><strong>Cells Re-embedded</strong></td><td>%d</td></tr>`, resp.CellsReembed)
	fmt.Fprintf(w, `<tr><td><strong>Cells Skipped</strong></td><td>%d</td></tr>`, resp.CellsSkipped)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `</table></div>`)
}

func (h *Handler) partialMemoryEditForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	memID := r.URL.Query().Get("id")
	if wsID == "" || memID == "" || h.data == nil {
		h.writeFormError(w, "Memory not found")
		return
	}

	mem, err := h.data.GetMemory(r.Context(), memID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	var tagRows string
	if len(mem.Tags) > 0 {
		for k, v := range mem.Tags {
			tagRows += fmt.Sprintf(`<div style="display:flex;gap:0.5rem;margin-bottom:0.25rem"><input type="text" name="tag_key" value="%s" style="flex:1"><input type="text" name="tag_value" value="%s" style="flex:1"></div>`,
				template.HTMLEscapeString(k), template.HTMLEscapeString(v))
		}
	} else {
		tagRows = `<div style="display:flex;gap:0.5rem;margin-bottom:0.25rem"><input type="text" name="tag_key" placeholder="key" style="flex:1"><input type="text" name="tag_value" placeholder="value" style="flex:1"></div>`
	}

	fmt.Fprintf(w, `<dialog id="mem-edit-modal" class="modal" open>
<form hx-post="/ui/api/memories/update" hx-target="#update-result" hx-swap="innerHTML" class="modal-form" style="max-width:600px">
  <h3>Edit Memory</h3>
  <div id="update-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="memory_id" value="%s">
  <input type="hidden" name="expected_watermark" value="%s">
  <p class="text-muted" style="font-size:0.85rem">Watermark: <code>%s</code></p>
  <label>Content <span class="text-muted">(required)</span>
    <textarea name="content" required rows="10" style="width:100%%;font-family:var(--font-mono);font-size:0.9rem">%s</textarea>
  </label>
  <fieldset style="border:1px solid var(--border);border-radius:6px;padding:0.75rem;margin-top:0.5rem">
    <legend style="font-size:0.9rem;font-weight:600;padding:0 0.25rem">Tags</legend>
    <div id="edit-tag-rows">%s</div>
    <button type="button" class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.5rem" onclick="var d=document.createElement('div');d.style.cssText='display:flex;gap:0.5rem;margin-bottom:0.25rem';d.innerHTML='<input type=\'text\' name=\'tag_key\' placeholder=\'key\' style=\'flex:1\'><input type=\'text\' name=\'tag_value\' placeholder=\'value\' style=\'flex:1\'>';document.getElementById('edit-tag-rows').appendChild(d)">+ Add Tag</button>
  </fieldset>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Update</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(mem.ID),
		template.HTMLEscapeString(mem.Watermark),
		template.HTMLEscapeString(truncateStr(mem.Watermark, 20)),
		template.HTMLEscapeString(mem.Content),
		tagRows,
	)
}

func (h *Handler) handleMemoryPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	memID := strings.TrimSpace(r.FormValue("memory_id"))
	if wsID == "" || memID == "" {
		h.writeFormError(w, "Workspace ID and Memory ID are required")
		return
	}

	oldStrs := r.Form["old_string"]
	newStrs := r.Form["new_string"]
	replaceAlls := r.Form["replace_all"]

	var ops []api.PatchOp
	for i, old := range oldStrs {
		if old == "" {
			continue
		}
		newStr := ""
		if i < len(newStrs) {
			newStr = newStrs[i]
		}
		ra := false
		if i < len(replaceAlls) && replaceAlls[i] == "true" {
			ra = true
		}
		ops = append(ops, api.PatchOp{OldString: old, NewString: newStr, ReplaceAll: ra})
	}

	if len(ops) == 0 {
		h.writeFormError(w, "At least one patch operation is required")
		return
	}

	req := api.PatchRequest{
		Patch:             ops,
		ExpectedWatermark: strings.TrimSpace(r.FormValue("expected_watermark")),
	}

	resp, err := h.data.PatchMemory(r.Context(), wsID, memID, req)
	if err != nil {
		if strings.Contains(err.Error(), "cas conflict") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<div class="form-error">This memory was modified since you opened it. <a href="/ui/workspaces/%s/memories/%s">Reload</a> and reapply your changes.</div>`,
				template.HTMLEscapeString(wsID), template.HTMLEscapeString(memID))
			return
		}
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Patch Applied</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td></tr>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(resp.MemoryID), template.HTMLEscapeString(resp.MemoryID))
	fmt.Fprintf(w, `<tr><td><strong>New Watermark</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.Watermark))
	fmt.Fprintf(w, `<tr><td><strong>Patches Applied</strong></td><td>%d</td></tr>`, resp.PatchesApplied)
	fmt.Fprintf(w, `<tr><td><strong>Cells Re-embedded</strong></td><td>%d</td></tr>`, resp.CellsReembed)
	fmt.Fprintf(w, `<tr><td><strong>Cells Skipped</strong></td><td>%d</td></tr>`, resp.CellsSkipped)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `</table></div>`)
}

func (h *Handler) partialMemoryPatchForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	memID := r.URL.Query().Get("id")
	if wsID == "" || memID == "" || h.data == nil {
		h.writeFormError(w, "Memory not found")
		return
	}

	mem, err := h.data.GetMemory(r.Context(), memID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	fmt.Fprintf(w, `<dialog id="mem-patch-modal" class="modal" open>
<form hx-post="/ui/api/memories/patch" hx-target="#patch-result" hx-swap="innerHTML" class="modal-form" style="max-width:700px">
  <h3>Patch Memory</h3>
  <div id="patch-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="memory_id" value="%s">
  <input type="hidden" name="expected_watermark" value="%s">
  <details style="margin-bottom:0.75rem">
    <summary style="cursor:pointer;font-weight:600;font-size:0.9rem">Current Content <span class="text-muted">(click to expand)</span></summary>
    <pre id="patch-content-preview" style="white-space:pre-wrap;word-break:break-word;max-height:200px;overflow:auto;background:var(--bg-secondary);padding:0.5rem;border-radius:4px;font-size:0.85rem;margin-top:0.5rem">%s</pre>
  </details>
  <div id="patch-ops">
    <div class="patch-op" style="border:1px solid var(--border);border-radius:6px;padding:0.75rem;margin-bottom:0.5rem">
      <label style="font-size:0.9rem;font-weight:600">Find (old_string)</label>
      <textarea name="old_string" rows="2" style="width:100%%;font-family:var(--font-mono);font-size:0.85rem" placeholder="text to find..." oninput="checkAnchor(this)"></textarea>
      <div class="anchor-status" style="font-size:0.8rem;margin:0.25rem 0"></div>
      <label style="font-size:0.9rem;font-weight:600">Replace (new_string)</label>
      <textarea name="new_string" rows="2" style="width:100%%;font-family:var(--font-mono);font-size:0.85rem" placeholder="replacement text..."></textarea>
      <label style="font-size:0.85rem;display:flex;align-items:center;gap:0.5rem;margin-top:0.25rem">
        <input type="checkbox" onchange="this.previousElementSibling || null; this.nextElementSibling.value=this.checked?'true':'false'"><input type="hidden" name="replace_all" value="false"> Replace all occurrences
      </label>
    </div>
  </div>
  <button type="button" class="btn btn-secondary" style="font-size:0.85rem;margin-bottom:0.75rem" onclick="addPatchOp()">+ Add Operation</button>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary" id="patch-submit-btn">Apply Patch</button>
  </div>
</form>
</dialog>
<script>
var patchContent = %s;
function checkAnchor(el) {
  var status = el.parentElement.querySelector('.anchor-status');
  var val = el.value;
  if (!val) { status.textContent = ''; return; }
  var count = 0, idx = -1;
  while ((idx = patchContent.indexOf(val, idx + 1)) !== -1) count++;
  if (count === 0) {
    status.innerHTML = '<span style="color:var(--danger)">Not found in content</span>';
  } else if (count === 1) {
    status.innerHTML = '<span style="color:var(--success)">Unique match</span>';
  } else {
    status.innerHTML = '<span style="color:var(--warning)">' + count + ' matches (use replace_all or narrow the anchor)</span>';
  }
}
function addPatchOp() {
  var container = document.getElementById('patch-ops');
  var div = document.createElement('div');
  div.className = 'patch-op';
  div.style.cssText = 'border:1px solid var(--border);border-radius:6px;padding:0.75rem;margin-bottom:0.5rem';
  div.innerHTML = '<label style="font-size:0.9rem;font-weight:600">Find (old_string)</label>' +
    '<textarea name="old_string" rows="2" style="width:100%%;font-family:var(--font-mono);font-size:0.85rem" placeholder="text to find..." oninput="checkAnchor(this)"></textarea>' +
    '<div class="anchor-status" style="font-size:0.8rem;margin:0.25rem 0"></div>' +
    '<label style="font-size:0.9rem;font-weight:600">Replace (new_string)</label>' +
    '<textarea name="new_string" rows="2" style="width:100%%;font-family:var(--font-mono);font-size:0.85rem" placeholder="replacement text..."></textarea>' +
    '<label style="font-size:0.85rem;display:flex;align-items:center;gap:0.5rem;margin-top:0.25rem"><input type="checkbox" onchange="this.nextElementSibling.value=this.checked?\'true\':\'false\'"><input type="hidden" name="replace_all" value="false"> Replace all occurrences</label>';
  container.appendChild(div);
}
</script>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(mem.ID),
		template.HTMLEscapeString(mem.Watermark),
		template.HTMLEscapeString(mem.Content),
		jsonEscapeString(mem.Content),
	)
}

func (h *Handler) handleMemoryAppend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	memID := strings.TrimSpace(r.FormValue("memory_id"))
	if wsID == "" || memID == "" {
		h.writeFormError(w, "Workspace ID and Memory ID are required")
		return
	}

	content := r.FormValue("content")
	if strings.TrimSpace(content) == "" {
		h.writeFormError(w, "Content is required")
		return
	}

	req := api.AppendRequest{
		Content:           content,
		ExpectedWatermark: strings.TrimSpace(r.FormValue("expected_watermark")),
	}

	resp, err := h.data.AppendMemory(r.Context(), wsID, memID, req)
	if err != nil {
		if strings.Contains(err.Error(), "cas conflict") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<div class="form-error">This memory was modified since you opened it. <a href="/ui/workspaces/%s/memories/%s">Reload</a> and reapply your changes.</div>`,
				template.HTMLEscapeString(wsID), template.HTMLEscapeString(memID))
			return
		}
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Content Appended</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td></tr>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(resp.MemoryID), template.HTMLEscapeString(resp.MemoryID))
	fmt.Fprintf(w, `<tr><td><strong>New Watermark</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.Watermark))
	fmt.Fprintf(w, `<tr><td><strong>Cells Added</strong></td><td>%d</td></tr>`, resp.CellsAdded)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `</table></div>`)
}

func (h *Handler) partialMemoryAppendForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	memID := r.URL.Query().Get("id")
	if wsID == "" || memID == "" || h.data == nil {
		h.writeFormError(w, "Memory not found")
		return
	}

	mem, err := h.data.GetMemory(r.Context(), memID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	fmt.Fprintf(w, `<dialog id="mem-append-modal" class="modal" open>
<form hx-post="/ui/api/memories/append" hx-target="#append-result" hx-swap="innerHTML" class="modal-form" style="max-width:600px">
  <h3>Append to Memory</h3>
  <div id="append-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="memory_id" value="%s">
  <input type="hidden" name="expected_watermark" value="%s">
  <p class="text-muted" style="font-size:0.85rem">Current length: %d chars · Watermark: <code>%s</code></p>
  <label>Content to append <span class="text-muted">(required)</span>
    <textarea name="content" required rows="6" style="width:100%%;font-family:var(--font-mono);font-size:0.9rem" placeholder="Content will be appended to the end..."></textarea>
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary">Append</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(mem.ID),
		template.HTMLEscapeString(mem.Watermark),
		len(mem.Content),
		template.HTMLEscapeString(truncateStr(mem.Watermark, 20)),
	)
}

func (h *Handler) handleMemoryForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	memID := strings.TrimSpace(r.FormValue("memory_id"))
	if wsID == "" || memID == "" {
		h.writeFormError(w, "Workspace ID and Memory ID are required")
		return
	}

	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != memID {
		h.writeFormError(w, "Type the memory ID to confirm deletion")
		return
	}

	resp, err := h.data.ForgetMemory(r.Context(), wsID, memID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Memory Forgotten</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.MemoryID))
	fmt.Fprintf(w, `<tr><td><strong>Cascaded Edges</strong></td><td>%d</td></tr>`, resp.CascadedEdges)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `</table><p style="margin-top:0.75rem"><a href="/ui/workspaces/%s?tab=overview">Back to workspace</a></p></div>`,
		template.HTMLEscapeString(wsID))
}

func (h *Handler) partialMemoryForgetForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wsID := r.URL.Query().Get("ws")
	memID := r.URL.Query().Get("id")
	if wsID == "" || memID == "" || h.data == nil {
		h.writeFormError(w, "Memory not found")
		return
	}

	mem, err := h.data.GetMemory(r.Context(), memID)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	edges, _ := h.data.GetEdges(r.Context(), wsID, memID)
	cells, _ := h.data.GetCells(r.Context(), memID)

	fmt.Fprintf(w, `<dialog id="mem-forget-modal" class="modal" open>
<form hx-post="/ui/api/memories/forget" hx-target="#forget-result" hx-swap="innerHTML" class="modal-form">
  <h3>Forget Memory</h3>
  <div id="forget-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <input type="hidden" name="memory_id" value="%s">
  <p>This will permanently delete memory <strong>%s</strong> and cascade to related data.</p>
  <div class="card" style="background:var(--bg-secondary);padding:0.75rem;margin:0.5rem 0">
    <p style="margin:0;font-size:0.9rem"><strong>Cascade effects:</strong></p>
    <ul style="margin:0.25rem 0 0 1.25rem;font-size:0.9rem">
      <li>%d cells will be deleted</li>
      <li>%d edges will be removed</li>
    </ul>
  </div>
  <label>Type <code>%s</code> to confirm
    <input type="text" name="confirm" required autocomplete="off" placeholder="%s">
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn" style="background:var(--danger);color:#fff;border-color:var(--danger)">Forget</button>
  </div>
</form>
</dialog>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(mem.ID),
		template.HTMLEscapeString(truncateStr(mem.ID, 24)),
		len(cells),
		len(edges),
		template.HTMLEscapeString(mem.ID),
		template.HTMLEscapeString(mem.ID),
	)
}

func jsonEscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return `"` + s + `"`
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
