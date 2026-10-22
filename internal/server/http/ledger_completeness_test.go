package http_test

import (
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// TestLedger_EveryGraphAndPinWrite (#6171): link, batch link, unlink,
// pin and unpin each leave a ledger entry with the op, target, agent and
// the request's X-Request-ID.
func TestLedger_EveryGraphAndPinWrite(t *testing.T) {
	n := newMesh(t, []string{"a"}, nil)[0]
	_, ws := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": "audit"}, nil)
	wsID := ws["id"].(string)
	agent := types.NewID()
	as := func(reqID string) map[string]string {
		return map[string]string{"Memora-Agent-Id": agent, "X-Request-ID": reqID}
	}
	mem := func(c string) string {
		_, m := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/memories", map[string]any{"content": c}, as("imprint-"+c))
		return m["id"].(string)
	}
	a, b, c := mem("a"), mem("b"), mem("c")
	base := "/v1/workspaces/" + wsID

	status, link := n.Do(t, "POST", base+"/memories/"+a+"/edges", map[string]any{"edge_type": "references", "target_memory_id": b}, as("req-link"))
	if status != 201 {
		t.Fatalf("link: %d %v", status, link)
	}
	single := link["edge"].(map[string]any)["edge_id"].(string)
	if link["ledger_id"] == "" || link["ledger_id"] == nil {
		t.Fatalf("single link: no ledger_id in %v", link)
	}
	_, batch := n.Do(t, "POST", base+"/edges/:batch", map[string]any{"edges": []any{
		map[string]any{"source_memory_id": b, "target_memory_id": c, "edge_type": "references"},
		map[string]any{"source_memory_id": c, "target_memory_id": a, "edge_type": "mentions"},
	}}, as("req-batch"))
	var batched []string
	for _, r := range batch["results"].([]any) {
		batched = append(batched, r.(map[string]any)["edge_id"].(string))
	}
	if status, _ := n.Do(t, "DELETE", base+"/edges/"+single, nil, as("req-unlink")); status != 204 && status != 200 {
		t.Fatalf("unlink: %d", status)
	}
	_, pin := n.Do(t, "POST", base+"/recall/pins", map[string]any{"query": "a"}, as("req-pin"))
	pinID := pin["pin_id"].(string)
	if status, _ := n.Do(t, "DELETE", base+"/recall/pins/"+pinID, nil, as("req-unpin")); status != 204 && status != 200 {
		t.Fatalf("unpin: %d", status)
	}

	_, led := n.Do(t, "GET", base+"/ledger?limit=100", nil, nil)
	type key struct{ op, target string }
	got := map[key]map[string]any{}
	for _, e := range led["entries"].([]any) {
		e := e.(map[string]any)
		got[key{e["op"].(string), e["target"].(string)}] = e
	}
	want := map[key]string{
		{"link", single}:     "req-link",
		{"link", batched[0]}: "req-batch",
		{"link", batched[1]}: "req-batch",
		{"unlink", single}:   "req-unlink",
		{"pin", pinID}:       "req-pin",
		{"unpin", pinID}:     "req-unpin",
	}
	for k, reqID := range want {
		e, ok := got[k]
		if !ok {
			t.Errorf("no %s entry for %s", k.op, k.target)
			continue
		}
		if e["agent_id"] != agent || e["request_id"] != reqID {
			t.Errorf("%s %s: agent %v request_id %v, want %s and %s", k.op, k.target, e["agent_id"], e["request_id"], agent, reqID)
		}
	}
	if e := got[key{"pin", pinID}]; e != nil && e["metadata"].(map[string]any)["query"] != "a" {
		t.Errorf("pin entry metadata = %v", e["metadata"])
	}
}
