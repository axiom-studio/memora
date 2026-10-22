package http_test

import (
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// TestRoutes_NonUUIDIDs_Return400NamingTheField pins R1 at the HTTP
// boundary: an ID that is not a UUIDv4 — a legacy prefixed ID, an
// uppercase UUID, or garbage — is a 400 invalid_input whose message names
// the parameter, never a 404 or 500.
func TestRoutes_NonUUIDIDs_Return400NamingTheField(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}

	_, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "ids"}, nil))
	wsID := ws["id"].(string)
	_, imp := doJSON(t, c, jsonReq(t, "POST", base+"/"+wsID+"/memories", map[string]any{"content": "x"}, agentH))
	memID := imp["id"].(string)
	wsBase := base + "/" + wsID

	const legacy = "mem_01HXYZABCDEFGHJKMNPQRSTVWX"
	upper := strings.ToUpper(types.NewID())
	cases := []struct {
		name, method, url, field string
		body                     any
	}{
		{"legacy workspace", "GET", base + "/ws_01HXYZABCDEFGHJKMNPQRSTVWX", "workspace_id", nil},
		{"uppercase workspace", "GET", base + "/" + strings.ToUpper(wsID), "workspace_id", nil},
		{"legacy memory", "GET", wsBase + "/memories/" + legacy, "memory_id", nil},
		{"uppercase memory", "GET", wsBase + "/memories/" + upper, "memory_id", nil},
		{"garbage memory on append", "POST", wsBase + "/memories/not-an-id:append", "memory_id", map[string]any{"content": "y"}},
		{"legacy watermark", "GET", wsBase + "/memories/" + memID + "@wmk_01HXYZABCDEFGHJKMNPQRSTVWX", "watermark", nil},
		{"memory sub-resource", "GET", wsBase + "/memories/" + legacy + "/watermarks", "memory_id", nil},
		{"collection", "GET", wsBase + "/collections/coll_1", "collection_id", nil},
		{"collection filter", "GET", wsBase + "/memories?collection_id=coll_1", "collection_id", nil},
		{"pin", "DELETE", wsBase + "/recall/pins/pin_1", "pin_id", nil},
		{"edge", "DELETE", wsBase + "/edges/edg_synthetic_42", "edge_id", nil},
		{"neighbors body", "POST", wsBase + "/graph/neighbors", "memory_id", map[string]any{"memory_id": legacy}},
		{"traverse body", "POST", wsBase + "/graph/traverse", "seed_memory_id", map[string]any{"seed_memory_id": legacy}},
		{"ledger memory filter", "GET", wsBase + "/ledger?memory_id=" + legacy, "memory_id", nil},
		{"ledger cursor", "GET", wsBase + "/ledger?since_ledger_id=lg_1", "since_ledger_id", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSON(t, c, jsonReq(t, tc.method, tc.url, tc.body, agentH))
			if status != 400 || body["error_code"] != "invalid_input" {
				t.Fatalf("status=%d body=%v, want 400 invalid_input", status, body)
			}
			if msg, _ := body["message"].(string); !strings.Contains(msg, tc.field+" must be a UUID") {
				t.Errorf("message %q does not name %s", msg, tc.field)
			}
		})
	}
}

// TestRoutes_UnknownUUID_Is404 shows the split: a well-formed UUID that
// matches nothing is still a 404, not a validation error.
func TestRoutes_UnknownUUID_Is404(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	_, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": "ids"}, nil))

	status, _ := doJSON(t, c, jsonReq(t, "GET", base+"/"+types.NewID(), nil, nil))
	if status != 404 {
		t.Errorf("unknown workspace: status=%d, want 404", status)
	}
	status, _ = doJSON(t, c, jsonReq(t, "GET", base+"/"+ws["id"].(string)+"/memories/"+types.NewID(), nil, nil))
	if status != 404 {
		t.Errorf("unknown memory: status=%d, want 404", status)
	}
}
