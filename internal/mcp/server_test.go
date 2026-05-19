package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	if err := primary.Open(ctx, adapter.PrimaryConfig{Driver: "sqlite", DSN: filepath.Join(dir, "primary.db")}); err != nil {
		t.Fatalf("open primary: %v", err)
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
		Primary:  primary,
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
	const agentID = "agent_opaque_dispatch_test"
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

	mem, err := svc.Primary.GetMemory(context.Background(), memID)
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
	id, _ := payload["memory_id"].(string)
	if id == "" {
		t.Fatalf("imprint response missing memory_id: %+v", payload)
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
