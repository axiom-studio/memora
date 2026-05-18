// Package pgvector is the scale-up Memora VectorStore adapter using
// Postgres + the pgvector extension. v0.1 of OSS ships sqlite-vec as
// the default. This package is a registered-but-deferred stub —
// `--vector-driver=pgvector` returns a capability error pointing at
// the v0.5 milestone.
//
// Tracking: OSS roadmap v0.5 in PRD #297 §12.
package pgvector

import (
	"context"
	"fmt"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterVector("pgvector", func() adapter.VectorStore { return &Store{} })
}

// Store is the (deferred) pgvector VectorStore.
type Store struct{}

func (s *Store) Open(_ context.Context, _ adapter.VectorConfig) error {
	return fmt.Errorf("%w: pgvector VectorStore is not yet wired in OSS v0.1 (tracked for v0.5; use --vector-driver=sqlite-vec for now)", types.ErrCapability)
}
func (s *Store) Close() error                          { return nil }
func (s *Store) Ping(_ context.Context) error          { return types.ErrCapability }
func (s *Store) Capabilities() adapter.VectorCapabilities {
	return adapter.VectorCapabilities{}
}
func (s *Store) PutVector(context.Context, adapter.VectorPut) error              { return types.ErrCapability }
func (s *Store) PutVectorsBatch(context.Context, []adapter.VectorPut) error      { return types.ErrCapability }
func (s *Store) Query(context.Context, adapter.VectorQuery) ([]adapter.VectorHit, error) {
	return nil, types.ErrCapability
}
func (s *Store) DeleteVectors(context.Context, []adapter.VectorKey) error { return types.ErrCapability }
