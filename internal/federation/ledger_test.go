package federation

import (
	"strings"
	"testing"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestNewOutboundRecallEntry(t *testing.T) {
	fr := &FanoutResult{
		Results:       []api.RecallHit{{MemoryID: "m1"}, {MemoryID: "m2"}},
		TotalScanned:  50,
		PartialSuccess: true,
		FailedPeers:   []string{"dead-peer"},
		PeerLatencyMS: map[string]int{"peer1": 10},
	}
	e := NewOutboundRecallEntry("ws_test", "fed_abc", fr)
	if e.Op != "federation_recall" {
		t.Errorf("expected federation_recall, got %s", e.Op)
	}
	if !strings.HasPrefix(e.LedgerID, "lg_") {
		t.Errorf("expected lg_ prefix, got %s", e.LedgerID)
	}
	if e.Metadata["federation_id"] != "fed_abc" {
		t.Errorf("expected fed_abc, got %v", e.Metadata["federation_id"])
	}
	if e.Metadata["partial_success"] != true {
		t.Error("expected partial_success=true")
	}
}

func TestNewOutboundRecallEntry_NoFailures(t *testing.T) {
	fr := &FanoutResult{
		Results:      []api.RecallHit{{MemoryID: "m1"}},
		TotalScanned: 10,
	}
	e := NewOutboundRecallEntry("ws_test", "fed_abc", fr)
	if _, ok := e.Metadata["partial_success"]; ok {
		t.Error("should not have partial_success when no failures")
	}
}

func TestNewOutboundLookupEntry_Found(t *testing.T) {
	e := NewOutboundLookupEntry("ws_test", "fed_abc", "mem_123", true, "peer-a")
	if e.Op != "federation_lookup" {
		t.Errorf("expected federation_lookup, got %s", e.Op)
	}
	if e.Target != "mem_123" {
		t.Errorf("expected mem_123 target, got %s", e.Target)
	}
	if e.Metadata["found"] != true {
		t.Error("expected found=true")
	}
	if e.Metadata["found_on_peer"] != "peer-a" {
		t.Errorf("expected found_on_peer=peer-a, got %v", e.Metadata["found_on_peer"])
	}
}

func TestNewOutboundLookupEntry_NotFound(t *testing.T) {
	e := NewOutboundLookupEntry("ws_test", "fed_abc", "mem_404", false, "")
	if e.Metadata["found"] != false {
		t.Error("expected found=false")
	}
	if _, ok := e.Metadata["found_on_peer"]; ok {
		t.Error("should not have found_on_peer when not found")
	}
}

func TestNewInboundEntry(t *testing.T) {
	e := NewInboundEntry("ws_test", "peer-x", "fed_a,fed_b", "recall")
	if e.Op != "federation_inbound" {
		t.Errorf("expected federation_inbound, got %s", e.Op)
	}
	if e.AgentID != "peer-x" {
		t.Errorf("expected peer-x agent, got %s", e.AgentID)
	}
	if e.Metadata["inbound_op"] != "recall" {
		t.Errorf("expected recall, got %v", e.Metadata["inbound_op"])
	}
}

func TestNewRejectedEntry(t *testing.T) {
	e := NewRejectedEntry("ws_test", "bad-peer", "not authorized")
	if e.Op != "federation_rejected" {
		t.Errorf("expected federation_rejected, got %s", e.Op)
	}
	if e.Metadata["reason"] != "not authorized" {
		t.Errorf("unexpected reason: %v", e.Metadata["reason"])
	}
}
