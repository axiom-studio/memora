package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

// TestDispatch_EveryCatalogToolIsRouted guards against catalog/dispatch
// drift: every tool advertised by tools/list must reach a dispatch case.
// Calls use empty arguments, so most tools return a validation or
// not-found error — anything except "unknown tool" proves routing.
func TestDispatch_EveryCatalogToolIsRouted(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	for _, tool := range toolCatalog() {
		_, err := s.dispatchTool(context.Background(), tool.Name, json.RawMessage(`{}`))
		if err != nil && strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("%s is advertised by tools/list but has no dispatch case", tool.Name)
		}
	}
}

func dispatchJSON(t *testing.T, s *Server, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal %s args: %v", name, err)
	}
	out, err := s.dispatchTool(context.Background(), name, raw)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal %s result: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s result %s: %v", name, b, err)
	}
	return m, nil
}

func createWorkspace(t *testing.T, s *Server, args map[string]any) string {
	t.Helper()
	ws, err := dispatchJSON(t, s, "memora_create_workspace", args)
	if err != nil {
		t.Fatalf("create_workspace: %v", err)
	}
	return ws["id"].(string)
}

func TestDispatch_PinThenUnpin_RemovesPin(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())
	wsID := createWorkspace(t, s, map[string]any{"name": "pins"})

	pin, err := dispatchJSON(t, s, "memora_pin", map[string]any{
		"workspace_id": wsID, "query": "quarterly goals", "watermark": "wm_1", "label": "q3",
	})
	if err != nil {
		t.Fatalf("memora_pin: %v", err)
	}
	pinID, _ := pin["pin_id"].(string)
	if pinID == "" || pin["watermark"] != "wm_1" {
		t.Fatalf("memora_pin result = %v, want pin_id and watermark wm_1", pin)
	}
	pins, _ := svc.Metadata.ListPins(context.Background(), wsID)
	if len(pins) != 1 || pins[0].Mode != "hybrid" || pins[0].K != 10 || pins[0].Label != "q3" {
		t.Fatalf("stored pins = %+v, want one pin with REST defaults mode=hybrid k=10", pins)
	}

	if _, err := dispatchJSON(t, s, "memora_unpin", map[string]any{"workspace_id": wsID, "pin_id": pinID}); err != nil {
		t.Fatalf("memora_unpin: %v", err)
	}
	if pins, _ := svc.Metadata.ListPins(context.Background(), wsID); len(pins) != 0 {
		t.Fatalf("pins after unpin = %+v, want none", pins)
	}
	if _, err := dispatchJSON(t, s, "memora_unpin", map[string]any{"workspace_id": wsID, "pin_id": pinID}); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("second unpin err = %v, want ErrNotFound", err)
	}
}

func TestDispatch_Pin_WithoutQuery_ReturnsInvalidInput(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	wsID := createWorkspace(t, s, map[string]any{"name": "pins"})
	_, err := dispatchJSON(t, s, "memora_pin", map[string]any{"workspace_id": wsID, "watermark": "wm_1"})
	if !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDispatch_Unpin_FromOtherWorkspace_ReturnsNotFoundAndKeepsPin(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())
	owner := createWorkspace(t, s, map[string]any{"name": "owner"})
	other := createWorkspace(t, s, map[string]any{"name": "other"})

	pin, err := dispatchJSON(t, s, "memora_pin", map[string]any{"workspace_id": owner, "query": "q", "watermark": "wm_1"})
	if err != nil {
		t.Fatalf("memora_pin: %v", err)
	}
	_, err = dispatchJSON(t, s, "memora_unpin", map[string]any{"workspace_id": other, "pin_id": pin["pin_id"]})
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace unpin err = %v, want ErrNotFound", err)
	}
	if pins, _ := svc.Metadata.ListPins(context.Background(), owner); len(pins) != 1 {
		t.Fatalf("owner pins = %+v, want the pin to survive", pins)
	}
}

func TestDispatch_UpdateWorkspace_RenamesAndPreservesOtherFields(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())
	wsID := createWorkspace(t, s, map[string]any{"name": "before", "region": "eu-west", "chunker_id": "markdown"})

	got, err := dispatchJSON(t, s, "memora_update_workspace", map[string]any{"workspace_id": wsID, "name": "after"})
	if err != nil {
		t.Fatalf("memora_update_workspace: %v", err)
	}
	if got["name"] != "after" {
		t.Fatalf("result name = %v, want after", got["name"])
	}
	ws, err := svc.Metadata.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.Name != "after" || ws.Region != "eu-west" || ws.ChunkerID != "markdown" {
		t.Fatalf("stored workspace = %+v, want name=after with region/chunker preserved", ws)
	}
}

