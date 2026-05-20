package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func (h *Handler) partialMemoryList(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>No data source.</p></div>`)
		return
	}

	mems, err := h.data.ListMemories(r.Context(), wsID, 100)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div style="margin-bottom:1rem;display:flex;gap:0.5rem"><button class="btn btn-primary" hx-get="/ui/partials/memory-imprint-form?ws=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">New Memory</button>`, template.HTMLEscapeString(wsID))
	fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/upload-form?ws=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Upload Document</button>`, template.HTMLEscapeString(wsID))
	fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/bulk-ops-form?ws=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Bulk Ops</button></div>`, template.HTMLEscapeString(wsID))
	fmt.Fprint(w, `<div id="mem-modal-container"></div>`)

	if len(mems) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Memories</h3><p>Click "New Memory" above to imprint content.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>ID</th><th>Agent</th><th>Recall Ready</th><th>Content (preview)</th><th>Updated</th></tr></thead><tbody>`)
	for _, m := range mems {
		readyBadge := `<span class="badge badge-ok">Yes</span>`
		if !m.RecallReady {
			readyBadge = `<span class="badge badge-warn">Pending</span>`
		}
		fmt.Fprintf(w, `<tr><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td><td class="mono">%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(wsID),
			template.HTMLEscapeString(m.ID),
			template.HTMLEscapeString(truncateStr(m.ID, 16)),
			template.HTMLEscapeString(truncateStr(m.AgentID, 16)),
			readyBadge,
			template.HTMLEscapeString(truncateStr(m.Content, 60)),
			template.HTMLEscapeString(m.UpdatedAt.UTC().Format("Jan 2 15:04")),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) partialRecallResults(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	query := r.URL.Query().Get("q")
	mode := r.URL.Query().Get("mode")
	kStr := r.URL.Query().Get("k")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" || query == "" {
		fmt.Fprint(w, `<p class="text-muted">Enter a query to search.</p>`)
		return
	}
	if mode == "" {
		mode = "hybrid"
	}
	k, _ := strconv.Atoi(kStr)
	if k <= 0 {
		k = 10
	}

	resp, err := h.data.Recall(r.Context(), wsID, query, mode, k)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Recall error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	h.renderRecallResults(w, wsID, resp)
}

func (h *Handler) partialRecallFull(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	mode := r.URL.Query().Get("mode")
	kStr := r.URL.Query().Get("k")
	collectionID := strings.TrimSpace(r.URL.Query().Get("collection_id"))
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	tsAfterStr := strings.TrimSpace(r.URL.Query().Get("ts_after"))
	tsBeforeStr := strings.TrimSpace(r.URL.Query().Get("ts_before"))
	graphDepthStr := r.URL.Query().Get("graph_depth")
	graphDir := r.URL.Query().Get("graph_direction")
	includeCells := r.URL.Query().Get("include_cells") == "on"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || wsID == "" || query == "" {
		fmt.Fprint(w, `<p class="text-muted">Enter a query to search.</p>`)
		return
	}
	if mode == "" {
		mode = "hybrid"
	}
	k, _ := strconv.Atoi(kStr)
	if k <= 0 {
		k = 10
	}

	req := api.RecallRequest{
		Query:        query,
		Mode:         api.RecallMode(mode),
		K:            k,
		IncludeCells: includeCells,
	}

	if collectionID != "" || agentID != "" || tsAfterStr != "" || tsBeforeStr != "" {
		req.Filters = api.RecallFilters{
			CollectionID: collectionID,
			AgentID:      agentID,
		}
		if tsAfterStr != "" {
			if t, err := time.Parse("2006-01-02", tsAfterStr); err == nil {
				req.Filters.TsAfter = &t
			}
		}
		if tsBeforeStr != "" {
			if t, err := time.Parse("2006-01-02", tsBeforeStr); err == nil {
				req.Filters.TsBefore = &t
			}
		}
	}

	graphDepth, _ := strconv.Atoi(graphDepthStr)
	if graphDepth > 0 {
		if graphDir == "" {
			graphDir = "out"
		}
		req.GraphExpansion = &api.GraphExpansion{
			Depth:     graphDepth,
			Direction: api.GraphDirection(graphDir),
		}
	}

	resp, err := h.data.RecallFull(r.Context(), wsID, req)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Recall error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	h.renderRecallResults(w, wsID, resp)
}

