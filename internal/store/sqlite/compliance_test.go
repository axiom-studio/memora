package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/adapter/compliance"
)

// TestCompliance_PrimaryStore_SQLite runs the public compliance suite
// against the SQLite adapter. Adapter authors can copy this file as a
// template for their own implementations.
func TestCompliance_PrimaryStore_SQLite(t *testing.T) {
	compliance.PrimaryStoreSuite(t, func(t *testing.T) adapter.PrimaryStore {
		t.Helper()
		dir := t.TempDir()
		dsn := filepath.Join(dir, "compliance.db")
		s := &Store{}
		if err := s.Open(context.Background(), adapter.PrimaryConfig{Driver: "sqlite", DSN: dsn}); err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
