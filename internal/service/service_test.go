package service

import (
	"context"
	"path/filepath"
	"testing"

	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/internal/store/sqlitevec"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/embedding"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

const testAgent = "agent_opaque_service_test"

// newTestService wires the OSS-default sqlite metadata + sqlite-vec vector
// stores and the noop embedder; no test doubles.
func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	meta := &storesqlite.Store{}
	if err := meta.Open(ctx, adapter.MetadataConfig{Driver: "sqlite", DSN: filepath.Join(dir, "memora.db")}); err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	vec := &sqlitevec.Store{}
	if err := vec.Open(ctx, adapter.VectorConfig{Driver: "sqlite-vec", DSN: filepath.Join(dir, "memora.db"), Dim: 384}); err != nil {
		t.Fatalf("open vector: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })

	emb, err := embedding.Open("noop:default")
	if err != nil {
		t.Fatalf("open embedding: %v", err)
	}
	return &Service{Metadata: meta, Vector: vec, Embedder: emb}
}

func newTestWorkspace(t *testing.T, s *Service) string {
	t.Helper()
	ws := &types.Workspace{Name: "service-test"}
	if err := s.Metadata.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return ws.ID
}

func storedRecallReady(t *testing.T, s *Service, memoryID string) bool {
	t.Helper()
	m, err := s.Metadata.GetMemory(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("GetMemory(%s): %v", memoryID, err)
	}
	return m.RecallReady
}

// Issue #4379: the response said recall_ready=true but the stored row
// stayed false forever, because only the (never-started) async pool
// flipped it.
func TestImprint_PersistsRecallReady(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)

	resp, err := s.Imprint(context.Background(), ws, testAgent, api.ImprintRequest{Content: "first paragraph\n\nsecond paragraph"})
	if err != nil {
		t.Fatalf("Imprint: %v", err)
	}
	if !resp.RecallReady {
		t.Fatal("ImprintResponse.RecallReady = false, want true after a successful inline embed")
	}
	if !storedRecallReady(t, s, resp.MemoryID) {
		t.Fatal("stored recall_ready = false after imprint; want true (response and row disagree)")
	}
}

func TestUpdate_LeavesRecallReadyTrue(t *testing.T) {
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	ctx := context.Background()

	imp, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: "original"})
	if err != nil {
		t.Fatalf("Imprint: %v", err)
	}
	if _, err := s.Update(ctx, ws, imp.MemoryID, testAgent, imp.Watermark, api.UpdateRequest{Content: "rewritten\n\nwith a second cell", ExpectedWatermark: imp.Watermark}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !storedRecallReady(t, s, imp.MemoryID) {
		t.Fatal("stored recall_ready = false after update re-embedded every cell")
	}
}
