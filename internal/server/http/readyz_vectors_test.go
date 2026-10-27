package http_test

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

// TestReadyz_VectorCounts (#6180): /readyz reports stale and missing
// vectors as integers (and, as before, as adapter strings); a memory
// whose vectors are gone counts as missing.
func TestReadyz_VectorCounts(t *testing.T) {
	n := newMesh(t, []string{"a"}, nil)[0]
	mem := n.Seed(t, "obsidian ridge survey")
	ready := func() map[string]any {
		t.Helper()
		status, body := n.Do(t, "GET", "/readyz", nil, nil)
		if status != 200 {
			t.Fatalf("readyz: %d %v", status, body)
		}
		return body
	}
	body := ready()
	if body["stale_vectors"] != float64(0) || body["missing_vectors"] != float64(0) {
		t.Fatalf("fresh node: %v, want stale_vectors 0 and missing_vectors 0 as numbers", body)
	}
	got, _ := n.Svc.Metadata.GetMemory(context.Background(), mem)
	if err := n.Svc.Vector.DeleteVectors(context.Background(), []adapter.VectorKey{{WorkspaceID: got.WorkspaceID, MemoryID: mem}}); err != nil {
		t.Fatal(err)
	}
	body = ready()
	cells, _ := n.Svc.Metadata.GetCells(context.Background(), got.WorkspaceID, mem)
	if body["missing_vectors"] != float64(len(cells)) || body["adapter"].(map[string]any)["missing_vectors"] == nil {
		t.Fatalf("after losing %d vectors: %v", len(cells), body)
	}
}
