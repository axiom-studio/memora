package embedding

import (
	"context"
	"testing"
)

func TestNoopProvider_Roundtrip(t *testing.T) {
	p, err := Open("noop:default")
	if err != nil {
		t.Fatal(err)
	}
	vs, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vs))
	}
	if len(vs[0]) != p.Dim() {
		t.Fatalf("vec dim %d != provider dim %d", len(vs[0]), p.Dim())
	}
}

func TestOpen_UnknownProvider(t *testing.T) {
	_, err := Open("nope:nope")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestOpen_DefaultsToNoop(t *testing.T) {
	p, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "noop" {
		t.Fatalf("default should be noop, got %s", p.Name())
	}
}
