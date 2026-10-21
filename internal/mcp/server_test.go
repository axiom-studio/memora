package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/service"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/identity"
	"github.com/axiom-studio/memora/pkg/types"
)

func testLogger() *stdlog.Logger { return stdlog.New(io.Discard, "", 0) }

// newTestService wires a real sqlite primary + sqlitevec vector +
// noop embedder + opaque identity. No mocks of internal types; only
// the public interfaces and their OSS-default implementations.
func newTestService(t *testing.T) *service.Service {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	primary := &storesqlite.Store{}
	if err := primary.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "primary.db")}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "vector.db"), Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })

	embedProvider, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatalf("open embedding: %v", err)
	}
	return &service.Service{
		Metadata: primary,
		Vector:   vec,
		Embedder: embedProvider,
		Identity: map[string]adapter.IdentityProvider{
			string(types.IdentityProviderOpaque): identity.Opaque{},
		},
	}
}

// roundtrip drives one ServeStdio cycle: writes the supplied newline-
// delimited JSON-RPC frames to a buffer, runs the server, and returns
// each decoded response in order. EOF on the in-buffer terminates the
// server loop naturally.
func roundtrip(t *testing.T, s *Server, frames ...string) []response {
	t.Helper()
	in := &bytes.Buffer{}
	for _, f := range frames {
		in.WriteString(f)
		if !strings.HasSuffix(f, "\n") {
			in.WriteString("\n")
		}
	}
	out := &bytes.Buffer{}
	if err := s.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	var responses []response
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r response
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("decode response %q: %v", string(line), err)
		}
		responses = append(responses, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan responses: %v", err)
	}
	return responses
}

func TestServeStdio_NoAuth_AllowsInitializeAndTools(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("initialize errored: %+v", resps[0].Error)
	}
	if resps[1].Error != nil {
		t.Fatalf("tools/list errored: %+v", resps[1].Error)
	}
	// tools/list result.tools is a non-empty array.
	res, _ := resps[1].Result.(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned empty catalog")
	}
}

func TestServeStdio_AuthRequired_ValidKey(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "secret-key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"secret-key"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("valid-key initialize errored: %+v", resps[0].Error)
	}
	if resps[1].Error != nil {
		t.Fatalf("post-init tools/list errored: %+v", resps[1].Error)
	}
}

func TestServeStdio_AuthRequired_BadKey(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "secret-key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"wrong-key"}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil {
		t.Fatal("bad-key initialize should error")
	}
	if resps[0].Error.Code != ErrCodeUnauthorized {
		t.Fatalf("error code = %d, want %d", resps[0].Error.Code, ErrCodeUnauthorized)
	}
}

func TestServeStdio_AuthRequired_NoKey(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "secret-key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != ErrCodeUnauthorized {
		t.Fatalf("missing apiKey should produce ErrCodeUnauthorized, got %+v", resps[0].Error)
	}
}

func TestServeStdio_AuthRequired_ToolBeforeInit(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "secret-key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != ErrCodeUnauthorized {
		t.Fatalf("tools/list before init should produce ErrCodeUnauthorized, got %+v", resps[0].Error)
	}
}

func TestServeStdio_AuthRequired_PingBeforeInit_Rejected(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "secret-key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != ErrCodeUnauthorized {
		t.Fatalf("ping before init should produce ErrCodeUnauthorized (no probe-without-key), got %+v", resps[0].Error)
	}
}

func TestServeStdio_NoAuth_PingAllowed(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`,
	)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("ping with no auth should succeed, got %+v", resps)
	}
}

func TestServeStdio_BadFrame_ContinuesScanning(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	// Malformed JSON in line 1; valid initialize in line 2. Line 1
	// must be logged and skipped, not crash the loop.
	resps := roundtrip(t, s,
		`{not valid json}`,
		`{"jsonrpc":"2.0","id":42,"method":"initialize","params":{}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response (only the valid frame), got %d (%+v)", len(resps), resps)
	}
	if resps[0].Error != nil {
		t.Fatalf("valid initialize after bad frame errored: %+v", resps[0].Error)
	}
}

