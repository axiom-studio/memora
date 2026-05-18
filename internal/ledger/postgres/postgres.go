// Package postgres is the scale-up LedgerStore — same `multi`-style
// composition as the SQLite adapter but against Postgres. v0.1 ships
// sqlite + file ledgers; this is a deferred stub registered for the
// driver registry so `--ledger-driver=postgres` returns a clean
// capability error.
package postgres

import (
	"context"
	"fmt"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterLedger("postgres", func() adapter.LedgerStore { return &Store{} })
}

type Store struct{}

func (s *Store) Open(_ context.Context, _ adapter.LedgerConfig) error {
	return fmt.Errorf("%w: postgres LedgerStore is not yet wired in OSS v0.1 (tracked for v0.5; use sqlite or file for now)", types.ErrCapability)
}
func (s *Store) Close() error                                                  { return nil }
func (s *Store) Ping(_ context.Context) error                                  { return types.ErrCapability }
func (s *Store) Capabilities() adapter.LedgerCapabilities                      { return adapter.LedgerCapabilities{} }
func (s *Store) Append(context.Context, api.LedgerEntry) error                 { return types.ErrCapability }
func (s *Store) AppendBatch(context.Context, []api.LedgerEntry) error          { return types.ErrCapability }
func (s *Store) Query(context.Context, adapter.LedgerQuery) ([]api.LedgerEntry, string, error) {
	return nil, "", types.ErrCapability
}
func (s *Store) Redact(context.Context, string, []string) error { return types.ErrCapability }
