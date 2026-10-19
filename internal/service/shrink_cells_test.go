package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/store/file"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// #6037: an edit that re-chunks to fewer cells must not leave the removed
// cells' text, content blobs or vectors behind, and changed cells must not
// orphan their old vector.

func withFileContent(t *testing.T, s *Service) {
	t.Helper()
	cs := &file.ContentStore{}
	if err := cs.Open(context.Background(), adapter.ContentConfig{Driver: "file", DSN: t.TempDir()}); err != nil {
		t.Fatalf("open content: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	s.Content = cs
}

// vectorCount returns how many vectors the workspace holds.
func vectorCount(t *testing.T, s *Service, ws string) int {
	t.Helper()
	q, err := s.Embedder.Embed(context.Background(), []string{"probe"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Vector.Query(context.Background(), adapter.VectorQuery{WorkspaceID: ws, Embedding: q[0], K: 1000})
	if err != nil {
		t.Fatalf("vector query: %v", err)
	}
	return len(hits)
}

func assertCellsClean(t *testing.T, s *Service, ws, memID string, before []types.Cell, want int, removedText ...string) {
	t.Helper()
	ctx := context.Background()
	cells, err := s.Metadata.GetCells(ctx, ws, memID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != want {
		t.Fatalf("GetCells = %d cells, want %d", len(cells), want)
	}
	for _, c := range cells {
		for _, gone := range removedText {
			if strings.Contains(c.Text, gone) {
				t.Errorf("stored cell %s still holds removed text %q", c.CellID, gone)
			}
		}
		if c.VectorKey != c.CellID {
			t.Errorf("cell %s vector_key = %q, want its own cell_id", c.CellID, c.VectorKey)
		}
	}
	if n := vectorCount(t, s, ws); n != want {
		t.Errorf("workspace holds %d vectors, want %d (one per cell, no orphans)", n, want)
	}
	live := map[string]bool{}
	for _, c := range cells {
		live[c.CellID] = true
	}
	for _, c := range before {
		if live[c.CellID] {
			continue
		}
		if _, err := s.Content.GetCellContent(ctx, ws, memID, c.CellID); !errors.Is(err, types.ErrNotFound) {
			t.Errorf("content of removed cell %s: err = %v, want ErrNotFound", c.CellID, err)
		}
	}
}

func TestShrinkingEdits_RemoveCellsVectorsAndContent(t *testing.T) {
	for _, mode := range []string{"sync", "async"} {
		t.Run(mode, func(t *testing.T) {
			var s *Service
			if mode == "async" {
				s = newAsyncTestService(t)
			} else {
				s = newTestService(t)
			}
			withFileContent(t, s)
			ws := newMarkdownWorkspace(t, s)
			ctx := context.Background()
			settle := func(memID string) {
				if mode == "async" {
					waitEmbedded(t, s, ws, memID)
				}
			}

			imp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: asyncDoc})
			if err != nil {
				t.Fatal(err)
			}
			settle(imp.ID)
			before, _ := s.Metadata.GetCells(ctx, ws, imp.ID)

			// Update: 3 cells -> 1, and the surviving slot's text changes.
			up, err := s.Update(ctx, ws, imp.ID, testAgent, imp.Watermark, api.UpdateRequest{Content: "# only\nonly body"})
			if err != nil {
				t.Fatal(err)
			}
			settle(imp.ID)
			assertCellsClean(t, s, ws, imp.ID, before, 1, "beta body", "gamma body", "alpha body")

			// Grow back to 3, then Patch a heading out: 3 cells -> 2.
			up2, err := s.Update(ctx, ws, imp.ID, testAgent, up.Watermark, api.UpdateRequest{Content: asyncDoc})
			if err != nil {
				t.Fatal(err)
			}
			settle(imp.ID)
			before, _ = s.Metadata.GetCells(ctx, ws, imp.ID)
			if _, err := s.Patch(ctx, ws, imp.ID, testAgent, up2.Watermark, api.PatchRequest{
				Patch: []api.PatchOp{{OldString: "\n# gamma\ngamma body", NewString: ""}},
			}); err != nil {
				t.Fatal(err)
			}
			settle(imp.ID)
			assertCellsClean(t, s, ws, imp.ID, before, 2, "gamma body")

			env, err := s.Metadata.GetMemory(ctx, imp.ID)
			if err != nil || strings.Contains(env.Content, "gamma") {
				t.Errorf("memory content after patch: %q (err %v)", env.Content, err)
			}
		})
	}
}