func TestServeStdio_UnknownMethod(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"nonexistent/method","params":{}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if resps[1].Error == nil || resps[1].Error.Code != -32601 {
		t.Fatalf("unknown method should return -32601, got %+v", resps[1].Error)
	}
}

func TestServeStdio_Notification_NoResponse(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	// A request without `id` is a notification: dispatched but never
	// answered. We send one notification followed by a regular request
	// — the regular response should be the ONLY frame in the output.
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response (notification produces none), got %d (%+v)", len(resps), resps)
	}
	idBytes, _ := resps[0].ID.MarshalJSON()
	if string(idBytes) != "7" {
		t.Fatalf("expected response id=7, got %s", string(idBytes))
	}
}

func TestDispatch_AgentIDFlowsToService(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	// Set up: create a workspace, then imprint a memory through the
	// MCP tools. Read the resulting Memory back from the primary and
	// confirm the agent_id round-tripped through the dispatch path.
	const agentID = "dccc75db-d856-4353-8a71-c748fe2bc81f"
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_create_workspace","arguments":{"name":"dispatch-test"}}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d (%+v)", len(resps), resps)
	}
	if resps[1].Error != nil {
		t.Fatalf("create_workspace errored: %+v", resps[1].Error)
	}
	wsID := extractToolWorkspaceID(t, resps[1])

	imprintArgs := map[string]any{
		"workspace_id": wsID,
		"agent_id":     agentID,
		"content":      "hello dispatch",
	}
	argsJSON, _ := json.Marshal(imprintArgs)
	imprintFrame := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memora_imprint","arguments":` + string(argsJSON) + `}}`
	resps2 := roundtrip(t, s, imprintFrame)
	if len(resps2) != 1 || resps2[0].Error != nil {
		t.Fatalf("imprint failed: %+v", resps2)
	}
	memID := extractToolMemoryID(t, resps2[0])

	mem, err := svc.Metadata.GetMemory(context.Background(), memID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.WrittenByAgentID != agentID {
		t.Fatalf("WrittenByAgentID = %q, want %q", mem.WrittenByAgentID, agentID)
	}
}

// extractToolWorkspaceID pulls workspace_id out of the MCP tool-call
// response envelope: result.content[0].text is a JSON-encoded payload.
func extractToolWorkspaceID(t *testing.T, r response) string {
	t.Helper()
	payload := unwrapToolContent(t, r)
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatalf("create_workspace response missing id: %+v", payload)
	}
	return id
}

func extractToolMemoryID(t *testing.T, r response) string {
	t.Helper()
	payload := unwrapToolContent(t, r)
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatalf("imprint response missing id: %+v", payload)
	}
	return id
}

func unwrapToolContent(t *testing.T, r response) map[string]any {
	t.Helper()
	res, _ := r.Result.(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("response missing content array: %+v", r.Result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("content text not JSON: %v (%q)", err, text)
	}
	return payload
}

func TestInitialize_OpaqueProvider_Accepts(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"key","agentId":"88b4bd22-90ad-4364-badb-bf03e8a490a9","identityProvider":"opaque"}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("opaque provider should accept: %+v", resps[0].Error)
	}
	if s.pinnedAgent != "88b4bd22-90ad-4364-badb-bf03e8a490a9" {
		t.Fatalf("pinnedAgent = %q, want %q", s.pinnedAgent, "88b4bd22-90ad-4364-badb-bf03e8a490a9")
	}
}

// initializeAgent runs initialize and returns the agentId the server
// reports, or the error response.
func initializeAgent(t *testing.T, s *Server, params string) (string, *rpcError) {
	t.Helper()
	resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+params+`}`)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		return "", resps[0].Error
	}
	var result struct {
		AgentID string `json:"agentId"`
	}
	b, _ := json.Marshal(resps[0].Result)
	_ = json.Unmarshal(b, &result)
	return result.AgentID, nil
}

