package rank

import (
	"math"
	"testing"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func byMemory(h api.RecallHit) string { return h.MemoryID }

func hits(ids ...string) []api.RecallHit {
	out := make([]api.RecallHit, len(ids))
	for i, id := range ids {
		out[i] = api.RecallHit{MemoryID: id}
	}
	return out
}

// TestFuse_MatchesHandComputedRRF: d1 is ranked 1st and 3rd, d2 2nd and
// 1st, d3 3rd only, d4 2nd only, each list weighted 1.
func TestFuse_MatchesHandComputedRRF(t *testing.T) {
	got := Fuse([]List{
		{Hits: hits("d1", "d2", "d3"), Weight: 1},
		{Hits: hits("d2", "d4", "d1"), Weight: 1},
	}, byMemory, 0)
	want := map[string]float64{
		"d1": 1.0/61 + 1.0/63,
		"d2": 1.0/62 + 1.0/61,
		"d3": 1.0 / 63,
		"d4": 1.0 / 62,
	}
	order := []string{"d2", "d1", "d4", "d3"}
	if len(got) != len(order) {
		t.Fatalf("got %d hits, want %d", len(got), len(order))
	}
	for i, h := range got {
		if h.MemoryID != order[i] || math.Abs(h.Score-want[h.MemoryID]) > 1e-12 {
			t.Fatalf("rank %d = %s %.6f, want %s %.6f", i, h.MemoryID, h.Score, order[i], want[order[i]])
		}
	}
}

func TestFuse_WeightsAndTruncation(t *testing.T) {
	got := Fuse([]List{
		{Hits: hits("a"), Weight: 1},
		{Hits: hits("b"), Weight: 3},
	}, byMemory, 1)
	if len(got) != 1 || got[0].MemoryID != "b" {
		t.Fatalf("Fuse = %+v, want only b (triple weight)", got)
	}
}

// TestFuse_MergesSourceFieldsAndBreaksTiesByKey: a hit found by both
// lists carries both source scores and the vector side's cell; equal
// fused scores order by key so the result is deterministic.
func TestFuse_MergesSourceFieldsAndBreaksTiesByKey(t *testing.T) {
	kw := []api.RecallHit{{MemoryID: "m", KeywordScore: 2.5}}
	vec := []api.RecallHit{{MemoryID: "m", VectorScore: 0.9, CellID: "c"}}
	got := Fuse([]List{{Hits: kw, Weight: 1}, {Hits: vec, Weight: 1}}, byMemory, 0)
	if len(got) != 1 || got[0].KeywordScore != 2.5 || got[0].VectorScore != 0.9 || got[0].CellID != "c" {
		t.Fatalf("merged hit = %+v", got)
	}
	tied := Fuse([]List{{Hits: hits("z"), Weight: 1}, {Hits: hits("a"), Weight: 1}}, byMemory, 0)
	if tied[0].MemoryID != "a" || tied[1].MemoryID != "z" {
		t.Fatalf("tie order = %s,%s; want a,z", tied[0].MemoryID, tied[1].MemoryID)
	}
}

func TestFuse_Empty(t *testing.T) {
	if got := Fuse(nil, byMemory, 5); len(got) != 0 {
		t.Fatalf("Fuse(nil) = %+v", got)
	}
}
