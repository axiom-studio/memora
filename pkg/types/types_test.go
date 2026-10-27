package types

import (
	"errors"
	"strings"
	"testing"
)

// Fixed UUIDv4 fixtures for the validator tests.
const (
	testWS   = "0e7d4d8a-5f1c-4b8e-9a3d-1c2b3a4d5e6f"
	testMemX = "6a1f0c2e-3b4d-4e5f-8a6b-7c8d9e0f1a2b"
	testMemY = "9b8a7c6d-5e4f-4a3b-b2c1-d0e9f8a7b6c5"
)

func TestNewID_IsCanonicalUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if _, err := ParseID("id", id); err != nil {
			t.Fatalf("NewID() = %q does not parse: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestParseID(t *testing.T) {
	const valid = "3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90"
	if got, err := ParseID("memory_id", valid); err != nil || got != valid {
		t.Fatalf("ParseID(valid) = %q, %v; want %q, nil", got, err, valid)
	}
	rejected := map[string]string{
		"empty":          "",
		"legacy prefix":  "mem_01HXYZABCDEFGHJKMNPQRSTVWX",
		"uppercase":      strings.ToUpper(valid),
		"braces":         "{" + valid + "}",
		"urn":            "urn:uuid:" + valid,
		"no hyphens":     strings.ReplaceAll(valid, "-", ""),
		"version 1":      "3f2b8c1e-6d4a-1f0b-9a7e-2c5d8e1f4a90",
		"version 7":      "0192b3c4-6d4a-7f0b-9a7e-2c5d8e1f4a90",
		"wrong variant":  "3f2b8c1e-6d4a-4f0b-ca7e-2c5d8e1f4a90",
		"nil uuid":       "00000000-0000-0000-0000-000000000000",
		"trailing space": valid + " ",
	}
	for name, id := range rejected {
		_, err := ParseID("memory_id", id)
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: ParseID(%q) err = %v, want ErrInvalidInput", name, id, err)
			continue
		}
		if !strings.Contains(err.Error(), "memory_id must be a UUID") {
			t.Errorf("%s: error %q does not name the field", name, err)
		}
	}
}

func TestEdgeValidate_SelfLoopRejected(t *testing.T) {
	e := &Edge{
		WorkspaceID:      testWS,
		SourceMemoryID:   testMemX,
		TargetMemoryID:   testMemX,
		EdgeType:         EdgeTypeReferences,
		CreatedByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7",
	}
	if err := e.Validate(); err == nil {
		t.Fatal("self-loop edge should be rejected")
	}
}

func TestEdgeValidate_BadEdgeType(t *testing.T) {
	e := &Edge{
		WorkspaceID:      testWS,
		SourceMemoryID:   testMemX,
		TargetMemoryID:   testMemY,
		EdgeType:         "not_a_real_type",
		CreatedByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7",
	}
	if err := e.Validate(); err == nil {
		t.Fatal("bad edge_type should be rejected")
	}
}

func TestEdgeValidate_Happy(t *testing.T) {
	e := &Edge{
		WorkspaceID:      testWS,
		SourceMemoryID:   testMemX,
		TargetMemoryID:   testMemY,
		EdgeType:         EdgeTypeReferences,
		CreatedByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7",
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
		WorkspaceID:      testWS,
		WrittenByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("happy memory rejected: %v", err)
	}

	bad := &Memory{WorkspaceID: testWS}
	if err := bad.Validate(); err == nil {
		t.Fatal("memory without agent_id should be rejected")
	}
}

// TestValidate_RejectsLegacyPrefixedIDs pins R1: validators accept only
// UUIDs, so a pre-UUID "ws_…"/"mem_…" ID is invalid input everywhere.
func TestValidate_RejectsLegacyPrefixedIDs(t *testing.T) {
	const legacyWS = "ws_01HXYZABCDEFGHJKMNPQRSTVWX"
	cases := map[string]interface{ Validate() error }{
		"memory":     &Memory{WorkspaceID: legacyWS, WrittenByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7"},
		"cell":       &Cell{MemoryID: "mem_01HXYZABCDEFGHJKMNPQRSTVWX"},
		"workspace":  &Workspace{ID: legacyWS, Name: "w"},
		"collection": &Collection{WorkspaceID: legacyWS, Name: "c"},
		"agent":      &Agent{AgentID: "fd5c17ad-0c70-4b3d-a408-01f3ae5b44c7", WorkspaceID: legacyWS, IdentityProvider: "opaque"},
		"edge": &Edge{WorkspaceID: testWS, SourceMemoryID: "mem_01HXYZABCDEFGHJKMNPQRSTVWX", TargetMemoryID: testMemY,
			EdgeType: EdgeTypeReferences, CreatedByAgentID: "2b4224a8-95b4-4171-9e6b-689847d53ac7"},
	}
	for name, v := range cases {
		if err := v.Validate(); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "must be a UUID") {
			t.Errorf("%s.Validate() = %v, want ErrInvalidInput naming a UUID field", name, err)
		}
	}
}

func TestMD5Hex(t *testing.T) {
	if got := MD5Hex(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("MD5Hex(empty) = %s", got)
	}
}
