package http_test

import "testing"

// TestLedger_NoLedgerStore_Is501 (#6153): a server built without a
// LedgerStore answers GET .../ledger with 501 capability_unavailable,
// not a recovered nil-pointer panic (500).
func TestLedger_NoLedgerStore_Is501(t *testing.T) {
	ts := newTestServer(t) // newTestService wires no ledger
	_, ws := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/workspaces", map[string]any{"name": "no-ledger"}, nil))
	status, body := doJSON(t, ts.Client(), jsonReq(t, "GET", ts.URL+"/v1/workspaces/"+ws["id"].(string)+"/ledger", nil, nil))
	if status != 501 || body["error_code"] != "capability_unavailable" {
		t.Fatalf("status=%d body=%v, want 501 capability_unavailable", status, body)
	}
}
