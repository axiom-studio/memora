package ui

import (
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

// idParams are query and form parameters that hold a Memora UUID on
// every console route, with the field name errors report.
var idParams = map[string]string{
	"ws":               "workspace_id",
	"workspace_id":     "workspace_id",
	"memory_id":        "memory_id",
	"collection_id":    "collection_id",
	"seed":             "seed_memory_id",
	"agent":            "agent_id",
	"agent_id":         "agent_id",
	"edge_id":          "edge_id",
	"source_memory_id": "source_memory_id",
	"target_memory_id": "target_memory_id",
	"node_id":          "node_id",
}

// idRouteParams says what "id" holds on each route that takes one. It is
// overloaded: a workspace on one form, a memory, agent or peer on
// others, and an identity provider's name (not a UUID) on the IdP forms,
// which are therefore not listed.
var idRouteParams = map[string]string{
	"/ui/partials/workspace-detail":       "workspace_id",
	"/ui/partials/workspace-edit-form":    "workspace_id",
	"/ui/partials/workspace-delete-form":  "workspace_id",
	"/ui/api/workspaces/update":           "workspace_id",
	"/ui/api/workspaces/delete":           "workspace_id",
	"/ui/api/workspaces/config":           "workspace_id",
	"/ui/partials/memory-detail":          "memory_id",
	"/ui/partials/memory-edit-form":       "memory_id",
	"/ui/partials/memory-patch-form":      "memory_id",
	"/ui/partials/memory-append-form":     "memory_id",
	"/ui/partials/memory-forget-form":     "memory_id",
	"/ui/partials/tag-add-form":           "memory_id",
	"/ui/partials/tag-delete-form":        "memory_id",
	"/ui/api/tags/upsert":                 "memory_id",
	"/ui/api/tags/delete":                 "memory_id",
	"/ui/partials/agent-deactivate-form":  "agent_id",
	"/ui/partials/audit-detail":           "ledger_id",
	"/ui/partials/collection-delete-form": "collection_id",
	"/ui/api/collections/delete":          "collection_id",
	"/ui/partials/edge-unlink-form":       "edge_id",
	"/ui/partials/peer-edit-form":         "node_id",
	"/ui/partials/peer-remove-form":       "node_id",
	"/ui/api/peers/remove":                "node_id",
	"/ui/partials/pin-delete-form":        "pin_id",
	"/ui/api/pins/delete":                 "pin_id",
}

// checkIDs validates every ID the request carries, in the
// /ui/workspaces/{ws}[/memories/{id}] path, the query and form
// parameters named in idParams, and "id" where idRouteParams says what
// it is. It returns the first error, naming the field.
func checkIDs(r *http.Request) error {
	check := func(field, v string) error {
		if v = strings.TrimSpace(v); v == "" {
			return nil
		}
		_, err := types.ParseID(field, v)
		return err
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/ui/workspaces/"); ok {
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if err := check("workspace_id", parts[0]); err != nil {
			return err
		}
		if len(parts) >= 3 && parts[1] == "memories" {
			if err := check("memory_id", parts[2]); err != nil {
				return err
			}
		}
	}
	// ParseForm keeps the pairs it parsed before an error, and handlers
	// read them with FormValue, so a malformed form must not skip the
	// checks below (#6366). ParseForm reads no multipart body, so a
	// multipart form is parsed in full here and its fields merge into
	// r.Form; handlers then read the parsed form (#6407). maxMemory is
	// the body bound, so every value (a bulk "records" field included)
	// fits and nothing spills to a temp file.
	parse := r.ParseForm
	if isMultipart(r) {
		parse = func() error { return r.ParseMultipartForm(multipartLimit(r.URL.Path)) }
	}
	if err := parse(); err != nil {
		if tooBig := (*http.MaxBytesError)(nil); errors.As(err, &tooBig) {
			return fmt.Errorf("%w: form too large (limit %d MiB)", types.ErrInvalidInput, tooBig.Limit>>20)
		}
		return fmt.Errorf("%w: malformed form", types.ErrInvalidInput)
	}
	for name, field := range idParams {
		for _, v := range r.Form[name] {
			if err := check(field, v); err != nil {
				return err
			}
		}
	}
	if field, ok := idRouteParams[r.URL.Path]; ok {
		for _, v := range r.Form["id"] {
			if err := check(field, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// multipartLimit bounds a console multipart body at its handler's own
// limit, so the middleware never refuses what the handler accepts: the
// bulk seed, import and replay routes take 64 MiB, the rest an upload.
func multipartLimit(path string) int64 {
	if strings.HasPrefix(path, "/ui/api/bulk/") {
		return maxBulkUploadBytes
	}
	return maxUploadBytes + 4096
}

func isMultipart(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "multipart/form-data"
}

// validateIDs answers 400 with the field-named error for a console
// request carrying a malformed ID, before any handler runs, so a legacy
// or garbled ID never shows as an empty page or "not found".
func (h *Handler) validateIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMultipart(r) {
			// checkIDs parses multipart bodies before any handler bounds them.
			r.Body = http.MaxBytesReader(w, r.Body, multipartLimit(r.URL.Path))
		}
		if err := checkIDs(r); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `<div class="form-error">%s</div>`, template.HTMLEscapeString(strings.TrimPrefix(err.Error(), "memora: invalid input: ")))
			return
		}
		next.ServeHTTP(w, r)
	})
}