// TestInitialize_AnthropicSession_MintsStableUUID: a proof-identified
// agent gets a server-minted UUIDv4 agent_id, reported by initialize,
// pinned for the session, and the same for every later session with
// the same proof.
func TestInitialize_AnthropicSession_MintsStableUUID(t *testing.T) {
	svc := newTestService(t)
	svc.Identity[string(types.IdentityProviderAnthropicSession)] = identity.AnthropicSession{}
	params := `{"apiKey":"key","identityProvider":"anthropic_session","identityProof":{"session_id":"sess-1","model":"claude-3","system_prompt":"my prompt"}}`

	s1 := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})
	first, rpcErr := initializeAgent(t, s1, params)
	if rpcErr != nil {
		t.Fatalf("valid anthropic_session proof should accept: %+v", rpcErr)
	}
	if _, err := types.ParseID("agentId", first); err != nil {
		t.Fatalf("minted agent_id: %v", err)
	}
	if s1.pinnedAgent != first {
		t.Fatalf("pinnedAgent = %q, want %q", s1.pinnedAgent, first)
	}

	s2 := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})
	second, rpcErr := initializeAgent(t, s2, params)
	if rpcErr != nil || second != first {
		t.Fatalf("second session resolved to %q (%+v), want %q", second, rpcErr, first)
	}

	// Claiming the resolved agent_id explicitly is accepted.
	s3 := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})
	claimed := `{"apiKey":"key","agentId":"` + first + `","identityProvider":"anthropic_session","identityProof":{"session_id":"sess-1","model":"claude-3","system_prompt":"my prompt"}}`
	if got, rpcErr := initializeAgent(t, s3, claimed); rpcErr != nil || got != first {
		t.Fatalf("claiming the resolved agent_id: got %q (%+v)", got, rpcErr)
	}
}

func TestInitialize_AnthropicSession_RejectsOtherAgentAndBadProof(t *testing.T) {
	svc := newTestService(t)
	svc.Identity[string(types.IdentityProviderAnthropicSession)] = identity.AnthropicSession{}
	cases := map[string]string{
		"claims another agent": `{"apiKey":"key","agentId":"` + types.NewID() + `","identityProvider":"anthropic_session","identityProof":{"session_id":"s","model":"m"}}`,
		"proof lacks model":    `{"apiKey":"key","identityProvider":"anthropic_session","identityProof":{"session_id":"s"}}`,
	}
	for name, params := range cases {
		s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})
		_, rpcErr := initializeAgent(t, s, params)
		if rpcErr == nil || rpcErr.Code != ErrCodeUnauthorized {
			t.Errorf("%s: want ErrCodeUnauthorized, got %+v", name, rpcErr)
		}
		if s.pinnedAgent != "" {
			t.Errorf("%s: pinnedAgent should be empty after failed verify, got %q", name, s.pinnedAgent)
		}
	}
}

func TestInitialize_OpaqueProvider_RejectsNonUUIDAgentID(t *testing.T) {
	s := NewServerWithConfig(newTestService(t), testLogger(), Config{APIKey: "key"})
	_, rpcErr := initializeAgent(t, s, `{"apiKey":"key","agentId":"agent_my_bot","identityProvider":"opaque"}`)
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "agent_id must be a UUID") {
		t.Fatalf("want agent_id UUID rejection, got %+v", rpcErr)
	}
}

