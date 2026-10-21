package http_test

import (
	"bytes"
	"context"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/mcp"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/pkg/embedding"
)

// TestSearch_EmbedderFailure_NeverLeaksAPIKey (#6165): with a real
// Gemini provider whose endpoint drops every connection, a vector search
// fails with a fixed message. The sentinel API key appears in neither the
// HTTP response, the MCP error, nor the server log.
func TestSearch_EmbedderFailure_NeverLeaksAPIKey(t *testing.T) {
	const sentinel = "AIzaSENTINEL0123456789"
	dropper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer dropper.Close()
	t.Setenv("GEMINI_API_KEY", sentinel)
	t.Setenv("GEMINI_BASE_URL", dropper.URL)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	svc := newTestService(t)
	gemini, err := embedding.Open("gemini:text-embedding-004")
	if err != nil {
		t.Fatalf("open gemini provider: %v", err)
	}
	svc.Embedder = gemini
	srv := httpserver.New(httpserver.Config{Service: svc, Logger: slog.New(slog.NewTextHandler(&logs, nil)), MaxBodyBytes: httpserver.DefaultMaxBodyBytes})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status, body := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/search", map[string]any{"query": "x", "mode": "vector"}, nil))
	msg, _ := body["message"].(string)
	if status != 501 || !strings.Contains(msg, "embedding provider error") || strings.Contains(msg, sentinel) || strings.Contains(msg, dropper.URL) {
		t.Fatalf("HTTP search: status=%d message=%q; want 501 with the fixed message only", status, msg)
	}

	var out bytes.Buffer
	frames := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memora_search","arguments":{"query":"x","mode":"vector"}}}` + "\n"
	if err := mcp.NewServer(svc, stdlog.New(io.Discard, "", 0)).ServeStdio(context.Background(), strings.NewReader(frames), &out); err != nil {
		t.Fatalf("mcp: %v", err)
	}
	if !strings.Contains(out.String(), "embedding provider error") || strings.Contains(out.String(), sentinel) {
		t.Fatalf("MCP output %q; want the fixed error without the key", out.String())
	}

	if !strings.Contains(logs.String(), "embed query failed") {
		t.Fatalf("expected the redacted failure detail in the server log, got %q", logs.String())
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Fatalf("server log contains the API key: %q", logs.String())
	}
}