func (h *Handler) partialRecallQueryBar(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if wsID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>No workspace selected.</p></div>`)
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

	fmt.Fprintf(w, `<div class="card" id="recall-query-bar">
<h3 class="card-title">Recall Query</h3>
<div style="display:flex;gap:0.75rem;flex-wrap:wrap;align-items:end">
  <label style="flex:1;min-width:200px">Query
    <input type="text" name="q" id="recall-q" placeholder="Search memories..." required>
  </label>
  <label>Mode
    <select name="mode" id="recall-mode">
      <option value="hybrid" selected>hybrid</option>
      <option value="vector">vector</option>
      <option value="keyword">keyword</option>
      <option value="lookup">lookup</option>
    </select>
  </label>
  <label>K
    <input type="number" name="k" id="recall-k" value="10" min="1" max="100" style="width:70px">
  </label>
  <button type="button" class="btn btn-primary" id="recall-search-btn"
    hx-get="/ui/partials/recall-full" hx-target="#recall-results" hx-swap="innerHTML"
    hx-include="#recall-query-bar input, #recall-query-bar select"
    hx-vals='{"ws":"%s"}'>Search</button>
  <button type="button" class="btn btn-secondary" onclick="document.getElementById('recall-filters').style.display=document.getElementById('recall-filters').style.display==='none'?'block':'none'">Filters</button>
</div>
<div id="recall-filters" style="display:none;margin-top:0.75rem;padding-top:0.75rem;border-top:1px solid var(--border)">
  <div style="display:flex;gap:0.75rem;flex-wrap:wrap;align-items:end">
    <label>Collection
      <select name="collection_id" id="recall-coll">
        <option value="">(any)</option>
        %s
      </select>
    </label>
    <label>Agent ID
      <input type="text" name="agent_id" id="recall-agent" placeholder="(any)">
    </label>
    <label>After
      <input type="date" name="ts_after" id="recall-after">
    </label>
    <label>Before
      <input type="date" name="ts_before" id="recall-before">
    </label>
  </div>
  <div style="display:flex;gap:0.75rem;flex-wrap:wrap;align-items:end;margin-top:0.5rem">
    <label>Graph Depth
      <input type="number" name="graph_depth" id="recall-gdepth" value="0" min="0" max="5" style="width:70px">
    </label>
    <label>Graph Direction
      <select name="graph_direction" id="recall-gdir">
        <option value="out">out</option>
        <option value="in">in</option>
        <option value="both">both</option>
      </select>
    </label>
    <label style="display:flex;align-items:center;gap:0.5rem;padding-top:1.4rem">
      <input type="checkbox" name="include_cells" id="recall-cells"> Include cells
    </label>
  </div>
</div>
</div>
<div id="recall-results" style="margin-top:1rem">
  <p class="text-muted">Enter a query to search.</p>
</div>`, template.HTMLEscapeString(wsID), collOptions)
}

func (h *Handler) renderRecallResults(w http.ResponseWriter, wsID string, resp *api.RecallResponse) {
	if resp == nil || len(resp.Results) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Results</h3><p>Try a different query or mode.</p></div>`)
		return
	}

	metaParts := []string{
		fmt.Sprintf("%d results", len(resp.Results)),
		fmt.Sprintf("%d candidates scanned", resp.TotalCandidatesScanned),
		fmt.Sprintf("%d ms", resp.LatencyMS),
	}
	if resp.GraphNodesExpanded > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d graph nodes expanded", resp.GraphNodesExpanded))
	}
	if resp.FederationID != "" {
		metaParts = append(metaParts, "federation: "+resp.FederationID)
	}
	if resp.PartialSuccess {
		metaParts = append(metaParts, "partial success")
	}
	fmt.Fprintf(w, `<p class="text-muted mb-1">%s</p>`, template.HTMLEscapeString(strings.Join(metaParts, " · ")))

	if resp.EmbeddingPending {
		fmt.Fprint(w, `<p class="text-muted" style="font-style:italic">Some memories are still being embedded — results may be incomplete.</p>`)
	}

	hasGraph := false
	for _, hit := range resp.Results {
		if hit.GraphProvenance != nil {
			hasGraph = true
			break
		}
	}

	fmt.Fprint(w, `<table><thead><tr><th>Memory</th><th>Score</th><th>Via</th>`)
	if hasGraph {
		fmt.Fprint(w, `<th>Graph Path</th>`)
	}
	fmt.Fprint(w, `<th>Text (preview)</th></tr></thead><tbody>`)
	for _, hit := range resp.Results {
		via := hit.Via
		if hit.PeerID != "" {
			via = "fed:" + hit.PeerID
		}
		fmt.Fprintf(w, `<tr><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td><td class="text-right">%.3f</td><td>%s</td>`,
			template.HTMLEscapeString(wsID),
			template.HTMLEscapeString(hit.MemoryID),
			template.HTMLEscapeString(truncateStr(hit.MemoryID, 16)),
			hit.Score,
			template.HTMLEscapeString(via),
		)
		if hasGraph {
			if hit.GraphProvenance != nil {
				fmt.Fprintf(w, `<td class="mono">%s → %s (L%d)</td>`,
					template.HTMLEscapeString(truncateStr(hit.GraphProvenance.FromMemoryID, 12)),
					template.HTMLEscapeString(hit.GraphProvenance.EdgeType),
					hit.GraphProvenance.Layer)
			} else {
				fmt.Fprint(w, `<td>—</td>`)
			}
		}
		fmt.Fprintf(w, `<td>%s</td></tr>`,
			template.HTMLEscapeString(truncateStr(hit.Text, 80)),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) partialMemoryDetail(w http.ResponseWriter, r *http.Request) {
	memID := r.URL.Query().Get("id")
	wsID := r.URL.Query().Get("ws")
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "content"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil || memID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>Memory not found.</p></div>`)
		return
	}

	switch tab {
	case "content":
		h.renderMemoryContent(w, r, memID)
	case "edges":
		h.renderMemoryEdges(w, r, wsID, memID)
	case "cells":
		h.renderMemoryCells(w, r, memID)
	case "watermarks":
		h.renderMemoryWatermarks(w, r, wsID, memID)
	default:
		h.renderMemoryContent(w, r, memID)
	}
}

func (h *Handler) renderMemoryContent(w http.ResponseWriter, r *http.Request, memID string) {
	mem, err := h.data.GetMemory(r.Context(), memID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	readyBadge := `<span class="badge badge-ok">Yes</span>`
	if !mem.RecallReady {
		readyBadge = `<span class="badge badge-warn">Pending</span>`
	}

	wsID := r.URL.Query().Get("ws")
	fmt.Fprint(w, `<div class="card"><div style="display:flex;justify-content:space-between;align-items:center"><h3 class="card-title" style="margin:0">Memory Details</h3>`)
	if wsID != "" {
		fmt.Fprintf(w, `<div style="display:flex;gap:0.5rem">`)
		fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/memory-edit-form?ws=%s&amp;id=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Edit</button>`,
			template.HTMLEscapeString(wsID), template.HTMLEscapeString(mem.ID))
		fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/memory-patch-form?ws=%s&amp;id=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Patch</button>`,
			template.HTMLEscapeString(wsID), template.HTMLEscapeString(mem.ID))
		fmt.Fprintf(w, `<button class="btn" hx-get="/ui/partials/memory-append-form?ws=%s&amp;id=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Append</button>`,
			template.HTMLEscapeString(wsID), template.HTMLEscapeString(mem.ID))
		fmt.Fprintf(w, `<button class="btn" style="color:var(--danger);border-color:var(--danger)" hx-get="/ui/partials/memory-forget-form?ws=%s&amp;id=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">Forget</button>`,
			template.HTMLEscapeString(wsID), template.HTMLEscapeString(mem.ID))
		fmt.Fprintf(w, `</div>`)
	}
	fmt.Fprint(w, `</div><div id="mem-modal-container"></div><table>`)
	fmt.Fprintf(w, `<tr><td><strong>ID</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(mem.ID))
	fmt.Fprintf(w, `<tr><td><strong>Watermark</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(mem.Watermark))
	fmt.Fprintf(w, `<tr><td><strong>Agent</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(mem.AgentID))
	fmt.Fprintf(w, `<tr><td><strong>Recall Ready</strong></td><td>%s</td></tr>`, readyBadge)
	fmt.Fprintf(w, `<tr><td><strong>Content MD5</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(mem.ContentMD5))
	fmt.Fprintf(w, `<tr><td><strong>Created</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(mem.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC")))
	fmt.Fprintf(w, `<tr><td><strong>Updated</strong></td><td>%s</td></tr>`, template.HTMLEscapeString(mem.UpdatedAt.UTC().Format("2006-01-02 15:04:05 UTC")))
	if len(mem.Tags) > 0 {
		var tags []string
		for k, v := range mem.Tags {
			tags = append(tags, k+"="+v)
		}
		fmt.Fprintf(w, `<tr><td><strong>Tags</strong></td><td class="mono">%s</td></tr>`, template.HTMLEscapeString(strings.Join(tags, ", ")))
	}
	fmt.Fprint(w, `</table></div>`)

	fmt.Fprint(w, `<div class="card mt-2"><h3 class="card-title">Content</h3>`)
	fmt.Fprintf(w, `<pre style="white-space:pre-wrap;word-break:break-word;max-height:400px;overflow:auto">%s</pre>`, template.HTMLEscapeString(mem.Content))
	fmt.Fprint(w, `</div>`)
}

func (h *Handler) renderMemoryEdges(w http.ResponseWriter, r *http.Request, wsID, memID string) {
	edges, err := h.data.GetEdges(r.Context(), wsID, memID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprintf(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/edge-link-form?ws=%s&amp;source=%s" hx-target="#edge-modal-container" hx-swap="innerHTML">Link Edge</button></div>`,
		template.HTMLEscapeString(wsID), template.HTMLEscapeString(memID))
	fmt.Fprint(w, `<div id="edge-modal-container"></div>`)

	if len(edges) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Edges</h3><p>Click "Link Edge" above to create a connection.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Edge ID</th><th>Type</th><th>Source</th><th>Target</th><th>Agent</th><th></th></tr></thead><tbody>`)
	for _, e := range edges {
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="mono">%s</td><td><button class="btn" style="color:var(--danger);border-color:var(--danger);padding:0.25rem 0.5rem;font-size:0.85rem" hx-get="/ui/partials/edge-unlink-form?ws=%s&amp;id=%s" hx-target="#edge-modal-container" hx-swap="innerHTML">Unlink</button></td></tr>`,
			template.HTMLEscapeString(truncateStr(e.EdgeID, 16)),
			template.HTMLEscapeString(e.EdgeType),
			template.HTMLEscapeString(truncateStr(e.SourceMemoryID, 16)),
			template.HTMLEscapeString(truncateStr(e.TargetMemoryID, 16)),
			template.HTMLEscapeString(truncateStr(e.AgentID, 16)),
			template.HTMLEscapeString(wsID),
			template.HTMLEscapeString(e.EdgeID),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) renderMemoryCells(w http.ResponseWriter, r *http.Request, memID string) {
	cells, err := h.data.GetCells(r.Context(), memID)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if len(cells) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Cells</h3><p>This memory has not been chunked yet.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>#</th><th>Cell ID</th><th>Text MD5</th><th>Text (preview)</th></tr></thead><tbody>`)
	for _, c := range cells {
		fmt.Fprintf(w, `<tr><td>%d</td><td class="mono">%s</td><td class="mono">%s</td><td>%s</td></tr>`,
			c.Sequence,
			template.HTMLEscapeString(truncateStr(c.CellID, 16)),
			template.HTMLEscapeString(truncateStr(c.TextMD5, 12)),
			template.HTMLEscapeString(truncateStr(c.Text, 80)),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)
}

func (h *Handler) renderMemoryWatermarks(w http.ResponseWriter, r *http.Request, wsID, memID string) {
	since := time.Now().AddDate(0, 0, -7)
	hist, err := h.data.GetWatermarkHistory(r.Context(), wsID, memID, since)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	fmt.Fprint(w, `<div class="card"><h3 class="card-title">Watermark History</h3>`)
	fmt.Fprint(w, `<p class="text-muted" style="font-size:0.85rem;margin-bottom:0.75rem">Last 7 days of version history for this memory.</p>`)

	if len(hist) == 0 {
		fmt.Fprint(w, `<p class="text-muted">No watermark history found.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Watermark</th><th>Op</th><th>Agent</th><th>MD5 Before</th><th>MD5 After</th><th>Time</th></tr></thead><tbody>`)
	for _, e := range hist {
		md5Before := "—"
		if e.ContentMD5Before != "" {
			md5Before = truncateStr(e.ContentMD5Before, 12)
		}
		md5After := "—"
		if e.ContentMD5After != "" {
			md5After = truncateStr(e.ContentMD5After, 12)
		}
		opBadge := template.HTMLEscapeString(e.Op)
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="mono">%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(truncateStr(e.Watermark, 20)),
			opBadge,
			template.HTMLEscapeString(truncateStr(e.AgentID, 20)),
			template.HTMLEscapeString(md5Before),
			template.HTMLEscapeString(md5After),
			template.HTMLEscapeString(e.CreatedAt.UTC().Format("2006-01-02 15:04:05")),
		)
	}
	fmt.Fprint(w, `</tbody></table></div>`)
}