func TestDispatch_UpdateWorkspace_Unknown_ReturnsNotFound(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	_, err := dispatchJSON(t, s, "memora_update_workspace", map[string]any{"workspace_id": types.NewID(), "name": "x"})
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDispatch_Lookup_FromOtherWorkspace_ReturnsNotFound(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())
	owner := createWorkspace(t, s, map[string]any{"name": "owner"})
	other := createWorkspace(t, s, map[string]any{"name": "other"})
	imp, err := dispatchJSON(t, s, "memora_imprint", map[string]any{"workspace_id": owner, "agent_id": "2fe0f846-5778-4699-afb3-d64180692e07", "content": "owner secret"})
	if err != nil {
		t.Fatalf("imprint: %v", err)
	}
	if _, err := dispatchJSON(t, s, "memora_lookup", map[string]any{"workspace_id": other, "memory_id": imp["id"]}); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("cross-workspace lookup err = %v, want ErrNotFound", err)
	}
	if _, err := dispatchJSON(t, s, "memora_lookup", map[string]any{"workspace_id": owner, "memory_id": imp["id"]}); err != nil {
		t.Fatalf("same-workspace lookup: %v", err)
	}
}

// Issue #5987: the advertised schema must match what dispatch enforces, so
// clients (and LLMs) are not pushed into inventing a watermark.
func TestDispatch_PinSchemaRequiredMatchesHandler(t *testing.T) {
	var schema map[string]any
	for _, tool := range toolCatalog() {
		if tool.Name == "memora_pin" {
			schema = tool.InputSchema
		}
	}
	required, _ := schema["required"].([]string)
	if strings.Join(required, ",") != "workspace_id,query" {
		t.Fatalf("memora_pin required = %v, want [workspace_id query]", required)
	}

	s := NewServer(newTestService(t), testLogger())
	wsID := createWorkspace(t, s, map[string]any{"name": "pins"})
	pin, err := dispatchJSON(t, s, "memora_pin", map[string]any{"workspace_id": wsID, "query": "no watermark"})
	if err != nil {
		t.Fatalf("memora_pin without watermark: %v (schema says it is optional)", err)
	}
	if pin["pin_id"] == "" || pin["watermark"] != "" {
		t.Fatalf("memora_pin without watermark = %v, want a pin with an empty watermark", pin)
	}
}

// TestDispatch_Search_NodeWide: memora_search finds memories in every
// workspace and tags each hit with its workspace_id.
func TestDispatch_Search_NodeWide(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	agent := types.NewID()
	var wsIDs []string
	for _, name := range []string{"mcp-search-a", "mcp-search-b"} {
		ws := createWorkspace(t, s, map[string]any{"name": name})
		if _, err := dispatchJSON(t, s, "memora_imprint", map[string]any{"workspace_id": ws, "agent_id": agent, "content": "mcp search phrase"}); err != nil {
			t.Fatalf("imprint: %v", err)
		}
		wsIDs = append(wsIDs, ws)
	}
	out, err := dispatchJSON(t, s, "memora_search", map[string]any{"query": "mcp search phrase", "mode": "keyword"})
	if err != nil {
		t.Fatalf("memora_search: %v", err)
	}
	got := map[string]bool{}
	for _, r := range out["results"].([]any) {
		got[r.(map[string]any)["workspace_id"].(string)] = true
	}
	if !got[wsIDs[0]] || !got[wsIDs[1]] {
		t.Fatalf("memora_search workspaces = %v, want %v", got, wsIDs)
	}
}

// TestDispatch_Search_MultiTenantNeedsWorkspaceIDs (#6159): MCP has no
// tenant header, so in multi-tenant mode memora_search refuses node-wide
// scope and searches only the workspace_ids given.
func TestDispatch_Search_MultiTenantNeedsWorkspaceIDs(t *testing.T) {
	s := NewServerWithConfig(newTestService(t), testLogger(), Config{MultiTenant: true})
	ws := createWorkspace(t, s, map[string]any{"name": "tenant"})
	if _, err := dispatchJSON(t, s, "memora_search", map[string]any{"query": "x"}); !errors.Is(err, types.ErrInvalidInput) {
		t.Fatalf("node-wide search in multi-tenant mode: err = %v, want ErrInvalidInput", err)
	}
	if _, err := dispatchJSON(t, s, "memora_search", map[string]any{"query": "x", "workspace_ids": []string{ws}}); err != nil {
		t.Fatalf("scoped search in multi-tenant mode: %v", err)
	}
}
