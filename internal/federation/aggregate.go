package federation

import (
	"math"
	"sort"
	"strings"

	"github.com/axiom-studio/memora/pkg/types/api"
)

// AggregateOpts controls how federated recall results are merged.
type AggregateOpts struct {
	K               int
	LocalInstanceID string
}

// Aggregate deduplicates and rank-merges a FanoutResult into a final
// RecallResponse. Dedup is by MemoryID (content_md5 dedup once the
// wire placeholders land). When results come from peers with different
// embedding models, Z-score normalization is applied per peer group
// before merging.
func Aggregate(fr *FanoutResult, opts AggregateOpts) api.RecallResponse {
	if fr == nil || len(fr.Results) == 0 {
		return api.RecallResponse{TotalCandidatesScanned: fr.TotalScanned}
	}

	groups := groupByPeer(fr.Results)
	needsNormalize := len(groups) > 1

	if needsNormalize {
		for peer, hits := range groups {
			zNormalize(hits)
			groups[peer] = hits
		}
	}

	var all []api.RecallHit
	for _, hits := range groups {
		all = append(all, hits...)
	}

	deduped := dedupByMemoryID(all)

	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	if opts.K > 0 && len(deduped) > opts.K {
		deduped = deduped[:opts.K]
	}

	return api.RecallResponse{
		Results:                deduped,
		TotalCandidatesScanned: fr.TotalScanned,
	}
}

func groupByPeer(hits []api.RecallHit) map[string][]api.RecallHit {
	groups := map[string][]api.RecallHit{}
	for _, h := range hits {
		peer := peerFromVia(h.Via)
		groups[peer] = append(groups[peer], h)
	}
	return groups
}

func peerFromVia(via string) string {
	if strings.HasPrefix(via, "federation:") {
		return via[len("federation:"):]
	}
	return "local"
}

// zNormalize converts raw scores to Z-scores within a result group.
// This calibrates results from different embedding models onto a
// comparable scale.
func zNormalize(hits []api.RecallHit) {
	if len(hits) < 2 {
		return
	}
	var sum, sumSq float64
	for _, h := range hits {
		sum += h.Score
		sumSq += h.Score * h.Score
	}
	n := float64(len(hits))
	mean := sum / n
	variance := sumSq/n - mean*mean
	if variance <= 0 {
		return
	}
	stddev := math.Sqrt(variance)
	for i := range hits {
		hits[i].Score = (hits[i].Score - mean) / stddev
	}
}

// dedupByMemoryID keeps the highest-score hit per memory_id.
func dedupByMemoryID(hits []api.RecallHit) []api.RecallHit {
	best := map[string]int{}
	var result []api.RecallHit
	for _, h := range hits {
		if idx, ok := best[h.MemoryID]; ok {
			if h.Score > result[idx].Score {
				result[idx] = h
			}
		} else {
			best[h.MemoryID] = len(result)
			result = append(result, h)
		}
	}
	return result
}
