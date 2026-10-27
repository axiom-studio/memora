package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// TestUI_RejectsNonUUIDIDs (#4371) walks the console's ID tables: every
// route whose "id" is an ID, every always-ID parameter, and the
// /ui/workspaces/{ws}/memories/{id} path. A legacy prefixed ID, an
// uppercase UUID and garbage each answer 400 naming the field, before
// any handler runs.
func TestUI_RejectsNonUUIDIDs(t *testing.T) {
	h := mustHandler(t)
	ds, _ := realDataSource(t)
	h.SetDataSource(ds)
	mux := http.NewServeMux()
	h.Register(mux)
	bads := []string{"mem_01HXYZABCDEFGHJKMNPQRSTVWX", strings.ToUpper(types.NewID()), "not-an-id"}

	do := func(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if method == "POST" {
			r = httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest("GET", target+"?"+form.Encode(), nil)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	expect := func(t *testing.T, w *httptest.ResponseRecorder, field string) {
		t.Helper()
		if w.Code != 400 || !strings.Contains(w.Body.String(), field+" must be a UUID") {
			t.Fatalf("status %d body %q, want 400 naming %s", w.Code, w.Body.String(), field)
		}
	}

	cases := 0
	for path, field := range idRouteParams {
		method := "GET"
		if strings.HasPrefix(path, "/ui/api/") {
			method = "POST"
		}
		for _, bad := range bads {
			t.Run(path+"/"+bad, func(t *testing.T) {
				expect(t, do(t, method, path, url.Values{"id": {bad}}), field)
			})
			cases++
		}
	}
	for name, field := range idParams {
		for _, bad := range bads {
			t.Run("param "+name+"/"+bad, func(t *testing.T) {
				expect(t, do(t, "GET", "/ui/partials/memory-list", url.Values{name: {bad}}), field)
				expect(t, do(t, "POST", "/ui/api/memories/imprint", url.Values{name: {bad}, "content": {"x"}}), field)
			})
			cases++
		}
	}
	for _, bad := range bads {
		t.Run("path/"+bad, func(t *testing.T) {
			expect(t, do(t, "GET", "/ui/workspaces/"+url.PathEscape(bad), nil), "workspace_id")
			expect(t, do(t, "GET", "/ui/workspaces/"+types.NewID()+"/memories/"+url.PathEscape(bad), nil), "memory_id")
		})
		cases++
	}
	if cases < 100 {
		t.Fatalf("only %d cases; the table walk is broken", cases)
	}

	// IdP forms carry a provider name in "id", not a UUID.
	if w := do(t, "GET", "/ui/partials/idp-edit-form", url.Values{"id": {"oidc_agent"}}); w.Code == 400 {
		t.Fatalf("idp form id was validated as a UUID: %s", w.Body.String())
	}
}

// TestUI_UnknownUUIDIsNotFound: a well-formed UUID that names nothing
// passes validation and reaches the handler's not-found.
func TestUI_UnknownUUIDIsNotFound(t *testing.T) {
	h := mustHandler(t)
	ds, svc := realDataSource(t)
	h.SetDataSource(ds)
	mux := http.NewServeMux()
	h.Register(mux)
	ws := &types.Workspace{Name: "ui-ids"}
	if err := svc.Metadata.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/ui/partials/memory-detail?ws=" + ws.ID + "&id=" + types.NewID(),
		"/ui/partials/workspace-detail?id=" + types.NewID(),
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		body := strings.ToLower(w.Body.String())
		if w.Code == 400 || strings.Contains(body, "must be a uuid") || !strings.Contains(body, "not found") {
			t.Fatalf("GET %s: %d %q, want a not-found page", target, w.Code, w.Body.String())
		}
	}
}

// TestUI_MalformedFormFailsClosed (#6366): a form body with a bad
// percent-escape is refused, wherever the bad pair sits; ParseForm still
// fills r.Form with the other pairs, so letting it through skipped every
// ID check.
func TestUI_MalformedFormFailsClosed(t *testing.T) {
	h := mustHandler(t)
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	for _, body := range []string{"memory_id=../../etc&bad=%zz", "bad=%zz&memory_id=../../etc", "memory_id=" + types.NewID() + "&bad=%zz"} {
		reached = false
		r := httptest.NewRequest("POST", "/ui/api/memories/forget", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.validateIDs(next).ServeHTTP(w, r)
		if w.Code != 400 || reached || !strings.Contains(w.Body.String(), "malformed form") {
			t.Fatalf("%q: status %d, handler reached %v, body %q; want 400 malformed form", body, w.Code, reached, w.Body.String())
		}
	}
}
