package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/adapter/compliance"
)

func TestCompliance_MetadataStore_SQLite(t *testing.T) {
	compliance.MetadataStoreSuite(t, func(t *testing.T) adapter.MetadataStore {
		t.Helper()
		dir := t.TempDir()
		dsn := filepath.Join(dir, "compliance.db")
		s := &Store{}
		if err := s.Open(context.Background(), adapter.MetadataConfig{Driver: "sqlite", DSN: dsn}); err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
