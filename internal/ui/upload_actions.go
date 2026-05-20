package ui

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/axiom-studio/memora/pkg/types/api"
)

const maxUploadBytes = 8 << 20 // 8 MiB

var allowedExtensions = map[string]bool{
	".txt": true,
	".md":  true,
}

func (h *Handler) partialUploadForm(w http.ResponseWriter, r *http.Request) {
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

	fmt.Fprintf(w, `<dialog id="upload-modal" class="modal" open>
<form hx-post="/ui/api/memories/upload" hx-target="#upload-result" hx-swap="innerHTML" hx-encoding="multipart/form-data" class="modal-form" style="max-width:600px">
  <h3>Upload Document</h3>
  <div id="upload-result"></div>
  <input type="hidden" name="workspace_id" value="%s">
  <div id="drop-zone" style="border:2px dashed var(--border);border-radius:8px;padding:2rem;text-align:center;cursor:pointer;margin-bottom:0.75rem;transition:border-color 0.2s"
    ondragover="event.preventDefault();this.style.borderColor='var(--primary)'"
    ondragleave="this.style.borderColor='var(--border)'"
    ondrop="event.preventDefault();this.style.borderColor='var(--border)';handleFiles(event.dataTransfer.files)"
    onclick="document.getElementById('file-input').click()">
    <p style="margin:0;font-size:1.1rem;font-weight:600">Drop file here or click to browse</p>
    <p class="text-muted" style="margin:0.25rem 0 0;font-size:0.85rem">Accepted: .txt, .md (max 8 MiB)</p>
  </div>
  <input type="file" id="file-input" name="file" accept=".txt,.md" style="display:none" onchange="handleFiles(this.files)">
  <div id="file-info" style="display:none;margin-bottom:0.75rem">
    <div class="card" style="background:var(--bg-secondary);padding:0.5rem 0.75rem">
      <p style="margin:0;font-size:0.9rem"><strong id="file-name"></strong> — <span id="file-size"></span></p>
    </div>
  </div>
  <div id="file-preview-wrap" style="display:none;margin-bottom:0.75rem">
    <label style="font-size:0.9rem;font-weight:600">Preview</label>
    <pre id="file-preview" style="white-space:pre-wrap;word-break:break-word;max-height:200px;overflow:auto;background:var(--bg-secondary);padding:0.5rem;border-radius:4px;font-size:0.85rem"></pre>
  </div>
  <textarea name="content" id="upload-content" required style="display:none"></textarea>
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
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="submit" class="btn btn-primary" id="upload-submit" disabled>Upload &amp; Imprint</button>
  </div>
</form>
</dialog>
<script>
function handleFiles(files) {
  if (!files || !files.length) return;
  var file = files[0];
  var ext = file.name.substring(file.name.lastIndexOf('.')).toLowerCase();
  var result = document.getElementById('upload-result');
  if (ext !== '.txt' && ext !== '.md') {
    result.innerHTML = '<div class="form-error">Unsupported format (' + ext + '). Please convert to .txt or .md first.</div>';
    return;
  }
  if (file.size > %d) {
    result.innerHTML = '<div class="form-error">File too large (' + (file.size / 1048576).toFixed(1) + ' MiB). Maximum is 8 MiB.</div>';
    return;
  }
  result.innerHTML = '';
  document.getElementById('file-name').textContent = file.name;
  document.getElementById('file-size').textContent = (file.size / 1024).toFixed(1) + ' KiB';
  document.getElementById('file-info').style.display = 'block';
  var reader = new FileReader();
  reader.onload = function(e) {
    var text = e.target.result;
    document.getElementById('upload-content').value = text;
    var preview = document.getElementById('file-preview');
    preview.textContent = text.substring(0, 2000) + (text.length > 2000 ? '\n... (' + text.length + ' chars total)' : '');
    document.getElementById('file-preview-wrap').style.display = 'block';
    document.getElementById('upload-submit').disabled = false;
  };
  reader.readAsText(file);
}
</script>`, template.HTMLEscapeString(wsID), collOptions, maxUploadBytes)
}

func (h *Handler) handleMemoryUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeFormError(w, "Data source not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+4096)

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeFormError(w, "Workspace ID is required")
		return
	}

	var content string
	var filename string

	if file, header, err := r.FormFile("file"); err == nil {
		defer file.Close()
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !allowedExtensions[ext] {
			h.writeFormError(w, fmt.Sprintf("Unsupported format (%s). Convert to .txt or .md first.", ext))
			return
		}
		data, err := io.ReadAll(file)
		if err != nil {
			h.writeFormError(w, "Failed to read file")
			return
		}
		content = string(data)
		filename = header.Filename
	} else {
		content = r.FormValue("content")
	}

	if strings.TrimSpace(content) == "" {
		h.writeFormError(w, "No file content")
		return
	}

	req := api.ImprintRequest{
		Content:      content,
		CollectionID: strings.TrimSpace(r.FormValue("collection_id")),
		ChunkerID:    strings.TrimSpace(r.FormValue("chunker_id")),
	}

	if filename != "" {
		req.Tags = map[string]string{"source_filename": filename}
	}

	resp, err := h.data.ImprintMemory(r.Context(), wsID, req)
	if err != nil {
		h.writeFormError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Document Uploaded</h3><table>`)
	fmt.Fprintf(w, `<tr><td><strong>Memory ID</strong></td><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td></tr>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(resp.MemoryID), template.HTMLEscapeString(resp.MemoryID))
	if filename != "" {
		fmt.Fprintf(w, `<tr><td><strong>Source File</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(filename))
	}
	fmt.Fprintf(w, `<tr><td><strong>Cells Created</strong></td><td>%d</td></tr>`, resp.CellsCreated)
	recallBadge := `<span class="badge badge-ok">Yes</span>`
	if !resp.RecallReady {
		recallBadge = `<span class="badge badge-warn">Pending</span>`
	}
	fmt.Fprintf(w, `<tr><td><strong>Recall Ready</strong></td><td>%s</td></tr>`, recallBadge)
	fmt.Fprintf(w, `<tr><td><strong>Ledger ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(resp.LedgerID))
	fmt.Fprintf(w, `</table></div>`)
}
