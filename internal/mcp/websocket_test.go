package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nhooyr.io/websocket"
)

func wsRoundtrip(t *testing.T, conn *websocket.Conn, frame string) response {
	t.Helper()
	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var r response
	if err := json.Unmarshal(msg, &r); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return r
}

func TestWebSocket_UpgradeRequiresBearerKey(t *testing.T) {
	svc := newTestService(t)
	handler := WebSocketHandler(svc, Config{APIKey: "ws-secret"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// No auth header → 401, no upgrade.
	_, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatal("expected dial to fail without auth")
	}

	// Bad auth header → 401.
	_, _, err = websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer wrong"}},
	})
	if err == nil {
		t.Fatal("expected dial to fail with bad key")
	}
}

func TestWebSocket_UpgradeAcceptedWithValidKey(t *testing.T) {
	svc := newTestService(t)
	handler := WebSocketHandler(svc, Config{APIKey: "ws-secret"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer ws-secret"}},
	})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()

	resp := wsRoundtrip(t, conn, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"ws-secret"}}`)
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	res, _ := resp.Result.(map[string]any)
	si, _ := res["serverInfo"].(map[string]any)
	if si["name"] != "memora-core" {
		t.Fatalf("unexpected serverInfo: %+v", si)
	}
}

func TestWebSocket_FullToolCallRoundtrip(t *testing.T) {
	svc := newTestService(t)
	handler := WebSocketHandler(svc, Config{APIKey: "ws-secret"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer ws-secret"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	resp := wsRoundtrip(t, conn, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"ws-secret"}}`)
	if resp.Error != nil {
		t.Fatalf("init: %+v", resp.Error)
	}

	resp = wsRoundtrip(t, conn, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_create_workspace","arguments":{"name":"ws-test"}}}`)
	if resp.Error != nil {
		t.Fatalf("create_workspace: %+v", resp.Error)
	}
	wsID := extractToolWorkspaceID(t, resp)

	imprintArgs, _ := json.Marshal(map[string]any{
		"workspace_id": wsID,
		"agent_id":     "ws_agent",
		"content":      "hello via websocket",
	})
	frame := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memora_imprint","arguments":` + string(imprintArgs) + `}}`
	resp = wsRoundtrip(t, conn, frame)
	if resp.Error != nil {
		t.Fatalf("imprint: %+v", resp.Error)
	}
	memID := extractToolMemoryID(t, resp)

	mem, err := svc.Metadata.GetMemory(context.Background(), memID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.WrittenByAgentID != "ws_agent" {
		t.Fatalf("WrittenByAgentID = %q, want ws_agent", mem.WrittenByAgentID)
	}
}

func TestWebSocket_TwoConcurrentConnections(t *testing.T) {
	svc := newTestService(t)
	handler := WebSocketHandler(svc, Config{APIKey: "ws-secret"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	dialOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer ws-secret"}},
	}

	conn1, _, err := websocket.Dial(context.Background(), wsURL, dialOpts)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.CloseNow()

	conn2, _, err := websocket.Dial(context.Background(), wsURL, dialOpts)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.CloseNow()

	// Initialize conn1 only. conn2 should still require its own init.
	resp1 := wsRoundtrip(t, conn1, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiKey":"ws-secret"}}`)
	if resp1.Error != nil {
		t.Fatalf("conn1 init: %+v", resp1.Error)
	}

	// conn1 can list tools.
	resp1 = wsRoundtrip(t, conn1, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if resp1.Error != nil {
		t.Fatalf("conn1 tools/list should succeed: %+v", resp1.Error)
	}

	// conn2 tools/list without init → unauthorized.
	var wg sync.WaitGroup
	var resp2 response
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp2 = wsRoundtrip(t, conn2, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	}()
	wg.Wait()

	if resp2.Error == nil || resp2.Error.Code != ErrCodeUnauthorized {
		t.Fatalf("conn2 tools/list before init should be unauthorized, got %+v", resp2.Error)
	}

	// Now init conn2 — it should work independently.
	resp2 = wsRoundtrip(t, conn2, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"apiKey":"ws-secret"}}`)
	if resp2.Error != nil {
		t.Fatalf("conn2 init: %+v", resp2.Error)
	}
	resp2 = wsRoundtrip(t, conn2, `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	if resp2.Error != nil {
		t.Fatalf("conn2 tools/list after init should succeed: %+v", resp2.Error)
	}
}
