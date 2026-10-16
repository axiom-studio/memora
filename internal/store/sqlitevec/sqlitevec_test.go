package sqlitevec

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{}
	if err := s.Open(context.Background(), adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(t.TempDir(), "vec.db"), Dim: 3}); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func put(t *testing.T, s *Store, ws, coll, mem, cell string, v ...float32) {
	t.Helper()
	if err := s.PutVector(context.Background(), adapter.VectorPut{
		Key:       adapter.VectorKey{WorkspaceID: ws, CollectionID: coll, MemoryID: mem, CellID: cell},
		Embedding: v,
	}); err != nil {
		t.Fatalf("PutVector(%s): %v", cell, err)
	}
}

func query(t *testing.T, s *Store, q adapter.VectorQuery) []adapter.VectorHit {
	t.Helper()
	hits, err := s.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	return hits
}

func TestStore_Capabilities(t *testing.T) {
	caps := (&Store{}).Capabilities()
	if !caps.SupportsExactSearch || caps.SupportsANN || !caps.SupportsHybridFilter || caps.MaxDimensions != 4096 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestStore_Open_RequiresDSN(t *testing.T) {
	if err := (&Store{}).Open(context.Background(), adapter.VectorConfig{}); err == nil {
		t.Fatal("Open with empty DSN succeeded; want an error")
	}
}

func TestStore_PutQueryDelete(t *testing.T) {
	s := openStore(t)
	put(t, s, "ws", "", "m1", "c1", 1, 0, 0)
	put(t, s, "ws", "", "m1", "c2", 0, 1, 0)
	put(t, s, "ws", "", "m2", "c3", 0, 0, 1)

	hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws", Embedding: []float32{1, 0, 0}, K: 2})
	if len(hits) != 2 || hits[0].Key.CellID != "c1" || math.Abs(hits[0].Score-1) > 1e-9 {
		t.Fatalf("hits = %+v, want 2 with c1 first at cosine 1", hits)
	}
	if hits[0].Key.MemoryID != "m1" || hits[0].Key.WorkspaceID != "ws" {
		t.Fatalf("hit key = %+v, want ws/m1 round-tripped", hits[0].Key)
	}

	if err := s.DeleteVectors(context.Background(), []adapter.VectorKey{{CellID: "c1"}, {CellID: "c2"}, {CellID: "c3"}}); err != nil {
		t.Fatalf("DeleteVectors: %v", err)
	}
	if hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws", Embedding: []float32{1, 0, 0}, K: 5}); len(hits) != 0 {
		t.Fatalf("hits after delete = %+v, want none", hits)
	}
	if err := s.DeleteVectors(context.Background(), nil); err != nil {
		t.Fatalf("DeleteVectors(nil): %v", err)
	}
}

func TestStore_Put_SameCellReplacesVector(t *testing.T) {
	s := openStore(t)
	put(t, s, "ws", "", "m1", "c1", 1, 0, 0)
	put(t, s, "ws", "", "m1", "c1", 0, 1, 0)

	hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws", Embedding: []float32{0, 1, 0}, K: 5})
	if len(hits) != 1 || math.Abs(hits[0].Score-1) > 1e-9 {
		t.Fatalf("hits = %+v, want one row holding the replacement vector", hits)
	}
}

func TestStore_Query_ScopesToWorkspaceAndCollection(t *testing.T) {
	s := openStore(t)
	put(t, s, "ws_a", "coll_1", "m1", "a1", 1, 0, 0)
	put(t, s, "ws_a", "coll_2", "m2", "a2", 1, 0, 0)
	put(t, s, "ws_b", "coll_1", "m3", "b1", 1, 0, 0)

	if hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws_a", Embedding: []float32{1, 0, 0}, K: 10}); len(hits) != 2 {
		t.Fatalf("ws_a hits = %+v, want 2 (ws_b must not leak in)", hits)
	}
	hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws_a", Embedding: []float32{1, 0, 0}, K: 10, Filter: adapter.VectorFilter{CollectionID: "coll_2"}})
	if len(hits) != 1 || hits[0].Key.CellID != "a2" || hits[0].Key.CollectionID != "coll_2" {
		t.Fatalf("collection-filtered hits = %+v, want only a2", hits)
	}
}

func TestStore_Query_DefaultKIsFive(t *testing.T) {
	s := openStore(t)
	for i, cell := range []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7"} {
		put(t, s, "ws", "", "m", cell, 1, float32(i), 0)
	}
	if hits := query(t, s, adapter.VectorQuery{WorkspaceID: "ws", Embedding: []float32{1, 0, 0}}); len(hits) != 5 {
		t.Fatalf("len(hits) = %d, want default k=5", len(hits))
	}
}

func TestStore_PutVectorsBatch_Empty(t *testing.T) {
	if err := openStore(t).PutVectorsBatch(context.Background(), nil); err != nil {
		t.Fatalf("PutVectorsBatch(nil): %v", err)
	}
}

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 2, 3}, []float32{1, 2, 3}, 1},
		{"scaled", []float32{1, 0}, []float32{5, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"empty", nil, []float32{1}, 0},
		{"mismatched length", []float32{1, 0}, []float32{1, 0, 0}, 0},
		{"zero vector", []float32{0, 0}, []float32{1, 0}, 0},
	}
	for _, c := range cases {
		if got := cosine(c.a, c.b); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: cosine = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEncodeDecodeFloats_RoundTrip(t *testing.T) {
	in := []float32{0, -1.5, 3.25, math.MaxFloat32, float32(math.SmallestNonzeroFloat32)}
	out, err := decodeFloats(encodeFloats(in))
	if err != nil {
		t.Fatalf("decodeFloats: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("index %d: %v != %v", i, out[i], in[i])
		}
	}
	if _, err := decodeFloats([]byte{1, 2, 3}); err == nil {
		t.Fatal("decodeFloats of a 3-byte blob succeeded; want a length error")
	}
}
