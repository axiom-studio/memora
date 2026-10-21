package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/memora/internal/rank"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func hits(node string, ids ...string) NodeHits {
	l := NodeHits{NodeID: node}
	for i, id := range ids {
		// Scores on deliberately unrelated scales: Merge must ignore them.
		l.Hits = append(l.Hits, api.RecallHit{MemoryID: id, NodeID: node, Score: float64(1000 - i)})
	}
	return l
}

// TestMerge_RRFMatchesHandComputed: two ranked lists interleave exactly
// as Σ 1/(60+rank) says, whatever the raw scores were.
func TestMerge_RRFMatchesHandComputed(t *testing.T) {
	fr := &FanoutResult{Lists: []NodeHits{
		hits("A", "a1", "a2", "a3"),
		hits("B", "b1", "b2"),
	}}
	got := Merge(fr, 0)
	// a1 and b1 tie at 1/61, a2 and b2 at 1/62; ties break by key
	// ("A/…" < "B/…"); a3 is 1/63.
	want := []struct {
		key   string
		score float64
	}{
		{"A/a1", 1.0 / 61}, {"B/b1", 1.0 / 61}, {"A/a2", 1.0 / 62}, {"B/b2", 1.0 / 62}, {"A/a3", 1.0 / 63},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want %d", len(got), len(want))
	}
	for i, w := range want {
		key := got[i].NodeID + "/" + got[i].MemoryID
		if key != w.key || math.Abs(got[i].Score-w.score) > 1e-12 {
			t.Fatalf("hit %d = %s %.6f, want %s %.6f", i, key, got[i].Score, w.key, w.score)
		}
	}
	if top2 := Merge(fr, 2); len(top2) != 2 || top2[0].MemoryID != "a1" || top2[1].MemoryID != "b1" {
		t.Fatalf("Merge(k=2) = %+v, want a1, b1", top2)
	}
	if rank.RRFK != 60 {
		t.Fatalf("hand-computed scores assume RRFK 60, got %d", rank.RRFK)
	}
}

// TestMerge_DedupeOnNodeAndMemory: the same (node_id, memory_id) collapses
// to one hit whose score adds both ranks; the same memory_id on two nodes
// stays two hits.
func TestMerge_DedupeOnNodeAndMemory(t *testing.T) {
	fr := &FanoutResult{Lists: []NodeHits{
		hits("A", "shared", "a2"),
		{NodeID: "B", Hits: []api.RecallHit{{MemoryID: "shared", NodeID: "A"}, {MemoryID: "shared", NodeID: "B"}}},
	}}
	got := Merge(fr, 0)
	count := map[string]int{}
	for _, h := range got {
		count[h.NodeID+"/"+h.MemoryID]++
	}
	if count["A/shared"] != 1 || count["B/shared"] != 1 || len(got) != 3 {
		t.Fatalf("hits = %v, want A/shared and B/shared once each plus A/a2", count)
	}
	if got[0].NodeID != "A" || got[0].MemoryID != "shared" || math.Abs(got[0].Score-(1.0/61+1.0/61)) > 1e-12 {
		t.Fatalf("top = %+v, want A/shared with both ranks fused", got[0])
	}
}

// TestFanoutSearch_PeerAttributionFromConfig: a peer that stamps another
// node's ID on its hits, or returns a memory twice, is overruled: hits
// are attributed to the peer as configured, once each, best rank kept.
func TestFanoutSearch_PeerAttributionFromConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.RecallResponse{Results: []api.RecallHit{
			{MemoryID: "m1", NodeID: "someone-else", NodeName: "forged"},
			{MemoryID: "m2"},
			{MemoryID: "m1"},
		}})
	}))
	defer srv.Close()
	pc := testPeerClient(srv, "peer-x")
	fr, err := FanoutSearch(context.Background(), nil, "self", []*PeerClient{pc}, api.FederationSearchRequest{}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	l := fr.Lists[0].Hits
	if len(l) != 2 || l[0].MemoryID != "m1" || l[1].MemoryID != "m2" {
		t.Fatalf("peer list = %+v, want m1, m2", l)
	}
	for _, h := range l {
		if h.NodeID != "peer-x" || h.NodeName != "peer-x" || h.Via != "federation:peer-x" {
			t.Fatalf("hit %s attributed %s/%s via %s, want peer-x", h.MemoryID, h.NodeID, h.NodeName, h.Via)
		}
	}
}

// TestFailedPeers_ErrorCodes: each way a peer can fail maps to a code.
func TestFailedPeers_ErrorCodes(t *testing.T) {
	fr := &FanoutResult{Failures: []PeerFailure{
		{NodeID: "1", Name: "limited", Err: ErrOutboundRateLimited},
		{NodeID: "2", Name: "slow", Err: fmt.Errorf("peer slow search: %w", context.DeadlineExceeded)},
		{NodeID: "3", Name: "refused", Err: &PeerError{Status: 401, Code: "federation_unauthorized"}},
		{NodeID: "4", Name: "broken", Err: &PeerError{Status: 502}},
		{NodeID: "5", Name: "garbled", Err: errors.New("peer garbled search decode: EOF")},
	}}
	want := []string{"outbound_rate_limited", "timeout", "federation_unauthorized", "http_502", "bad_response"}
	got := fr.FailedPeers()
	for i, w := range want {
		if got[i].ErrorCode != w || got[i].NodeID != fr.Failures[i].NodeID || got[i].Name != fr.Failures[i].Name {
			t.Fatalf("failed peer %d = %+v, want code %s", i, got[i], w)
		}
	}
	if !fr.PartialSuccess() {
		t.Fatal("PartialSuccess = false with failures")
	}
}

// TestFailedPeers_Unreachable: a peer nobody listens on is "unreachable".
func TestFailedPeers_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	pc := testPeerClient(srv, "gone")
	srv.Close()
	fr, err := FanoutSearch(context.Background(), &api.RecallResponse{}, "self", []*PeerClient{pc}, api.FederationSearchRequest{}, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fp := fr.FailedPeers(); len(fp) != 1 || fp[0].ErrorCode != "unreachable" {
		t.Fatalf("failed peers = %+v, want unreachable", fp)
	}
	if _, ok := fr.PeerLatencyMS["self"]; !ok {
		t.Fatalf("peer_latency_ms = %v, want this node's entry", fr.PeerLatencyMS)
	}
}
