package http_test

import (
	"fmt"
	"testing"

	"github.com/axiom-studio/memora/internal/testmesh"
)

// TestTagEdits_E2E (#6182): tags set or removed through the tag routes
// show in GET memory, and search and recall filters in every mode find
// the memory by its current tags and no longer by removed ones.
func TestTagEdits_E2E(t *testing.T) {
	n := newMesh(t, []string{"a"}, nil)[0]
	_, ws := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": "tags"}, nil)
	wsID := ws["id"].(string)
	agent := map[string]string{"Memora-Agent-Id": testmesh.AgentID}
	status, m := n.Do(t, "POST", "/v1/workspaces/"+wsID+"/memories",
		map[string]any{"content": "pumice field notes", "tags": map[string]string{"k": "v"}}, agent)
	if status != 201 {
		t.Fatalf("imprint: %d %v", status, m)
	}
	mem := "/v1/workspaces/" + wsID + "/memories/" + m["id"].(string)

	if status, body := n.Do(t, "PUT", mem+"/tags/team", map[string]any{"value": "red"}, agent); status != 204 && status != 200 {
		t.Fatalf("PUT tag: %d %v", status, body)
	}
	if status, body := n.Do(t, "DELETE", mem+"/tags/k", nil, agent); status != 204 && status != 200 {
		t.Fatalf("DELETE tag: %d %v", status, body)
	}
	_, got := n.Do(t, "GET", mem, nil, nil)
	if tags := fmt.Sprint(got["tags"]); tags != "map[team:red]" {
		t.Fatalf("GET memory tags = %s, want map[team:red]", tags)
	}

	count := func(path, mode string, tags map[string][]string) int {
		t.Helper()
		status, body := n.Do(t, "POST", path, map[string]any{"query": "pumice", "mode": mode, "k": 5, "filters": map[string]any{"tags": tags}}, nil)
		if status != 200 {
			t.Fatalf("%s %s: %d %v", path, mode, status, body)
		}
		r, _ := body["results"].([]any)
		return len(r)
	}
	for _, path := range []string{"/v1/search", "/v1/workspaces/" + wsID + "/recall"} {
		for _, mode := range []string{"keyword", "vector", "hybrid"} {
			if got := count(path, mode, map[string][]string{"team": {"red"}}); got != 1 {
				t.Errorf("%s %s with the new tag: %d hits, want 1", path, mode, got)
			}
			if got := count(path, mode, map[string][]string{"k": {"v"}}); got != 0 {
				t.Errorf("%s %s with the removed tag: %d hits, want 0", path, mode, got)
			}
		}
	}
}