func TestDispatch_PinnedAgentIDOverridesClientParam(t *testing.T) {
	svc := newTestService(t)
	s := NewServerWithConfig(svc, testLogger(), Config{APIKey: "key"})

	// Initialize with pinned 4c1e2f3a-5b6d-4e7f-8a9b-0c1d2e3f4a5b.
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"key","agentId":"4c1e2f3a-5b6d-4e7f-8a9b-0c1d2e3f4a5b","identityProvider":"opaque"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_create_workspace","arguments":{"name":"pin-test"}}}`,
	)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("setup failed: %+v", resps)
	}
	wsID := extractToolWorkspaceID(t, resps[1])

	// Imprint with agent_id=9e8d7c6b-5a4f-4e3d-ac2b-1a0f9e8d7c6b — pinned 4c1e2f3a-5b6d-4e7f-8a9b-0c1d2e3f4a5b should override.
	imprintArgs, _ := json.Marshal(map[string]any{
		"workspace_id": wsID,
		"agent_id":     "9e8d7c6b-5a4f-4e3d-ac2b-1a0f9e8d7c6b",
		"content":      "pinned override test",
	})
	frame := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memora_imprint","arguments":` + string(imprintArgs) + `}}`
	resps2 := roundtrip(t, s, frame)
	if len(resps2) != 1 || resps2[0].Error != nil {
		t.Fatalf("imprint failed: %+v", resps2)
	}
	memID := extractToolMemoryID(t, resps2[0])

	mem, err := svc.Metadata.GetMemory(context.Background(), memID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.WrittenByAgentID != "4c1e2f3a-5b6d-4e7f-8a9b-0c1d2e3f4a5b" {
		t.Fatalf("WrittenByAgentID = %q, want %q (pinned override)", mem.WrittenByAgentID, "4c1e2f3a-5b6d-4e7f-8a9b-0c1d2e3f4a5b")
	}
}

func TestDispatch_NoAuthMode_ClientAgentIDFlows(t *testing.T) {
	svc := newTestService(t)
	s := NewServer(svc, testLogger())

	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_create_workspace","arguments":{"name":"noauth-test"}}}`,
	)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("setup failed: %+v", resps)
	}
	wsID := extractToolWorkspaceID(t, resps[1])

	imprintArgs, _ := json.Marshal(map[string]any{
		"workspace_id": wsID,
		"agent_id":     "f269ac4f-2488-4f87-bcb0-84fe098ea039",
		"content":      "no auth mode test",
	})
	frame := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memora_imprint","arguments":` + string(imprintArgs) + `}}`
	resps2 := roundtrip(t, s, frame)
	if len(resps2) != 1 || resps2[0].Error != nil {
		t.Fatalf("imprint failed: %+v", resps2)
	}
	memID := extractToolMemoryID(t, resps2[0])

	mem, err := svc.Metadata.GetMemory(context.Background(), memID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.WrittenByAgentID != "f269ac4f-2488-4f87-bcb0-84fe098ea039" {
		t.Fatalf("WrittenByAgentID = %q, want %q (client pass-through)", mem.WrittenByAgentID, "f269ac4f-2488-4f87-bcb0-84fe098ea039")
	}
}

// TestToolCall_NonUUIDID_ReturnsInvalidParams pins R1 at the MCP
// boundary: an ID argument that is not a UUIDv4 is -32602 (invalid
// params) naming the argument, for every ID argument the catalog uses.
func TestToolCall_NonUUIDID_ReturnsInvalidParams(t *testing.T) {
	s := NewServer(newTestService(t), testLogger())
	cases := []struct{ tool, arg string }{
		{"memora_lookup", "workspace_id"},
		{"memora_lookup", "memory_id"},
		{"memora_imprint", "collection_id"},
		{"memora_unpin", "pin_id"},
		{"memora_link", "source_memory_id"},
		{"memora_link", "target_memory_id"},
		{"memora_traverse", "seed_memory_id"},
		{"memora_unlink", "edge_id"},
	}
	for i, tc := range cases {
		args := map[string]any{"workspace_id": types.NewID(), "agent_id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}
		args[tc.arg] = "mem_01HXYZABCDEFGHJKMNPQRSTVWX"
		argsJSON, _ := json.Marshal(args)
		frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, i+2, tc.tool, argsJSON)
		resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, frame)
		if len(resps) != 2 || resps[1].Error == nil {
			t.Fatalf("%s/%s: want an error response, got %+v", tc.tool, tc.arg, resps)
		}
		if resps[1].Error.Code != -32602 || !strings.Contains(resps[1].Error.Message, tc.arg+" must be a UUID") {
			t.Errorf("%s/%s: error = %+v, want -32602 naming %s", tc.tool, tc.arg, resps[1].Error, tc.arg)
		}
	}
}
