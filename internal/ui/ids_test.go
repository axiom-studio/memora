package ui

import (
	"bytes"
	"context"
	"mime/multipart"
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

// TestUI_MultipartBodyIDsChecked (#6407): a multipart console upload,
// which is what the browser sends, is parsed by the ID middleware, so its
// body IDs are validated and the handler still sees its fields and file.
func TestUI_MultipartBodyIDsChecked(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)
	ws := types.NewID()
	upload := func(query string, fields map[string]string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		fw, _ := mw.CreateFormFile("file", "notes.md")
		_, _ = fw.Write([]byte("uploaded notes"))
		_ = mw.Close()
		r := httptest.NewRequest("POST", "/ui/api/memories/upload"+query, &buf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	if w := upload("", map[string]string{"workspace_id": ws}); w.Code != 200 || !strings.Contains(w.Body.String(), "Document Uploaded") {
		t.Fatalf("valid multipart upload: status %d body %q, want 200 Document Uploaded", w.Code, w.Body.String())
	}
	for name, c := range map[string]struct {
		query  string
		fields map[string]string
		field  string
	}{
		"bad body collection_id": {"?workspace_id=" + ws, map[string]string{"collection_id": "garbage"}, "collection_id"},
		"bad body workspace_id":  {"", map[string]string{"workspace_id": "../../etc"}, "workspace_id"},
	} {
		if w := upload(c.query, c.fields); w.Code != 400 || !strings.Contains(w.Body.String(), c.field+" must be a UUID") {
			t.Fatalf("%s: status %d body %q, want 400 naming %s", name, w.Code, w.Body.String(), c.field)
		}
	}
	// The middleware bounds the body it parses, at the handler's limit.
	if w := upload("", map[string]string{"workspace_id": ws, "pad": strings.Repeat("x", maxUploadBytes+8192)}); w.Code != 400 || !strings.Contains(w.Body.String(), "form too large (limit 8 MiB)") {
		t.Fatalf("oversized multipart: status %d body %q, want 400 form too large", w.Code, w.Body.String())
	}
}

// TestUI_MultipartBulkImportOver8MiB (#6407 QA rework): the console posts
// bulk import as FormData with a 64 MiB handler limit; the ID middleware
// must parse it at that limit, not the 8 MiB upload one.
func TestUI_MultipartBulkImportOver8MiB(t *testing.T) {
	h := mustHandler(t)
	h.SetDataSource(&mockDataSource{})
	mux := http.NewServeMux()
	h.Register(mux)
	records := `[{"content":"` + strings.Repeat("x", 12<<20) + `","content_md5":"big"}]` // ~12 MiB
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("workspace_id", types.NewID())
	_ = mw.WriteField("conflict", "skip")
	_ = mw.WriteField("records", records)
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/ui/api/bulk/import", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if body := w.Body.String(); !strings.Contains(body, `"type":"complete"`) || !strings.Contains(body, `"succeeded":1`) {
		t.Fatalf("12 MiB multipart bulk import: status %d body %.300q, want a completed import", w.Code, body)
	}
}
