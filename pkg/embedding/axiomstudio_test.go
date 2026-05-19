package embedding

import (
	"context"
	"errors"
	"testing"
)

func TestAxiomStudioProvider_Returns501(t *testing.T) {
	p, err := Open("axiomstudio:default")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected 501 error")
	}
	if !errors.Is(err, errNotImplemented) {
		t.Errorf("wrong error: %v", err)
	}
}

func TestAxiomStudioProvider_Capabilities(t *testing.T) {
	p, _ := Open("axiomstudio:default")
	caps := p.Capabilities()
	if caps.Quality != "placeholder" {
		t.Errorf("quality = %q, want placeholder", caps.Quality)
	}
	if caps.SupportsBatch {
		t.Error("placeholder should not claim batch support")
	}
}
