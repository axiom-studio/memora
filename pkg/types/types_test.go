package types

import (
	"strings"
	"testing"
)

func TestNewID_PrefixedULID(t *testing.T) {
	id := NewID(MemoryIDPrefix)
	if !strings.HasPrefix(id, MemoryIDPrefix) {
		t.Fatalf("NewID(mem_) = %q, want prefix %s", id, MemoryIDPrefix)
	}
	if len(id) != len(MemoryIDPrefix)+26 { // ULIDs are 26 chars
		t.Fatalf("expected ULID length 26 after prefix, got len=%d for %q", len(id), id)
	}
}

func TestMustHavePrefix(t *testing.T) {
	if err := MustHavePrefix("mem_abc", MemoryIDPrefix); err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
	if err := MustHavePrefix("", MemoryIDPrefix); err == nil {
		t.Fatal("empty id should be rejected")
	}
	if err := MustHavePrefix("ws_abc", MemoryIDPrefix); err == nil {
		t.Fatal("wrong-prefix id should be rejected")
	}
}

func TestEdgeValidate_SelfLoopRejected(t *testing.T) {
	e := &Edge{
		WorkspaceID:      "ws_a",
		SourceMemoryID:   "mem_x",
		TargetMemoryID:   "mem_x",
		EdgeType:         EdgeTypeReferences,
		CreatedByAgentID: "agent_opaque_test",
	}
	if err := e.Validate(); err == nil {
		t.Fatal("self-loop edge should be rejected")
	}
}

func TestEdgeValidate_BadEdgeType(t *testing.T) {
	e := &Edge{
		WorkspaceID:      "ws_a",
		SourceMemoryID:   "mem_x",
		TargetMemoryID:   "mem_y",
		EdgeType:         "not_a_real_type",
		CreatedByAgentID: "agent_opaque_test",
	}
	if err := e.Validate(); err == nil {
		t.Fatal("bad edge_type should be rejected")
	}
}

func TestEdgeValidate_Happy(t *testing.T) {
	e := &Edge{
		WorkspaceID:      "ws_a",
		SourceMemoryID:   "mem_x",
		TargetMemoryID:   "mem_y",
		EdgeType:         EdgeTypeReferences,
		CreatedByAgentID: "agent_opaque_test",
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("happy-path edge rejected: %v", err)
	}
}

func TestValidEdgeType(t *testing.T) {
	for _, et := range StoredEdgeTypes {
		if !ValidEdgeType(string(et)) {
			t.Errorf("%s should be valid", et)
		}
	}
	if ValidEdgeType("knows_about") {
		t.Error("unknown edge type should not be valid")
	}
}

func TestMemoryValidate(t *testing.T) {
	m := &Memory{
		WorkspaceID:      "ws_a",
		WrittenByAgentID: "agent_opaque_test",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("happy memory rejected: %v", err)
	}

	bad := &Memory{WorkspaceID: "ws_a"}
	if err := bad.Validate(); err == nil {
		t.Fatal("memory without agent_id should be rejected")
	}
}

func TestMD5Hex(t *testing.T) {
	if got := MD5Hex(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("MD5Hex(empty) = %s", got)
	}
}
