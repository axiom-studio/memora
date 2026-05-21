package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

const maxBulkUploadBytes = 64 << 20 // 64 MiB

type seedRecord struct {
	Content      string            `json:"content"`
	Tags         map[string]string `json:"tags,omitempty"`
	ChunkerID    string            `json:"chunker_id,omitempty"`
	CollectionID string            `json:"collection_id,omitempty"`
}

type dumpRecord struct {
	MemoryID     string            `json:"memory_id"`
	Content      string            `json:"content"`
	ContentMD5   string            `json:"content_md5"`
	AgentID      string            `json:"agent_id"`
	Tags         map[string]string `json:"tags,omitempty"`
	CollectionID string            `json:"collection_id,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

func (h *Handler) partialBulkOpsForm(w http.ResponseWriter, r *http.Request) {
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

	fmt.Fprintf(w, `<dialog id="bulk-modal" class="modal" aria-labelledby="bulk-title">
<div class="modal-form" style="max-width:800px">
  <h3 id="bulk-title">Bulk Operations</h3>
  <div class="tabs" style="margin-bottom:1rem">
    <a class="tab active" onclick="showBulkTab('seed',this)">Seed</a>
    <a class="tab" onclick="showBulkTab('dump',this)">Dump</a>
    <a class="tab" onclick="showBulkTab('import',this)">Import</a>
    <a class="tab" onclick="showBulkTab('replay',this)">Replay</a>
  </div>

  <div id="bulk-seed" class="bulk-panel">
    <p class="text-muted" style="font-size:0.85rem;margin-bottom:0.75rem">Upload NDJSON file with one record per line. Each line: <code>{"content":"...","tags":{},"chunker_id":"","collection_id":""}</code></p>
    <input type="file" id="seed-file" accept=".jsonl,.ndjson,.json">
    <div id="seed-preview" style="display:none;margin:0.75rem 0;padding:0.5rem;background:var(--bg-secondary);border-radius:4px;font-size:0.85rem"></div>
    <label>Default Collection
      <select id="seed-collection">
        <option value="">(none)</option>
        %s
      </select>
    </label>
    <label>Default Chunker
      <select id="seed-chunker">
        <option value="">default</option>
        <option value="markdown">markdown</option>
        <option value="noop">no-chunk</option>
      </select>
    </label>
    <label style="display:flex;align-items:center;gap:0.5rem"><input type="checkbox" id="seed-autolink"> Enable auto-link</label>
    <div id="seed-progress" class="bulk-progress" style="display:none"></div>
    <div id="seed-result"></div>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="button" class="btn btn-primary" id="seed-btn" disabled onclick="startBulkSeed()">Seed</button>
    </div>
  </div>

  <div id="bulk-dump" class="bulk-panel" style="display:none">
    <p class="text-muted" style="font-size:0.85rem;margin-bottom:0.75rem">Export all memories as NDJSON. One JSON object per line.</p>
    <label>Filter by Collection
      <select id="dump-collection">
        <option value="">(all)</option>
        %s
      </select>
    </label>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="button" class="btn btn-primary" onclick="startBulkDump()">Export</button>
    </div>
  </div>

  <div id="bulk-import" class="bulk-panel" style="display:none">
    <p class="text-muted" style="font-size:0.85rem;margin-bottom:0.75rem">Import a dump NDJSON file. Records are imprinted as new memories.</p>
    <input type="file" id="import-file" accept=".jsonl,.ndjson,.json">
    <div id="import-preview" style="display:none;margin:0.75rem 0;padding:0.5rem;background:var(--bg-secondary);border-radius:4px;font-size:0.85rem"></div>
    <label>Conflict Handling
      <select id="import-conflict">
        <option value="skip">Skip duplicates (by content MD5)</option>
        <option value="overwrite">Overwrite duplicates</option>
        <option value="error">Error on duplicates</option>
      </select>
    </label>
    <div id="import-progress" class="bulk-progress" style="display:none"></div>
    <div id="import-result"></div>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="button" class="btn btn-primary" id="import-btn" disabled onclick="startBulkImport()">Import</button>
    </div>
  </div>

  <div id="bulk-replay" class="bulk-panel" style="display:none">
    <p class="text-muted" style="font-size:0.85rem;margin-bottom:0.75rem">Upload a ledger NDJSON file to replay operations. Each line must have <code>op</code>, <code>target</code>, and operation-specific fields.</p>
    <input type="file" id="replay-file" accept=".jsonl,.ndjson,.json">
    <div id="replay-preview" style="display:none;margin:0.75rem 0;padding:0.5rem;background:var(--bg-secondary);border-radius:4px;font-size:0.85rem"></div>
    <div id="replay-progress" class="bulk-progress" style="display:none"></div>
    <div id="replay-result"></div>
    <div class="modal-actions">
      <button type="button" class="btn btn-secondary" onclick="this.closest('dialog').close()">Cancel</button>
      <button type="button" class="btn btn-primary" id="replay-btn" disabled onclick="startBulkReplay()">Replay</button>
    </div>
  </div>
</div>
</dialog>
<script>
var bulkWsID = %q;

function showBulkTab(name, el) {
  document.querySelectorAll('.bulk-panel').forEach(function(p) { p.style.display = 'none'; });
  document.querySelectorAll('.tabs .tab').forEach(function(t) { t.classList.remove('active'); });
  document.getElementById('bulk-' + name).style.display = 'block';
  if (el) el.classList.add('active');
}

function renderBulkProgress(id, done, total, failed) {
  var el = document.getElementById(id);
  el.style.display = 'block';
  var pct = total > 0 ? Math.round((done / total) * 100) : 0;
  el.innerHTML = '<div style="display:flex;justify-content:space-between;font-size:0.85rem;margin-bottom:0.25rem"><span>' + done + '/' + total + (failed > 0 ? ' (' + failed + ' failed)' : '') + '</span><span>' + pct + '%%</span></div>'
    + '<div style="height:6px;background:var(--border);border-radius:3px;overflow:hidden"><div style="height:100%%;background:var(--accent);width:' + pct + '%%;transition:width 0.3s"></div></div>';
}

function readNDJSON(file, callback) {
  var reader = new FileReader();
  reader.onload = function(e) {
    var lines = e.target.result.split('\n').filter(function(l) { return l.trim() !== ''; });
    var records = [];
    for (var i = 0; i < lines.length; i++) {
      try { records.push(JSON.parse(lines[i])); }
      catch(ex) { records.push({_error: 'parse error on line ' + (i+1)}); }
    }
    callback(records);
  };
  reader.readAsText(file);
}

document.getElementById('seed-file').onchange = function() {
  var file = this.files[0];
  if (!file) return;
  readNDJSON(file, function(records) {
    var valid = records.filter(function(r) { return !r._error && r.content; });
    document.getElementById('seed-preview').style.display = 'block';
    document.getElementById('seed-preview').textContent = valid.length + ' valid records found (' + (file.size / 1024).toFixed(1) + ' KiB)';
    document.getElementById('seed-btn').disabled = valid.length === 0;
    window._seedRecords = valid;
  });
};

document.getElementById('import-file').onchange = function() {
  var file = this.files[0];
  if (!file) return;
  readNDJSON(file, function(records) {
    var valid = records.filter(function(r) { return !r._error && r.content; });
    document.getElementById('import-preview').style.display = 'block';
    document.getElementById('import-preview').textContent = valid.length + ' valid records found (' + (file.size / 1024).toFixed(1) + ' KiB)';
    document.getElementById('import-btn').disabled = valid.length === 0;
    window._importRecords = valid;
  });
};

document.getElementById('replay-file').onchange = function() {
  var file = this.files[0];
  if (!file) return;
  readNDJSON(file, function(records) {
    var valid = records.filter(function(r) { return !r._error && r.op; });
    document.getElementById('replay-preview').style.display = 'block';
    document.getElementById('replay-preview').textContent = valid.length + ' operations found (' + (file.size / 1024).toFixed(1) + ' KiB)';
    document.getElementById('replay-btn').disabled = valid.length === 0;
    window._replayRecords = valid;
  });
};

function streamBulkOp(url, body, progressID, resultID, btnID) {
  var btn = document.getElementById(btnID);
  btn.disabled = true;
  btn.textContent = 'Processing...';
  document.getElementById(resultID).innerHTML = '';

  fetch(url, {method: 'POST', body: body})
    .then(function(resp) {
      var reader = resp.body.getReader();
      var decoder = new TextDecoder();
      var buf = '';
      function read() {
        reader.read().then(function(result) {
          if (result.done) return;
          buf += decoder.decode(result.value, {stream: true});
          var lines = buf.split('\n');
          buf = lines.pop();
          for (var i = 0; i < lines.length; i++) {
            var line = lines[i];
            if (line.indexOf('data: ') === 0) {
              try {
                var evt = JSON.parse(line.substring(6));
                if (evt.type === 'progress') {
                  renderBulkProgress(progressID, evt.done, evt.total, evt.failed);
                } else if (evt.type === 'complete') {
                  document.getElementById(resultID).innerHTML = '<div class="card mt-1"><h3 class="card-title">Complete</h3><table>'
                    + '<tr><td><strong>Succeeded</strong></td><td>' + evt.succeeded + '</td></tr>'
                    + '<tr><td><strong>Failed</strong></td><td>' + evt.failed + '</td></tr>'
                    + '<tr><td><strong>Skipped</strong></td><td>' + (evt.skipped || 0) + '</td></tr>'
                    + '</table></div>';
                  btn.textContent = 'Done';
                  btn.disabled = false;
                  btn.onclick = function() { btn.closest('dialog').close(); };
                } else if (evt.type === 'error') {
                  document.getElementById(resultID).innerHTML = '<div class="form-error">' + evt.message + '</div>';
                  btn.textContent = 'Error';
                }
              } catch(ex) {}
            }
          }
          read();
        });
      }
      read();
    })
    .catch(function() {
      document.getElementById(resultID).innerHTML = '<div class="form-error">Network error</div>';
      btn.textContent = 'Error';
    });
}

function startBulkSeed() {
  if (!window._seedRecords || !window._seedRecords.length) return;
  var fd = new FormData();
  fd.append('workspace_id', bulkWsID);
  fd.append('collection_id', document.getElementById('seed-collection').value);
  fd.append('chunker_id', document.getElementById('seed-chunker').value);
  fd.append('auto_link', document.getElementById('seed-autolink').checked ? 'true' : 'false');
  fd.append('records', JSON.stringify(window._seedRecords));
  streamBulkOp('/ui/api/bulk/seed', fd, 'seed-progress', 'seed-result', 'seed-btn');
}

function startBulkDump() {
  var coll = document.getElementById('dump-collection').value;
  var url = '/ui/api/bulk/dump?ws=' + encodeURIComponent(bulkWsID);
  if (coll) url += '&collection=' + encodeURIComponent(coll);
  window.location.href = url;
}

function startBulkImport() {
  if (!window._importRecords || !window._importRecords.length) return;
  var fd = new FormData();
  fd.append('workspace_id', bulkWsID);
  fd.append('conflict', document.getElementById('import-conflict').value);
  fd.append('records', JSON.stringify(window._importRecords));
  streamBulkOp('/ui/api/bulk/import', fd, 'import-progress', 'import-result', 'import-btn');
}

function startBulkReplay() {
  if (!window._replayRecords || !window._replayRecords.length) return;
  var fd = new FormData();
  fd.append('workspace_id', bulkWsID);
  fd.append('records', JSON.stringify(window._replayRecords));
  streamBulkOp('/ui/api/bulk/replay', fd, 'replay-progress', 'replay-result', 'replay-btn');
}
</script>`, collOptions, collOptions, template.HTMLEscapeString(wsID))
}

func (h *Handler) handleBulkSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeBulkError(w, "Data source not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBulkUploadBytes)

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeBulkError(w, "Workspace ID is required")
		return
	}

	defaultCollection := strings.TrimSpace(r.FormValue("collection_id"))
	defaultChunker := strings.TrimSpace(r.FormValue("chunker_id"))
	autoLink := r.FormValue("auto_link") == "true"

	var records []seedRecord
	if err := json.Unmarshal([]byte(r.FormValue("records")), &records); err != nil {
		h.writeBulkError(w, "Invalid records JSON")
		return
	}

	if len(records) == 0 {
		h.writeBulkError(w, "No records to seed")
		return
	}

	h.streamBulkSSE(w, r, len(records), func(i int, progress func(done, failed int)) error {
		rec := records[i]
		collID := rec.CollectionID
		if collID == "" {
			collID = defaultCollection
		}
		chunker := rec.ChunkerID
		if chunker == "" {
			chunker = defaultChunker
		}
		req := api.ImprintRequest{
			Content:      rec.Content,
			Tags:         rec.Tags,
			CollectionID: collID,
			ChunkerID:    chunker,
			AutoLink:     &autoLink,
		}
		_, err := h.data.ImprintMemory(r.Context(), wsID, req)
		return err
	})
}

func (h *Handler) handleBulkDump(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		http.Error(w, "data source not configured", http.StatusInternalServerError)
		return
	}

	wsID := r.URL.Query().Get("ws")
	if wsID == "" {
		http.Error(w, "workspace ID required", http.StatusBadRequest)
		return
	}

	mems, err := h.data.ListMemories(r.Context(), wsID, 10000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-dump.jsonl", wsID))

	enc := json.NewEncoder(w)
	for _, m := range mems {
		_ = enc.Encode(dumpRecord{
			MemoryID:     m.ID,
			Content:      m.Content,
			ContentMD5:   m.ContentMD5,
			AgentID:      m.AgentID,
			Tags:         m.Tags,
			CreatedAt:    m.CreatedAt,
		})
	}
}

func (h *Handler) handleBulkImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeBulkError(w, "Data source not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBulkUploadBytes)

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeBulkError(w, "Workspace ID is required")
		return
	}

	conflict := strings.TrimSpace(r.FormValue("conflict"))
	if conflict == "" {
		conflict = "skip"
	}

	var records []dumpRecord
	if err := json.Unmarshal([]byte(r.FormValue("records")), &records); err != nil {
		h.writeBulkError(w, "Invalid records JSON")
		return
	}

	if len(records) == 0 {
		h.writeBulkError(w, "No records to import")
		return
	}

	existingMD5 := map[string]bool{}
	if conflict != "overwrite" {
		existing, _ := h.data.ListMemories(r.Context(), wsID, 10000)
		for _, m := range existing {
			existingMD5[m.ContentMD5] = true
		}
	}

	var skipped int
	h.streamBulkSSEWithSkip(w, r, len(records), func(i int, progress func(done, failed, skipped int)) (skip bool, err error) {
		rec := records[i]
		if conflict != "overwrite" && rec.ContentMD5 != "" && existingMD5[rec.ContentMD5] {
			if conflict == "error" {
				return false, fmt.Errorf("duplicate: %s", rec.ContentMD5)
			}
			return true, nil
		}
		req := api.ImprintRequest{
			Content:      rec.Content,
			Tags:         rec.Tags,
			CollectionID: rec.CollectionID,
		}
		_, err = h.data.ImprintMemory(r.Context(), wsID, req)
		return false, err
	}, &skipped)
}

type replayRecord struct {
	Op      string `json:"op"`
	Content string `json:"content,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
}

