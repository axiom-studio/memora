package service

import (
	"context"
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestSearch_FindsMemoriesInEveryWorkspace: one node-wide query returns
// matches from two workspaces, each hit tagged with its workspace, in
// every mode.
func TestSearch_FindsMemoriesInEveryWorkspace(t *testing.T) {
	s := newTestService(t)
	wsA, wsB := newTestWorkspace(t, s), newTestWorkspace(t, s)
	inA := imprint(t, s, wsA, "orbital mechanics notes")
	inB := imprint(t, s, wsB, "orbital mechanics notes")
	imprint(t, s, wsB, "grocery list")

	for _, mode := range []api.RecallMode{api.RecallModeKeyword, api.RecallModeVector, api.RecallModeHybrid} {
		r, err := s.Search(context.Background(), api.SearchRequest{Query: "orbital mechanics notes", Mode: mode, K: 2})
		if err != nil {
			t.Fatalf("%s: Search: %v", mode, err)
		}
		got := recallIDs(r)
		if got[inA].WorkspaceID != wsA || got[inB].WorkspaceID != wsB {
			t.Fatalf("%s: results = %+v, want %s in %s and %s in %s", mode, r.Results, inA, wsA, inB, wsB)
		}
	}
}

func TestSearch_WorkspaceIDsRestrictsScope(t *testing.T) {
	s := newTestService(t)
	wsA, wsB := newTestWorkspace(t, s), newTestWorkspace(t, s)
	inA := imprint(t, s, wsA, "shared needle")
	imprint(t, s, wsB, "shared needle")

	for _, mode := range []api.RecallMode{api.RecallModeKeyword, api.RecallModeVector} {
		r, err := s.Search(context.Background(), api.SearchRequest{Query: "shared needle", Mode: mode, WorkspaceIDs: []string{wsA}})
		if err != nil {
			t.Fatalf("%s: Search: %v", mode, err)
		}
		if len(r.Results) != 1 || r.Results[0].MemoryID != inA {
			t.Fatalf("%s: results = %+v, want only %s from %s", mode, r.Results, inA, wsA)
		}
	}
}

func TestSearch_HonoursK(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	for i := 0; i < 4; i++ {
		imprint(t, s, ws, "repeated phrase")
	}
	r, err := s.Search(context.Background(), api.SearchRequest{Query: "repeated phrase", K: 3})
	if err != nil || len(r.Results) != 3 {
		t.Fatalf("Search k=3 = %d results, %v; want 3", len(r.Results), err)
	}
}

func TestSearch_RejectsBadRequests(t *testing.T) {
	s := newTestService(t)
	for name, req := range map[string]api.SearchRequest{
		"empty query":      {Query: "  "},
		"lookup mode":      {Query: "x", Mode: api.RecallModeLookup},
		"legacy workspace": {Query: "x", WorkspaceIDs: []string{"ws_01HXYZABCDEFGHJKMNPQRSTVWX"}},
		"unknown mode":     {Query: "x", Mode: "fuzzy"},
	} {
		if _, err := s.Search(context.Background(), req); !errors.Is(err, types.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}
