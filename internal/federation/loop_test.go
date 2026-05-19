package federation

import (
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
)

func TestParseFederationPath_Empty(t *testing.T) {
	if got := ParseFederationPath(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseFederationPath_Single(t *testing.T) {
	got := ParseFederationPath("fed_abc")
	if len(got) != 1 || got[0] != "fed_abc" {
		t.Errorf("expected [fed_abc], got %v", got)
	}
}

func TestParseFederationPath_Multiple(t *testing.T) {
	got := ParseFederationPath("fed_a, fed_b, fed_c")
	if len(got) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(got))
	}
	if got[0] != "fed_a" || got[1] != "fed_b" || got[2] != "fed_c" {
		t.Errorf("unexpected: %v", got)
	}
}

func TestCheckLoop_NoLoop(t *testing.T) {
	if err := CheckLoop([]string{"fed_a"}, "fed_b"); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckLoop_SelfDetected(t *testing.T) {
	err := CheckLoop([]string{"fed_a", "fed_b"}, "fed_a")
	if !errors.Is(err, types.ErrFederationLoop) {
		t.Errorf("expected ErrFederationLoop, got %v", err)
	}
}

func TestCheckLoop_MaxHops(t *testing.T) {
	path := make([]string, MaxFederationHops)
	for i := range path {
		path[i] = "fed_other"
	}
	err := CheckLoop(path, "fed_local")
	if !errors.Is(err, types.ErrFederationLoop) {
		t.Errorf("expected ErrFederationLoop at max hops, got %v", err)
	}
}

func TestCheckLoop_UnderMaxHops(t *testing.T) {
	path := make([]string, MaxFederationHops-1)
	for i := range path {
		path[i] = "fed_other"
	}
	if err := CheckLoop(path, "fed_local"); err != nil {
		t.Errorf("expected no error under max hops, got %v", err)
	}
}

func TestAppendToPath_Empty(t *testing.T) {
	got := AppendToPath("", "fed_a")
	if got != "fed_a" {
		t.Errorf("expected fed_a, got %s", got)
	}
}

func TestAppendToPath_Existing(t *testing.T) {
	got := AppendToPath("fed_a", "fed_b")
	if got != "fed_a,fed_b" {
		t.Errorf("expected fed_a,fed_b, got %s", got)
	}
}
