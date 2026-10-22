package http_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/internal/federation"
	httpserver "github.com/axiom-studio/memora/internal/server/http"
	"github.com/axiom-studio/memora/internal/testmesh"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func (n *meshNode) ledger(t *testing.T, op string) []api.LedgerEntry {
	t.Helper()
	entries, _, err := n.Svc.Ledger.Query(context.Background(), adapter.LedgerQuery{Op: []string{op}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func (n *meshNode) metrics(t *testing.T) string {
	t.Helper()
	resp, err := n.TS.Client().Get(n.TS.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestFederationAudit_E2E (F-8): one federated search and one fetch
// write the expected entries in BOTH nodes' ledgers, with the peer's
// node ID as the actor; refused calls are audited with their reason;
// the /metrics counters move on both sides.
func TestFederationAudit_E2E(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	memB := b.seed(t, "meteor shower schedule")

	if status, body := a.search(t, map[string]any{"query": "meteor shower", "mode": "keyword", "federated": true}); status != 200 {
		t.Fatalf("search: %d %v", status, body)
	}
	if status, env := a.Do(t, "GET", "/v1/memories/"+memB+"?node="+b.ID, nil, nil); status != 200 {
		t.Fatalf("fetch: %d %v", status, env)
	}

	// Caller side.
	s := a.ledger(t, federation.OpSearch)
	if len(s) != 1 {
		t.Fatalf("a's federation_search entries = %d, want 1", len(s))
	}
	m := s[0].Metadata
	asked, _ := m["peers_asked"].([]any)
	hits, _ := m["hits_per_node"].(map[string]any)
	if m["query"] != "meteor shower" || len(asked) != 1 || asked[0] != b.ID || fmt.Sprint(hits[b.ID]) != "1" || fmt.Sprint(hits[a.ID]) != "0" {
		t.Fatalf("a's search entry = %+v", s[0])
	}
	if f := a.ledger(t, federation.OpFetch); len(f) != 1 || f[0].APIKeyID != b.ID || f[0].Target != memB || f[0].Metadata["direction"] != "outbound" || f[0].Metadata["outcome"] != "ok" {
		t.Fatalf("a's fetch entries = %+v", f)
	}

	// Receiver side.
	in := b.ledger(t, federation.OpInbound)
	if len(in) != 1 || in[0].APIKeyID != a.ID || in[0].Metadata["query"] != "meteor shower" || fmt.Sprint(in[0].Metadata["hit_count"]) != "1" {
		t.Fatalf("b's inbound entries = %+v", in)
	}
	if f := b.ledger(t, federation.OpFetch); len(f) != 1 || f[0].APIKeyID != a.ID || f[0].Metadata["direction"] != "inbound" || f[0].WorkspaceID == "" {
		t.Fatalf("b's fetch entries = %+v", f)
	}

	// Refused calls are audited with their reason; the caller sees only
	// the uniform 401.
	call := func(node, key string) int {
		req, _ := http.NewRequest("POST", b.TS.URL+"/v1/federation/search", strings.NewReader(`{"query":"x"}`))
		req.Header.Set(federation.HeaderLocalInstanceID, node)
		req.Header.Set(federation.HeaderFederationAuth, "Bearer "+key)
		resp, err := b.TS.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	stranger := types.NewID()
	if call(a.ID, "wrong-key") != 401 || call(stranger, "anything") != 401 || call("not-a-node", "x") != 401 {
		t.Fatal("refused calls must be 401")
	}
	// Unauthenticated refusals arrive as summaries when the window
	// (testmesh.RejectWindow) closes.
	// An unknown peer's claimed ID is not the actor (it is attacker-chosen,
	// #6232); a well-formed one is kept as a sample.
	reasons := map[string]string{}
	var samples []string
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		samples = nil
		for _, e := range b.ledger(t, federation.OpRejected) {
			reasons[e.APIKeyID] = fmt.Sprint(e.Metadata["reason"])
			if s, ok := e.Metadata["sample_claimed_peers"].([]any); ok {
				for _, v := range s {
					samples = append(samples, fmt.Sprint(v))
				}
			}
		}
		if len(reasons) == 2 && len(samples) > 0 || time.Now().After(deadline) {
			break
		}
	}
	if reasons[a.ID] != federation.ReasonBadKey || reasons[""] != federation.ReasonUnknownPeer || len(samples) != 1 || samples[0] != stranger {
		t.Fatalf("rejection reasons = %v, samples %v", reasons, samples)
	}

	// Metrics on both sides.
	am, bm := a.metrics(t), b.metrics(t)
	for _, want := range []string{
		fmt.Sprintf(`memora_federation_peer_requests_total{peer=%q,outcome="ok"} 2`, b.ID),
		fmt.Sprintf(`memora_federation_peer_latency_seconds_count{peer=%q} 2`, b.ID),
	} {
		if !strings.Contains(am, want) {
			t.Fatalf("a's /metrics lacks %s:\n%s", want, am)
		}
	}
	for _, want := range []string{
		fmt.Sprintf(`memora_federation_inbound_total{peer=%q,outcome="ok"} 2`, a.ID),
		fmt.Sprintf(`memora_federation_inbound_total{peer=%q,outcome="bad_key"} 1`, a.ID),
		`memora_federation_inbound_total{peer="unknown",outcome="unknown_peer"} 2`,
	} {
		if !strings.Contains(bm, want) {
			t.Fatalf("b's /metrics lacks %s:\n%s", want, bm)
		}
	}
	if strings.Contains(bm, stranger) {
		t.Fatal("an unknown caller's claimed node ID became a metric label")
	}
}

// TestFederationAudit_QueryRedactable: the query text lives in the entry
// metadata, so the ledger's redaction hides it.
func TestFederationAudit_QueryRedactable(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a := nodes[0]
	a.search(t, map[string]any{"query": "patient record 4711", "mode": "keyword", "federated": true})
	e := a.ledger(t, federation.OpSearch)[0]
	if err := a.Svc.Ledger.Redact(context.Background(), e.LedgerID, []string{"metadata"}); err != nil {
		t.Fatal(err)
	}
	if got := a.ledger(t, federation.OpSearch)[0]; strings.Contains(fmt.Sprint(got.Metadata), "4711") || !got.Redacted {
		t.Fatalf("after redaction = %+v", got)
	}
}

// TestFederationRateLimit_PerPeer (F-8): a peer's own inbound_qps trips
// at that rate, is audited, and the caller sees it as a failed peer.
func TestFederationRateLimit_PerPeer(t *testing.T) {
	nodes := newMesh(t, []string{"a", "b"}, nil)
	a, b := nodes[0], nodes[1]
	if _, err := b.Peers.Upsert(context.Background(), api.PeerRequest{NodeID: a.ID, Name: "a", URL: a.TS.URL, InboundQPS: 0.5}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"query": "x", "mode": "keyword", "federated": true}
	if _, first := a.search(t, body); first["failed_peers"] != nil {
		t.Fatalf("first call: %v", first["failed_peers"])
	}
	_, second := a.search(t, body)
	failed, _ := second["failed_peers"].([]any)
	if len(failed) != 1 || failed[0].(map[string]any)["error_code"] != "rate_limited" {
		t.Fatalf("second call at 0.5 QPS: failed_peers %v, want rate_limited", second["failed_peers"])
	}
	var limited int
	for _, e := range b.ledger(t, federation.OpRejected) {
		if e.Metadata["reason"] == federation.ReasonRateLimited && e.APIKeyID == a.ID {
			limited++
		}
	}
	if limited != 1 {
		t.Fatalf("rate_limited rejections on b = %d, want 1", limited)
	}
	if !strings.Contains(a.metrics(t), fmt.Sprintf(`memora_federation_peer_requests_total{peer=%q,outcome="rejected"} 1`, b.ID)) {
		t.Fatalf("a's metrics: %s", a.metrics(t))
	}
}

// TestFederationRejections_NoLedgerFlood (#6178): a flood of
// unauthenticated peer-route calls is cut off per client address before
// authentication, and what gets through is ledgered as one summary per
// caller and window, never as a row per request; a long path never
// reaches the ledger.
func TestFederationRejections_NoLedgerFlood(t *testing.T) {
	b := newMesh(t, []string{"b"}, nil)[0]
	stranger := types.NewID()
	post := func(path string) int {
		req, _ := http.NewRequest("POST", b.TS.URL+path, strings.NewReader(`{"query":"x"}`))
		req.Header.Set(federation.HeaderLocalInstanceID, stranger)
		req.Header.Set(federation.HeaderFederationAuth, "Bearer guess")
		resp, err := b.TS.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	codes := map[int]int{}
	start := time.Now()
	for i := 0; i < 2000; i++ {
		codes[post("/v1/federation/search")]++
	}
	windows := int(time.Since(start)/testmesh.RejectWindow) + 2
	if codes[401] == 0 || codes[429] == 0 || codes[401]+codes[429] != 2000 {
		t.Fatalf("status counts = %v, want 401s then 429s from the pre-auth limit", codes)
	}

	// One summary per window, together counting every 401.
	var rows []api.LedgerEntry
	total := 0
	for deadline := time.Now().Add(2 * time.Second); total != codes[401] && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		rows, total = b.ledger(t, federation.OpRejected), 0
		for _, e := range rows {
			n, _ := strconv.Atoi(fmt.Sprint(e.Metadata["count"]))
			total += n
		}
	}
	if total != codes[401] || len(rows) > windows {
		t.Fatalf("%d rows counting %d refusals; want at most %d rows counting %d", len(rows), total, windows, codes[401])
	}
	for _, e := range rows {
		if e.Metadata["reason"] != federation.ReasonUnknownPeer || e.APIKeyID != "" || fmt.Sprint(e.Metadata["sample_claimed_peers"]) != "["+stranger+"]" || e.Target != federation.RouteFederationSearch || e.Metadata["client"] != "127.0.0.1" {
			t.Fatalf("summary = %+v %v, want unknown_peer from 127.0.0.1 on the search route", e, e.Metadata)
		}
	}

	// Long and malformed paths: refused by the header limit or the route
	// table, never written to the ledger.
	time.Sleep(time.Second) // refill the pre-auth limit
	filler := strings.Repeat("A", 100<<10)
	// (A served node also caps the request line and headers at 64 KiB,
	// http.Server.MaxHeaderBytes; httptest's server does not apply it.)
	for _, p := range []string{"/v1/federation/memories/" + filler, "/v1/federation/memories/" + filler[:8<<10], "/v1/federation/search/" + filler[:8<<10]} {
		if got := post(p); got != 400 && got != 404 && got != 405 {
			t.Fatalf("%d-byte path: %d, want a route-table refusal", len(p), got)
		}
	}
	time.Sleep(3 * testmesh.RejectWindow)
	for _, e := range b.ledger(t, federation.OpRejected) {
		if strings.Contains(toJSON(t, e), "AAAA") {
			t.Fatalf("a request path reached the ledger: target %q", e.Target)
		}
		if e.Target != federation.RouteFederationSearch && e.Target != federation.RouteFederationMemory {
			t.Fatalf("target %q is not a route template", e.Target)
		}
	}
}

// TestShutdown_FlushesRejectionSummaries (#6232): a graceful shutdown
// writes the unauthenticated-refusal summaries of the open window.
func TestShutdown_FlushesRejectionSummaries(t *testing.T) {
	b := newMesh(t, []string{"b"}, nil)[0]
	reg, err := federation.NewRegistry(config.FederationConfig{Enabled: true}, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httpserver.New(httpserver.Config{
		Service: b.Svc, Federation: reg, MaxBodyBytes: httpserver.DefaultMaxBodyBytes,
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		FederationRejectWindow: time.Hour, // never flushes by itself here
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/v1/federation/search", strings.NewReader(`{"query":"x"}`))
	req.Header.Set(federation.HeaderLocalInstanceID, types.NewID())
	req.Header.Set(federation.HeaderFederationAuth, "Bearer guess")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unknown peer: %d, want 401", resp.StatusCode)
	}
	before := len(b.ledger(t, federation.OpRejected))
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := len(b.ledger(t, federation.OpRejected)); after != before+1 {
		t.Fatalf("rejection rows %d -> %d across Shutdown, want one summary written", before, after)
	}
}
