package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	sqliteledger "github.com/axiom-studio/memora/internal/ledger/sqlite"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/pkg/adapter"
)

// TestLedger_NoLedgerStore_Is501 (#6153): a server built without a
// LedgerStore answers GET .../ledger with 501 capability_unavailable,
// not a recovered nil-pointer panic (500).
func TestLedger_NoLedgerStore_Is501(t *testing.T) {
	ts := newTestServer(t) // newTestService wires no ledger
	_, ws := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/workspaces", map[string]any{"name": "no-ledger"}, nil))
	status, body := doJSON(t, ts.Client(), jsonReq(t, "GET", ts.URL+"/v1/workspaces/"+ws["id"].(string)+"/ledger", nil, nil))
	if status != 501 || body["error_code"] != "capability_unavailable" {
		t.Fatalf("status=%d body=%v, want 501 capability_unavailable", status, body)
	}
}

// TestLedger_RecordsRequestID (#6156): a ledger entry written while
// serving a request records that request's X-Request-ID (client-supplied
// or generated), the same one the response echoes.
func TestLedger_RecordsRequestID(t *testing.T) {
	svc := newTestService(t)
	led := &sqliteledger.Store{}
	if err := led.Open(context.Background(), adapter.LedgerConfig{DSN: filepath.Join(t.TempDir(), "ledger.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = led.Close() })
	svc.Ledger = led
	srv := httpserver.New(httpserver.Config{Service: svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: httpserver.DefaultMaxBodyBytes})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, ws := doJSON(t, ts.Client(), jsonReq(t, "POST", ts.URL+"/v1/workspaces", map[string]any{"name": "reqid"}, nil))
	wsID := ws["id"].(string)
	for _, sent := range []string{"trace-123", ""} {
		h := map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}
		if sent != "" {
			h["X-Request-ID"] = sent
		}
		resp, err := ts.Client().Do(jsonReq(t, "POST", ts.URL+"/v1/workspaces/"+wsID+"/memories", map[string]any{"content": "x"}, h))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		echoed := resp.Header.Get("X-Request-ID")
		entries, _, err := led.Query(context.Background(), adapter.LedgerQuery{WorkspaceID: wsID, Op: []string{"imprint"}, Limit: 1})
		if err != nil || len(entries) != 1 {
			t.Fatalf("ledger: %v %v", entries, err)
		}
		if entries[0].RequestID == "" || entries[0].RequestID != echoed || (sent != "" && echoed != sent) {
			t.Fatalf("ledger request_id = %q, response echoed %q (sent %q)", entries[0].RequestID, echoed, sent)
		}
	}
}
