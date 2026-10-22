package http_test

import (
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// TestAgentDeactivation_E2E (#6169): a deactivated agent cannot write
// in that workspace (403 agent_deactivated), its writes do not
// reactivate it or overwrite its registration, an explicit register
// reactivates it, and deactivate/reactivate are ledgered.
func TestAgentDeactivation_E2E(t *testing.T) {
	n := newMesh(t, []string{"a"}, nil)[0]
	_, ws := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": "agents"}, nil)
	wsID := ws["id"].(string)
	agent := types.NewID()
	as := map[string]string{"Memora-Agent-Id": agent}
	if status, body := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/agents", map[string]any{"agent_id": agent, "display_name": "writer-bot", "identity_provider": "opaque"}, nil); status != 201 {
		t.Fatalf("register: %d %v", status, body)
	}
	status, m := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/memories", map[string]any{"content": "before deactivation"}, as)
	if status != 201 {
		t.Fatalf("write while active: %d %v", status, m)
	}
	mem := "/v1/workspaces/" + wsID + "/memories/" + m["id"].(string)

	if status, _ := n.Do(t, "DELETE", "/v1/workspaces/"+wsID+"/agents/"+agent, nil, nil); status != 204 {
		t.Fatalf("deactivate: %d", status)
	}
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/workspaces/" + wsID + "/memories", map[string]any{"content": "after"}},
		{"POST", mem + ":append", map[string]any{"content": " more", "expected_watermark": m["watermark"]}},
		{"DELETE", mem, nil},
	} {
		if status, body := n.Do(t, w.method, w.path, w.body, as); status != 403 || body["error_code"] != "agent_deactivated" {
			t.Fatalf("%s %s by a deactivated agent: %d %v, want 403 agent_deactivated", w.method, w.path, status, body)
		}
	}
	_, a := n.Do(t, "GET", "/v1/workspaces/"+wsID+"/agents/"+agent, nil, nil)
	if a["active"] != false || a["display_name"] != "writer-bot" {
		t.Fatalf("after refused writes the agent is %v; want still inactive, display_name kept", a)
	}

	// Another agent is unaffected, and so is the same agent elsewhere.
	if status, _ := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/memories", map[string]any{"content": "other"}, map[string]string{"Memora-Agent-Id": types.NewID()}); status != 201 {
		t.Fatalf("another agent: %d", status)
	}
	_, ws2 := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": "elsewhere"}, nil)
	if status, _ := n.Do(t, "POST", "/v1/workspaces/"+ws2["id"].(string)+"/memories", map[string]any{"content": "elsewhere"}, as); status != 201 {
		t.Fatalf("same agent in another workspace: %d", status)
	}

	// Explicit registration reactivates.
	if status, _ := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/agents", map[string]any{"agent_id": agent, "display_name": "writer-bot", "identity_provider": "opaque"}, nil); status != 201 {
		t.Fatal("re-register failed")
	}
	if status, body := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/memories", map[string]any{"content": "after reactivation"}, as); status != 201 {
		t.Fatalf("write after reactivation: %d %v", status, body)
	}

	ops := map[string]int{}
	_, led := n.Do(t, "GET", "/v1/workspaces/"+wsID+"/ledger?limit=100", nil, nil)
	for _, e := range led["entries"].([]any) {
		e := e.(map[string]any)
		if e["target"] == agent {
			ops[e["op"].(string)]++
			if e["op"] == "agent_register" && e["metadata"].(map[string]any)["reactivated"] == true {
				ops["reactivated"]++
			}
		}
	}
	if ops["agent_deactivate"] != 1 || ops["agent_register"] != 2 || ops["reactivated"] != 1 {
		t.Fatalf("ledger ops for the agent = %v; want 1 deactivate, 2 registers, 1 reactivation", ops)
	}
}
