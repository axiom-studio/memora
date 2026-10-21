// Package rank fuses ranked result lists. Rank fusion is used wherever
// lists scored on different scales meet: keyword and vector hits in
// hybrid search, and local and peer hits in federated search.
package rank

import (
	"sort"

	"github.com/axiom-studio/memora/pkg/types/api"
)

// RRFK is the reciprocal-rank-fusion constant (Cormack et al., 2009). It
// damps the top ranks so that no single list dominates the fused order.
const RRFK = 60

// List is one ranked result list, best first, with each key at most
// once, and the weight of its votes.
type List struct {
	Hits   []api.RecallHit
	Weight float64
}

// Fuse merges lists by weighted reciprocal rank fusion:
//
//	score(d) = Σ_i weight_i / (RRFK + rank_i(d)), rank 1-based
//
// Ranks, not raw scores, are combined, so lists on unrelated scales
// (BM25 and cosine; two nodes' embedding models) fuse fairly. A hit's
// identity is key(hit). The fused hit takes its fields from the first
// list that has it, plus the VectorScore, KeywordScore and CellID that a
// later list contributes. Ties are broken by key for a deterministic
// order. The result is at most k hits. O(n log n) in the total hit count.
func Fuse(lists []List, key func(api.RecallHit) string, k int) []api.RecallHit {
	type entry struct {
		hit   api.RecallHit
		score float64
	}
	fused := map[string]*entry{}
	order := []string{}
	for _, l := range lists {
		for i, h := range l.Hits {
			id := key(h)
			e, ok := fused[id]
			if !ok {
				e = &entry{hit: h}
				fused[id] = e
				order = append(order, id)
			} else {
				if e.hit.VectorScore == 0 {
					e.hit.VectorScore = h.VectorScore
				}
				if e.hit.KeywordScore == 0 {
					e.hit.KeywordScore = h.KeywordScore
				}
				if e.hit.CellID == "" {
					e.hit.CellID = h.CellID
				}
			}
			e.score += l.Weight / float64(RRFK+i+1)
		}
	}
	out := make([]api.RecallHit, 0, len(order))
	for _, id := range order {
		e := fused[id]
		e.hit.Score = e.score
		out = append(out, e.hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return key(out[i]) < key(out[j])
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}
