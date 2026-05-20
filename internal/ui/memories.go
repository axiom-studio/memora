package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
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

	fmt.Fprintf(w, `<div style="margin-bottom:1rem"><button class="btn btn-primary" hx-get="/ui/partials/memory-imprint-form?ws=%s" hx-target="#mem-modal-container" hx-swap="innerHTML">New Memory</button></div>`, template.HTMLEscapeString(wsID))
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
	if len(resp.Results) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Results</h3><p>Try a different query or mode.</p></div>`)
		return
	}

	fmt.Fprintf(w, `<p class="text-muted mb-1">%d results · %d candidates scanned · %d ms</p>`,
		len(resp.Results), resp.TotalCandidatesScanned, resp.LatencyMS)
	fmt.Fprint(w, `<table><thead><tr><th>Memory</th><th>Score</th><th>Via</th><th>Text (preview)</th></tr></thead><tbody>`)
	for _, hit := range resp.Results {
		via := hit.Via
		if hit.PeerID != "" {
			via = "fed:" + hit.PeerID
		}
		fmt.Fprintf(w, `<tr><td class="mono"><a href="/ui/workspaces/%s/memories/%s">%s</a></td><td class="text-right">%.3f</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(wsID),
			template.HTMLEscapeString(hit.MemoryID),
			template.HTMLEscapeString(truncateStr(hit.MemoryID, 16)),
			hit.Score,
			template.HTMLEscapeString(via),
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
	if len(edges) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Edges</h3><p>This memory has no graph connections.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Edge ID</th><th>Type</th><th>Source</th><th>Target</th><th>Agent</th></tr></thead><tbody>`)
	for _, e := range edges {
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="mono">%s</td></tr>`,
			template.HTMLEscapeString(truncateStr(e.EdgeID, 16)),
			template.HTMLEscapeString(e.EdgeType),
			template.HTMLEscapeString(truncateStr(e.SourceMemoryID, 16)),
			template.HTMLEscapeString(truncateStr(e.TargetMemoryID, 16)),
			template.HTMLEscapeString(truncateStr(e.AgentID, 16)),
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
