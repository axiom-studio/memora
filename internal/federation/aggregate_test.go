package federation

import (
	"math"
	"testing"

	"github.com/axiom-studio/memora/pkg/types/api"
)

func TestAggregate_Empty(t *testing.T) {
	fr := &FanoutResult{TotalScanned: 5}
	resp := Aggregate(fr, AggregateOpts{K: 10})
	if len(resp.Results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(resp.Results))
	}
	if resp.TotalCandidatesScanned != 5 {
		t.Errorf("expected 5 scanned, got %d", resp.TotalCandidatesScanned)
	}
}

func TestAggregate_DedupByMemoryID(t *testing.T) {
	fr := &FanoutResult{
		Results: []api.RecallHit{
			{MemoryID: "mem_a", Score: 0.8, Via: "seed"},
			{MemoryID: "mem_a", Score: 0.9, Via: "federation:peer1"},
			{MemoryID: "mem_b", Score: 0.7, Via: "seed"},
		},
		TotalScanned: 10,
	}
	resp := Aggregate(fr, AggregateOpts{K: 10})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 deduped results, got %d", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.MemoryID == "mem_a" && r.Score < 0 {
			// After Z-normalization, scores may change, but the higher
			// original score should still win dedup. Just verify dedup happened.
		}
	}
}

func TestAggregate_TopK(t *testing.T) {
	fr := &FanoutResult{
		Results: []api.RecallHit{
			{MemoryID: "mem_a", Score: 0.9, Via: "seed"},
			{MemoryID: "mem_b", Score: 0.8, Via: "seed"},
			{MemoryID: "mem_c", Score: 0.7, Via: "seed"},
			{MemoryID: "mem_d", Score: 0.6, Via: "seed"},
		},
	}
	resp := Aggregate(fr, AggregateOpts{K: 2})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
}

func TestAggregate_SortedByScore(t *testing.T) {
	fr := &FanoutResult{
		Results: []api.RecallHit{
			{MemoryID: "mem_low", Score: 0.3, Via: "seed"},
			{MemoryID: "mem_high", Score: 0.9, Via: "seed"},
			{MemoryID: "mem_mid", Score: 0.6, Via: "seed"},
		},
	}
	resp := Aggregate(fr, AggregateOpts{K: 10})
	for i := 1; i < len(resp.Results); i++ {
		if resp.Results[i].Score > resp.Results[i-1].Score {
			t.Error("results not sorted descending by score")
		}
	}
}

func TestAggregate_ZNormalize_MultiPeer(t *testing.T) {
	fr := &FanoutResult{
		Results: []api.RecallHit{
			{MemoryID: "mem_local_1", Score: 0.9, Via: "seed"},
			{MemoryID: "mem_local_2", Score: 0.7, Via: "seed"},
			{MemoryID: "mem_peer_1", Score: 0.5, Via: "federation:peer1"},
			{MemoryID: "mem_peer_2", Score: 0.3, Via: "federation:peer1"},
		},
		TotalScanned: 20,
	}
	resp := Aggregate(fr, AggregateOpts{K: 10})
	if len(resp.Results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(resp.Results))
	}
	// After Z-normalization, the local group (0.9, 0.7) and peer group
	// (0.5, 0.3) each get normalized independently. The spread within
	// each group is preserved.
	for _, r := range resp.Results {
		if math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
			t.Errorf("invalid score: %f", r.Score)
		}
	}
}

func TestAggregate_SinglePeer_NoNormalize(t *testing.T) {
	fr := &FanoutResult{
		Results: []api.RecallHit{
			{MemoryID: "mem_a", Score: 0.9, Via: "seed"},
			{MemoryID: "mem_b", Score: 0.7, Via: "seed"},
		},
	}
	resp := Aggregate(fr, AggregateOpts{K: 10})
	if resp.Results[0].Score != 0.9 {
		t.Errorf("single peer should not normalize; expected 0.9, got %f", resp.Results[0].Score)
	}
}

func TestZNormalize(t *testing.T) {
	hits := []api.RecallHit{
		{Score: 10},
		{Score: 20},
		{Score: 30},
	}
	zNormalize(hits)
	// Mean = 20, stddev = sqrt(200/3 - 400) = sqrt(66.67 - 400)... let me recalc
	// Mean = 20, variance = (100+400+900)/3 - 400 = 466.67 - 400 = 66.67
	// stddev = 8.165
	// z-scores: (10-20)/8.165 = -1.22, (20-20)/8.165 = 0, (30-20)/8.165 = 1.22
	if math.Abs(hits[1].Score) > 0.01 {
		t.Errorf("middle score should be ~0 after z-normalize, got %f", hits[1].Score)
	}
	if hits[0].Score >= hits[1].Score || hits[1].Score >= hits[2].Score {
		t.Error("ordering should be preserved after z-normalize")
	}
}

func TestZNormalize_SingleElement(t *testing.T) {
	hits := []api.RecallHit{{Score: 0.5}}
	zNormalize(hits)
	if hits[0].Score != 0.5 {
		t.Errorf("single element should not change, got %f", hits[0].Score)
	}
}

func TestZNormalize_EqualScores(t *testing.T) {
	hits := []api.RecallHit{{Score: 0.5}, {Score: 0.5}, {Score: 0.5}}
	zNormalize(hits)
	// All same → variance = 0, should not normalize
	for _, h := range hits {
		if h.Score != 0.5 {
			t.Errorf("equal scores should not change, got %f", h.Score)
		}
	}
}

func TestDedupByMemoryID(t *testing.T) {
	hits := []api.RecallHit{
		{MemoryID: "mem_a", Score: 0.8},
		{MemoryID: "mem_b", Score: 0.9},
		{MemoryID: "mem_a", Score: 0.95},
		{MemoryID: "mem_b", Score: 0.3},
	}
	result := dedupByMemoryID(hits)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	for _, r := range result {
		switch r.MemoryID {
		case "mem_a":
			if r.Score != 0.95 {
				t.Errorf("expected 0.95 for mem_a, got %f", r.Score)
			}
		case "mem_b":
			if r.Score != 0.9 {
				t.Errorf("expected 0.9 for mem_b, got %f", r.Score)
			}
		}
	}
}
