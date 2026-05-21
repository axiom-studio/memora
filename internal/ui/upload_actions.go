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
<div class="modal-form" style="max-width:700px">
  <h3>Upload Documents</h3>
  <div id="upload-result"></div>
  <div id="drop-zone" style="border:2px dashed var(--border);border-radius:8px;padding:2rem;text-align:center;cursor:pointer;margin-bottom:0.75rem;transition:border-color 0.2s"
    ondragover="event.preventDefault();this.style.borderColor='var(--primary)'"
    ondragleave="this.style.borderColor='var(--border)'"
    ondrop="event.preventDefault();this.style.borderColor='var(--border)';handleBatchFiles(event.dataTransfer.files)"
    onclick="document.getElementById('file-input').click()">
    <p style="margin:0;font-size:1.1rem;font-weight:600">Drop files here or click to browse</p>
    <p class="text-muted" style="margin:0.25rem 0 0;font-size:0.85rem">Accepted: .txt, .md (max 8 MiB each) · Multiple files supported</p>
  </div>
  <input type="file" id="file-input" accept=".txt,.md" style="display:none" multiple onchange="handleBatchFiles(this.files)">
  <div id="file-queue" style="display:none;margin-bottom:0.75rem;max-height:300px;overflow:auto"></div>
  <div id="batch-progress" style="display:none;margin-bottom:0.75rem">
    <div style="display:flex;justify-content:space-between;font-size:0.85rem;margin-bottom:0.25rem"><span id="batch-status">Uploading...</span><span id="batch-count">0/0</span></div>
    <div style="height:6px;background:var(--border);border-radius:3px;overflow:hidden"><div id="batch-bar" style="height:100%%;background:var(--primary);width:0%%;transition:width 0.3s"></div></div>
  </div>
  <label>Collection
    <select id="batch-collection">
      <option value="">(none)</option>
      %s
    </select>
  </label>
  <label>Chunker
    <select id="batch-chunker">
      <option value="">default</option>
      <option value="markdown">markdown</option>
      <option value="no-chunk">no-chunk</option>
    </select>
  </label>
  <div class="modal-actions">
    <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
    <button type="button" class="btn btn-primary" id="upload-submit" disabled onclick="startBatchUpload()">Upload All</button>
  </div>
</div>
</dialog>
<script>
var batchWsID = %q;
var batchFiles = [];
var MAX_FILE_SIZE = %d;
var CONCURRENCY = 4;

function handleBatchFiles(files) {
  if (!files || !files.length) return;
  var result = document.getElementById('upload-result');
  result.innerHTML = '';
  batchFiles = [];
  var queue = document.getElementById('file-queue');
  queue.innerHTML = '';
  queue.style.display = 'block';

  for (var i = 0; i < files.length; i++) {
    var file = files[i];
    var ext = file.name.substring(file.name.lastIndexOf('.')).toLowerCase();
    var status = 'pending';
    var reason = '';
    if (ext !== '.txt' && ext !== '.md') {
      status = 'skipped';
      reason = 'unsupported format';
    } else if (file.size > MAX_FILE_SIZE) {
      status = 'skipped';
      reason = 'too large (' + (file.size / 1048576).toFixed(1) + ' MiB)';
    }
    batchFiles.push({file: file, status: status, reason: reason, memoryID: ''});
    var sizeStr = (file.size / 1024).toFixed(1) + ' KiB';
    var badge = status === 'skipped'
      ? '<span class="badge badge-err">' + reason + '</span>'
      : '<span class="badge" id="fstatus-' + i + '">pending</span>';
    queue.innerHTML += '<div style="display:flex;justify-content:space-between;align-items:center;padding:0.3rem 0;border-bottom:1px solid var(--border);font-size:0.85rem" id="frow-' + i + '">'
      + '<span class="mono">' + escapeH(file.name) + '</span>'
      + '<span style="display:flex;gap:0.5rem;align-items:center"><span class="text-muted">' + sizeStr + '</span>' + badge + '</span></div>';
  }
  var uploadable = batchFiles.filter(function(f) { return f.status === 'pending'; });
  document.getElementById('upload-submit').disabled = uploadable.length === 0;
  document.getElementById('upload-submit').textContent = 'Upload All (' + uploadable.length + ')';
}

function startBatchUpload() {
  var btn = document.getElementById('upload-submit');
  btn.disabled = true;
  btn.textContent = 'Uploading...';
  document.getElementById('batch-progress').style.display = 'block';

  var collectionID = document.getElementById('batch-collection').value;
  var chunkerID = document.getElementById('batch-chunker').value;
  var pending = [];
  for (var i = 0; i < batchFiles.length; i++) {
    if (batchFiles[i].status === 'pending') pending.push(i);
  }
  var total = pending.length;
  var done = 0;
  var succeeded = 0;
  var failed = 0;

  function updateProgress() {
    done++;
    var pct = Math.round((done / total) * 100);
    document.getElementById('batch-bar').style.width = pct + '%%';
    document.getElementById('batch-count').textContent = done + '/' + total;
    if (done === total) {
      document.getElementById('batch-status').textContent = 'Complete: ' + succeeded + ' succeeded, ' + failed + ' failed';
      btn.textContent = 'Done';
      btn.disabled = false;
      btn.onclick = function() {
        btn.closest('dialog').close();
        if (succeeded > 0) { htmx.trigger(document.body, 'memoryListChanged'); }
      };
    }
  }

  var queue = pending.slice();
  var active = 0;
  function uploadFileWithSlot(idx) {
    active++;
    var entry = batchFiles[idx];
    var badge = document.getElementById('fstatus-' + idx);
    if (badge) { badge.textContent = 'uploading...'; badge.className = 'badge'; }

    var reader = new FileReader();
    reader.onload = function(e) {
      var fd = new FormData();
      fd.append('workspace_id', batchWsID);
      fd.append('content', e.target.result);
      fd.append('collection_id', collectionID);
      fd.append('chunker_id', chunkerID);
      fd.append('file', entry.file);

      fetch('/ui/api/memories/upload', {method: 'POST', body: fd})
        .then(function(r) { return r.text(); })
        .then(function(html) {
          if (html.indexOf('form-error') !== -1) {
            entry.status = 'failed';
            if (badge) { badge.textContent = 'failed'; badge.className = 'badge badge-err'; }
            failed++;
          } else {
            entry.status = 'done';
            if (badge) { badge.textContent = 'done'; badge.className = 'badge badge-ok'; }
            succeeded++;
          }
        })
        .catch(function() {
          entry.status = 'failed';
          if (badge) { badge.textContent = 'error'; badge.className = 'badge badge-err'; }
          failed++;
        })
        .finally(function() {
          active--;
          updateProgress();
          if (queue.length > 0) uploadFileWithSlot(queue.shift());
        });
    };
    reader.readAsText(entry.file);
  }

  for (var s = 0; s < Math.min(CONCURRENCY, pending.length); s++) {
    uploadFileWithSlot(queue.shift());
  }
}

function escapeH(s) {
  var d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}
</script>`, collOptions, template.HTMLEscapeString(wsID), maxUploadBytes)
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
