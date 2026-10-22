package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/testmesh"
)

// TestMCP_FederatedSearchAndFetch (F-9): through the real dispatcher on
// node a of a two-node mesh, memora_search federated:true returns b's
// memory with its source (node, workspace, memory, matched text), and
// memora_fetch with that hit's node_id reads the whole memory from b.
func TestMCP_FederatedSearchAndFetch(t *testing.T) {
	nodes := testmesh.New(t, []string{"a", "b"}, testmesh.Options{})
	a, b := nodes[0], nodes[1]
	memA := a.Seed(t, "aurora over the fjord")
	memB := b.Seed(t, "aurora forecast for tonight\n\nsecond paragraph")
	s := NewServer(a.Svc, testLogger())

	call := func(tool string, args map[string]any) response {
		t.Helper()
		argsJSON, _ := json.Marshal(args)
		frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, argsJSON)
		resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, frame)
		if len(resps) != 2 || resps[1].Error != nil {
			t.Fatalf("%s: %+v", tool, resps)
		}
		return resps[1]
	}

	res := unwrapToolContent(t, call("memora_search", map[string]any{
		"query": "aurora", "mode": "keyword", "k": 10, "federated": true, "per_peer_k": 5, "include_content": true,
	}))
	hits, _ := res["results"].([]any)
	byMem := map[string]map[string]any{}
	for _, h := range hits {
		m := h.(map[string]any)
		byMem[m["memory_id"].(string)] = m
	}
	hb, ha := byMem[memB], byMem[memA]
	if hb == nil || ha == nil {
		t.Fatalf("hits = %v, want a's and b's memories", hits)
	}
	for _, f := range []string{"node_id", "node_name", "workspace_id", "memory_id", "matched_text"} {
		if s, _ := hb[f].(string); s == "" {
			t.Fatalf("b's hit lacks %s: %v", f, hb)
		}
	}
	if hb["node_id"] != b.ID || hb["node_name"] != "b" || ha["node_id"] != a.ID || !strings.Contains(hb["content"].(string), "second paragraph") {
		t.Fatalf("attribution/content: a=%v b=%v", ha, hb)
	}

	env := unwrapToolContent(t, call("memora_fetch", map[string]any{"memory_id": memB, "node_id": hb["node_id"]}))
	if env["id"] != memB || env["node_id"] != b.ID || !strings.Contains(env["content"].(string), "second paragraph") {
		t.Fatalf("memora_fetch from b = %v", env)
	}
	if cells, _ := env["cells"].([]any); len(cells) == 0 {
		t.Fatalf("memora_fetch returned no cells: %v", env)
	}
}