func (h *Handler) handleBulkReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.data == nil {
		h.writeBulkError(w, "Data source not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBulkUploadBytes)

	wsID := strings.TrimSpace(r.FormValue("workspace_id"))
	if wsID == "" {
		h.writeBulkError(w, "Workspace ID is required")
		return
	}

	var records []replayRecord
	if err := json.Unmarshal([]byte(r.FormValue("records")), &records); err != nil {
		h.writeBulkError(w, "Invalid records JSON")
		return
	}

	if len(records) == 0 {
		h.writeBulkError(w, "No records to replay")
		return
	}

	h.streamBulkSSE(w, r, len(records), func(i int, progress func(done, failed int)) error {
		rec := records[i]
		switch rec.Op {
		case "imprint":
			req := api.ImprintRequest{
				Content: rec.Content,
				Tags:    rec.Tags,
			}
			_, err := h.data.ImprintMemory(r.Context(), wsID, req)
			return err
		default:
			return fmt.Errorf("unsupported replay op: %s", rec.Op)
		}
	})
}

func (h *Handler) writeBulkError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"type": "error", "message": msg}))
}

func (h *Handler) streamBulkSSE(w http.ResponseWriter, r *http.Request, total int, process func(i int, progress func(done, failed int)) error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	var done, failed, succeeded int
	progress := func(d, f int) {}
	_ = progress

	for i := 0; i < total; i++ {
		if r.Context().Err() != nil {
			return
		}
		err := process(i, func(d, f int) {})
		done++
		if err != nil {
			failed++
		} else {
			succeeded++
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
			"type": "progress", "done": done, "total": total, "failed": failed,
		}))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
		"type": "complete", "succeeded": succeeded, "failed": failed, "skipped": 0,
	}))
	flusher.Flush()
}

func (h *Handler) streamBulkSSEWithSkip(w http.ResponseWriter, r *http.Request, total int, process func(i int, progress func(done, failed, skipped int)) (skip bool, err error), skippedOut *int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	var done, failed, succeeded, skipped int

	for i := 0; i < total; i++ {
		if r.Context().Err() != nil {
			return
		}
		skip, err := process(i, func(d, f, s int) {})
		done++
		if skip {
			skipped++
		} else if err != nil {
			failed++
		} else {
			succeeded++
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
			"type": "progress", "done": done, "total": total, "failed": failed,
		}))
		flusher.Flush()
	}

	if skippedOut != nil {
		*skippedOut = skipped
	}

	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
		"type": "complete", "succeeded": succeeded, "failed": failed, "skipped": skipped,
	}))
	flusher.Flush()
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// scanNDJSON is used by direct-upload variants (not the JS-parsed path).
func scanNDJSON(r io.Reader, maxLines int) ([]json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var out []json.RawMessage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		out = append(out, json.RawMessage(line))
		if maxLines > 0 && len(out) >= maxLines {
			break
		}
	}
	return out, scanner.Err()
}
