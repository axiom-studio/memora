package embedding

import (
	"context"
	"math"
	"testing"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// TestHashProvider_LexicalSimilarity: deterministic, unit-length, case-
// and punctuation-insensitive, and more shared words mean more similar.
func TestHashProvider_LexicalSimilarity(t *testing.T) {
	p, err := Open("hash:default")
	if err != nil {
		t.Fatal(err)
	}
	if !Searchable(p) || p.Dim() != hashDim {
		t.Fatalf("hash provider: searchable=%v dim=%d", Searchable(p), p.Dim())
	}
	vs, _ := p.Embed(context.Background(), []string{
		"NVDA dropped on export controls",
		"nvda DROPPED, on export controls!",
		"NVDA export outlook",
		"lunch menu for tuesday",
		"東京 タワー 東京",
	})
	if a, b := cosine(vs[0], vs[1]), 1.0; math.Abs(a-b) > 1e-6 {
		t.Errorf("same words, different case/punctuation: cosine %v, want 1", a)
	}
	if related, unrelated := cosine(vs[0], vs[2]), cosine(vs[0], vs[3]); related <= unrelated {
		t.Errorf("shared-word cosine %v should exceed disjoint cosine %v", related, unrelated)
	}
	for i, v := range vs {
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		if math.Abs(n-1) > 1e-5 {
			t.Errorf("vector %d has squared norm %v, want 1", i, n)
		}
	}
	again, _ := p.Embed(context.Background(), []string{"NVDA dropped on export controls"})
	if cosine(again[0], vs[0]) < 1-1e-9 {
		t.Error("hash embedding is not deterministic")
	}
}

func TestSearchable(t *testing.T) {
	noop, _ := Open("noop:default")
	hash, _ := Open("hash:default")
	if Searchable(noop) || Searchable(nil) || !Searchable(hash) {
		t.Fatalf("Searchable: noop=%v nil=%v hash=%v; want false false true", Searchable(noop), Searchable(nil), Searchable(hash))
	}
}
