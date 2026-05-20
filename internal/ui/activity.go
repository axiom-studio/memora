package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"time"
)

func (h *Handler) handleActivitySSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	lastCheck := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}

		if h.data == nil {
			continue
		}

		entries, err := h.data.LedgerEntriesSince(ctx, lastCheck, nil, 50)
		if err != nil || len(entries) == 0 {
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
			continue
		}

		for _, e := range entries {
			if e.Timestamp.After(lastCheck) {
				lastCheck = e.Timestamp
			}
			html := fmt.Sprintf(`<tr class="activity-new"><td class="mono">%s</td><td>%s</td><td class="mono">%s</td><td class="mono">%s</td></tr>`,
				template.HTMLEscapeString(e.Timestamp.UTC().Format("15:04:05")),
				template.HTMLEscapeString(e.Op),
				template.HTMLEscapeString(truncateStr(e.Target, 20)),
				template.HTMLEscapeString(truncateStr(e.AgentID, 16)),
			)
			fmt.Fprintf(w, "event: activity\ndata: %s\n\n", html)
		}
		flusher.Flush()
	}
}
