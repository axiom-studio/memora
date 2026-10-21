package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// seedFilterCorpus writes, across two workspaces, 150 decoys that match
// the query strongly and fail every filter, and 12 targets per filter
// kind that match it weakly. Unfiltered top-k is all decoys, so a search
// that filters after top-k returns nothing; S-4 must still return k.
func seedFilterCorpus(t *testing.T, s *Service) (wsA, wsB, otherAgent string, after time.Time) {
	t.Helper()
	ctx := context.Background()
	wsA, wsB = newTestWorkspace(t, s), newTestWorkspace(t, s)
	otherAgent = types.NewID()
	off := false
	write := func(ws, agent, content string, tags map[string]string) {
		if _, err := s.Imprint(ctx, ws, agent, api.ImprintRequest{Content: content, Tags: tags, AutoLink: &off}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 150; i++ {
		ws := wsA
		if i%2 == 1 {
			ws = wsB
		}
		write(ws, testAgent, fmt.Sprintf("comet comet comet comet decoy %d", i), map[string]string{"kind": "decoy"})
	}
	time.Sleep(5 * time.Millisecond)
	after = time.Now().UTC()
	for i := 0; i < 12; i++ {
		ws := wsA
		if i%2 == 1 {
			ws = wsB
		}
		filler := fmt.Sprintf("orbit plot spectrum telescope mirror lens dome night sky survey %d", i)
		write(ws, testAgent, "comet "+filler+" tagged", map[string]string{"kind": "target"})
		write(ws, otherAgent, "comet "+filler+" by other agent", map[string]string{"kind": "decoy"})
	}
	return wsA, wsB, otherAgent, after
}

// TestFilters_ReturnKMatches (S-4): with agent, tag and time filters,
// keyword, vector and hybrid search return k matching memories even
// though the unfiltered top results all fail the filter, both in one
// workspace (Recall) and node-wide (Search).
func TestFilters_ReturnKMatches(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	wsA, _, otherAgent, after := seedFilterCorpus(t, s)
	const k = 10

	filters := map[string]api.RecallFilters{
		"tag":   {Tags: map[string][]string{"kind": {"target"}}},
		"agent": {AgentID: otherAgent},
		"time":  {TsAfter: &after},
	}
	check := func(t *testing.T, name string, hits []api.RecallHit, f api.RecallFilters, want int) {
		t.Helper()
		if len(hits) != want {
			t.Fatalf("%s: %d hits, want %d", name, len(hits), want)
		}
		for _, h := range hits {
			m, err := s.Metadata.GetMemory(ctx, h.MemoryID)
			if err != nil {
				t.Fatal(err)
			}
			if !passesFilter(*m, f) {
				t.Fatalf("%s: hit %s fails the filter (tags %v, agent %s, created %s)", name, h.MemoryID, m.Tags, m.WrittenByAgentID, m.CreatedAt)
			}
		}
	}
	for fname, f := range filters {
		for _, mode := range []api.RecallMode{api.RecallModeKeyword, api.RecallModeVector, api.RecallModeHybrid} {
			// Node-wide: matches exist in both workspaces, 12 (tag, agent) or 24 (time).
			r, err := s.Search(ctx, api.SearchRequest{Query: "comet", Mode: mode, K: k, Filters: f})
			if err != nil {
				t.Fatal(err)
			}
			check(t, fmt.Sprintf("search/%s/%s", fname, mode), r.Results, f, k)
			// One workspace: wsA holds 6 tag/agent matches and 12 time matches.
			rr, err := s.Recall(ctx, wsA, api.RecallRequest{Query: "comet", Mode: mode, K: k, Filters: f})
			if err != nil {
				t.Fatal(err)
			}
			want := 6
			if fname == "time" {
				want = k
			}
			check(t, fmt.Sprintf("recall/%s/%s", fname, mode), rr.Results, f, want)
		}
	}
}
