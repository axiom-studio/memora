package federation

import (
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestSearchEntry(t *testing.T) {
	fr := &FanoutResult{
		Asked: []string{"b", "c"},
		Lists: []NodeHits{
			{NodeID: "a", Hits: []api.RecallHit{{MemoryID: "m1"}, {MemoryID: "m2"}}},
			{NodeID: "b", Hits: []api.RecallHit{{MemoryID: "m3"}}},
		},
		Failures: []PeerFailure{{NodeID: "c", Name: "c", Err: ErrOutboundRateLimited}},
	}
	e := SearchEntry("secret query", fr)
	if e.Op != OpSearch || e.WorkspaceID != "" {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := types.ParseID("ledger_id", e.LedgerID); err != nil {
		t.Fatal(err)
	}
	m := e.Metadata
	hits := m["hits_per_node"].(map[string]int)
	if m["query"] != "secret query" || m["total_hits"] != 3 || hits["a"] != 2 || hits["b"] != 1 {
		t.Fatalf("metadata = %v", m)
	}
	if fp := m["failed_peers"].([]api.FailedPeer); len(fp) != 1 || fp[0].ErrorCode != "outbound_rate_limited" {
		t.Fatalf("failed_peers = %v", m["failed_peers"])
	}
	if _, ok := SearchEntry("q", &FanoutResult{}).Metadata["failed_peers"]; ok {
		t.Fatal("failed_peers set without failures")
	}
}

func TestPeerEntries_ActorIsPeer(t *testing.T) {
	for _, e := range []api.LedgerEntry{
		InboundSearchEntry("peer-x", "q", []string{"peer-x"}, 4),
		RejectedEntry("peer-x", ReasonBadKey, "/v1/federation/search"),
		FetchEntry("inbound", "peer-x", "mem", "ws", OutcomeOK),
	} {
		if e.APIKeyID != "peer-x" {
			t.Fatalf("%s actor = %q, want the peer", e.Op, e.APIKeyID)
		}
	}
	if e := FetchEntry("outbound", "n", "mem", "", OutcomeNotFound); e.Target != "mem" || e.Metadata["outcome"] != OutcomeNotFound {
		t.Fatalf("fetch entry = %+v", e)
	}
}

func TestRejectReason(t *testing.T) {
	if got := RejectReason(refuse(ReasonBadCert, "x")); got != ReasonBadCert {
		t.Fatalf("got %s", got)
	}
	if got := RejectReason(types.ErrFederationLoop); got != ReasonLoop {
		t.Fatalf("got %s", got)
	}
	if got := RejectReason(errors.New("other")); got != "error" {
		t.Fatalf("got %s", got)
	}
	if !errors.Is(refuse(ReasonBadKey, "x"), types.ErrFederationAuth) {
		t.Fatal("refusal does not wrap ErrFederationAuth")
	}
}

func TestOutcomeOf(t *testing.T) {
	cases := map[error]string{
		nil:                     OutcomeOK,
		ErrOutboundRateLimited:  OutcomeRateLimited,
		&PeerError{Status: 401}: OutcomeRejected,
		&PeerError{Status: 502}: OutcomeError,
		types.ErrNotFound:       OutcomeNotFound,
		errors.New("garbled"):   OutcomeError,
	}
	for err, want := range cases {
		if got := OutcomeOf(err); got != want {
			t.Errorf("OutcomeOf(%v) = %s, want %s", err, got, want)
		}
	}
}
