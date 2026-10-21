package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestToolCatalog_RejectsNonUUIDIDs (ID-9) is driven by toolCatalog: for
// every tool, every input property that names an ID (types.IDField) is
// sent a legacy prefixed ID, an uppercase UUID and garbage, and the call
// must fail with -32602 naming the argument. A new tool's ID arguments
// are covered by being in the catalog.
func TestToolCatalog_RejectsNonUUIDIDs(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	bads := []string{"mem_01HXYZABCDEFGHJKMNPQRSTVWX", strings.ToUpper(types.NewID()), "not-an-id"}
	frameID, cases := 1, 0
	for _, tool := range toolCatalog() {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name := range props {
			isID, isList := types.IDField(name)
			if !isID {
				continue
			}
			for _, bad := range bads {
				args := map[string]any{"workspace_id": types.NewID(), "query": "x", "content": "x"}
				if isList {
					args[name] = []string{bad}
				} else {
					args[name] = bad
				}
				argsJSON, _ := json.Marshal(args)
				frameID++
				frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, frameID, tool.Name, argsJSON)
				resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, frame)
				if len(resps) != 2 || resps[1].Error == nil {
					t.Fatalf("%s %s=%q: want an error, got %+v", tool.Name, name, bad, resps)
				}
				if e := resps[1].Error; e.Code != -32602 || !strings.Contains(e.Message, name+" must be a UUID") {
					t.Errorf("%s %s=%q: error = %+v, want -32602 naming %s", tool.Name, name, bad, e, name)
				}
				cases++
			}
		}
	}
	if cases < 30 {
		t.Fatalf("only %d cases generated; the catalog walk is broken", cases)
	}
}

// TestFetch_LocalAndMultiTenant: memora_fetch reads a memory with its
// cells from this node; in multi-tenant mode it needs the right
// workspace_id and never reaches a peer.
func TestFetch_LocalAndMultiTenant(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	ws := &types.Workspace{Name: "fetch"}
	if err := svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Imprint(ctx, ws.ID, "2b4224a8-95b4-4171-9e6b-689847d53ac7", api.ImprintRequest{Content: "fetch me"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(s *Server, args map[string]any) response {
		argsJSON, _ := json.Marshal(args)
		frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_fetch","arguments":%s}}`, argsJSON)
		resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, frame)
		if len(resps) != 2 {
			t.Fatalf("responses = %+v", resps)
		}
		return resps[1]
	}

	single := NewServer(svc, testLogger())
	r := call(single, map[string]any{"memory_id": imp.ID})
	if r.Error != nil {
		t.Fatalf("local fetch: %+v", r.Error)
	}
	raw, _ := json.Marshal(r.Result)
	if !strings.Contains(string(raw), "fetch me") || !strings.Contains(string(raw), `\"cells\"`) && !strings.Contains(string(raw), `"cells"`) {
		t.Fatalf("local fetch result %s, want the content and cells", raw)
	}
	if r := call(single, map[string]any{"memory_id": imp.ID, "node_id": types.NewID()}); r.Error == nil {
		t.Fatal("unknown node: want an error")
	}

	mt := NewServerWithConfig(svc, testLogger(), Config{MultiTenant: true})
	if r := call(mt, map[string]any{"memory_id": imp.ID, "workspace_id": ws.ID}); r.Error != nil {
		t.Fatalf("multi-tenant own workspace: %+v", r.Error)
	}
	for _, args := range []map[string]any{
		{"memory_id": imp.ID},
		{"memory_id": imp.ID, "workspace_id": types.NewID()},
		{"memory_id": imp.ID, "workspace_id": ws.ID, "node_id": types.NewID()},
	} {
		if r := call(mt, args); r.Error == nil {
			t.Fatalf("multi-tenant %v: want an error", args)
		}
	}
}
