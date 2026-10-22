package embedding

import (
	"errors"
	"testing"
)

// TestAxiomStudioProvider_FailsAtOpen: the Cloud gateway is not wired in
// OSS, so a node configured for it fails at start-up (Open) instead of
// serving keyword-only results while every embed silently fails.
func TestAxiomStudioProvider_FailsAtOpen(t *testing.T) {
	if _, err := Open("axiomstudio:default"); !errors.Is(err, errNotImplemented) {
		t.Fatalf("Open(axiomstudio) err = %v, want errNotImplemented", err)
	}
}
