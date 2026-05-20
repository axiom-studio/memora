package ui

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) partialAuditList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.data == nil {
		fmt.Fprint(w, `<div class="empty-state"><p>No data source.</p></div>`)
		return
	}

	wsID := r.URL.Query().Get("ws")
	agentID := r.URL.Query().Get("agent")
	opsRaw := r.URL.Query().Get("ops")
	sinceRaw := r.URL.Query().Get("since")
	untilRaw := r.URL.Query().Get("until")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")

	var ops []string
	if opsRaw != "" {
		ops = strings.Split(opsRaw, ",")
	}
	var since, until *time.Time
	if sinceRaw != "" {
		if t, err := time.Parse("2006-01-02", sinceRaw); err == nil {
			since = &t
		}
	}
	if untilRaw != "" {
		if t, err := time.Parse("2006-01-02", untilRaw); err == nil {
			eod := t.Add(24*time.Hour - time.Nanosecond)
			until = &eod
		}
	}
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 {
		limit = 50
	}

	entries, nextCursor, err := h.data.AuditQuery(r.Context(), wsID, agentID, ops, since, until, cursor, limit)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if len(entries) == 0 && cursor == "" {
		fmt.Fprint(w, `<div class="empty-state"><h3>No Entries</h3><p>No audit log entries match the current filters.</p></div>`)
		return
	}

	fmt.Fprint(w, `<table><thead><tr><th>Time</th><th>Op</th><th>Target</th><th>Agent</th><th>Workspace</th><th>Latency</th><th></th></tr></thead><tbody>`)
	for _, e := range entries {
		fmt.Fprintf(w, `<tr><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="text-right">%dms</td>`+
			`<td><a hx-get="/ui/partials/audit-detail?id=%s" hx-target="#audit-detail" hx-swap="innerHTML" style="cursor:pointer">detail</a></td></tr>`,
			template.HTMLEscapeString(e.Timestamp.UTC().Format("2006-01-02 15:04:05")),
			template.HTMLEscapeString(e.Op),
			template.HTMLEscapeString(truncateStr(e.Target, 24)),
			template.HTMLEscapeString(truncateStr(e.AgentID, 20)),
			template.HTMLEscapeString(truncateStr(e.WorkspaceID, 16)),
			e.LatencyMS,
			template.HTMLEscapeString(e.LedgerID),
		)
	}
	fmt.Fprint(w, `</tbody></table>`)

	if nextCursor != "" {
		qp := buildAuditQP(wsID, agentID, opsRaw, sinceRaw, untilRaw, nextCursor, limit)
		fmt.Fprintf(w, `<div style="margin-top:0.5rem;text-align:center"><button class="btn btn-secondary" hx-get="/ui/partials/audit-list?%s" hx-target="#audit-results" hx-swap="innerHTML">Load More</button></div>`, template.HTMLEscapeString(qp))
	}
}

func (h *Handler) partialAuditDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ledgerID := r.URL.Query().Get("id")

	if h.data == nil || ledgerID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>Select an entry to view details.</p></div>`)
		return
	}

	entries, _, err := h.data.AuditQuery(r.Context(), "", "", nil, nil, nil, "", 1)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty-state"><p>Error: %s</p></div>`, template.HTMLEscapeString(err.Error()))
		return
	}

	var found bool
	for _, e := range entries {
		if e.LedgerID == ledgerID {
			b, _ := json.MarshalIndent(e, "", "  ")
			fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Entry %s</h3><pre style="white-space:pre-wrap;word-break:break-word;max-height:400px;overflow:auto">%s</pre></div>`,
				template.HTMLEscapeString(truncateStr(ledgerID, 16)),
				template.HTMLEscapeString(string(b)),
			)
			found = true
			break
		}
	}
	if !found {
		// Fetch by scanning recent entries — ledger doesn't have get-by-id
		allEntries, _, _ := h.data.AuditQuery(r.Context(), "", "", nil, nil, nil, "", 500)
		for _, e := range allEntries {
			if e.LedgerID == ledgerID {
				b, _ := json.MarshalIndent(e, "", "  ")
				fmt.Fprintf(w, `<div class="card"><h3 class="card-title">Entry %s</h3><pre style="white-space:pre-wrap;word-break:break-word;max-height:400px;overflow:auto">%s</pre></div>`,
					template.HTMLEscapeString(truncateStr(ledgerID, 16)),
					template.HTMLEscapeString(string(b)),
				)
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(w, `<div class="empty-state"><p>Entry %s not found.</p></div>`, template.HTMLEscapeString(truncateStr(ledgerID, 24)))
		}
	}
}

func (h *Handler) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	wsID := r.URL.Query().Get("ws")
	agentID := r.URL.Query().Get("agent")
	opsRaw := r.URL.Query().Get("ops")
	sinceRaw := r.URL.Query().Get("since")
	untilRaw := r.URL.Query().Get("until")

	var ops []string
	if opsRaw != "" {
		ops = strings.Split(opsRaw, ",")
	}
	var since, until *time.Time
	if sinceRaw != "" {
		if t, err := time.Parse("2006-01-02", sinceRaw); err == nil {
			since = &t
		}
	}
	if untilRaw != "" {
		if t, err := time.Parse("2006-01-02", untilRaw); err == nil {
			eod := t.Add(24*time.Hour - time.Nanosecond)
			until = &eod
		}
	}

	if h.data == nil {
		http.Error(w, "no data source", http.StatusServiceUnavailable)
		return
	}

	entries, _, err := h.data.AuditQuery(r.Context(), wsID, agentID, ops, since, until, "", 10000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=audit-log.csv")
		cw := csv.NewWriter(w)
		cw.Write([]string{"ledger_id", "timestamp", "op", "target", "agent_id", "workspace_id", "latency_ms", "request_id"})
		for _, e := range entries {
			cw.Write([]string{
				e.LedgerID,
				e.Timestamp.UTC().Format(time.RFC3339),
				e.Op,
				e.Target,
				e.AgentID,
				e.WorkspaceID,
				strconv.Itoa(e.LatencyMS),
				e.RequestID,
			})
		}
		cw.Flush()
	default:
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", "attachment; filename=audit-log.ndjson")
		enc := json.NewEncoder(w)
		for _, e := range entries {
			enc.Encode(e)
		}
	}
}

func buildAuditQP(wsID, agentID, ops, since, until, cursor string, limit int) string {
	var parts []string
	if wsID != "" {
		parts = append(parts, "ws="+wsID)
	}
	if agentID != "" {
		parts = append(parts, "agent="+agentID)
	}
	if ops != "" {
		parts = append(parts, "ops="+ops)
	}
	if since != "" {
		parts = append(parts, "since="+since)
	}
	if until != "" {
		parts = append(parts, "until="+until)
	}
	if cursor != "" {
		parts = append(parts, "cursor="+cursor)
	}
	if limit > 0 {
		parts = append(parts, "limit="+strconv.Itoa(limit))
	}
	return strings.Join(parts, "&")
}
