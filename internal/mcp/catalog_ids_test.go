package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
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
