// Package sqlite is the OSS-default Memora PrimaryStore adapter
// backed by modernc.org/sqlite (CGo-free).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"

	// Pure-Go SQLite driver.
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

func init() {
	adapter.RegisterPrimary("sqlite", func() adapter.PrimaryStore { return &Store{} })
}

// Store implements adapter.PrimaryStore against SQLite.
type Store struct {
	db     *sql.DB
	dsn    string
	wmkSeq uint64
	wmkMu  sync.Mutex
}

// Open implements adapter.PrimaryStore.
func (s *Store) Open(ctx context.Context, cfg adapter.PrimaryConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite: DSN required (e.g. ./data/memora.db)")
	}
	// Append pragmas that have to live on the URI for modernc.org/sqlite.
	dsn := cfg.DSN
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sqlite: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: ping: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite serializes writers; one conn keeps WAL behavior predictable.
	s.db = db
	s.dsn = cfg.DSN
	if err := s.runMigrations(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	if err := s.ensureLegacyAgent(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: ensure legacy agent: %w", err)
	}
	return nil
}

// Close implements adapter.PrimaryStore.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping implements adapter.PrimaryStore.
func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return errors.New("sqlite: not opened")
	}
	return s.db.PingContext(ctx)
}

// Capabilities implements adapter.PrimaryStore.
func (s *Store) Capabilities() adapter.PrimaryCapabilities {
	return adapter.PrimaryCapabilities{
		SupportsCAS:              true,
		SupportsTransactions:     true,
		SupportsBatchUpsert:      true,
		RecommendedMaxSizeGB:     50,
		MaxGraphDepth:            3,
		MaxNeighborsK:            200,
		MaxLinkBatchSize:         1000,
	}
}

// runMigrations walks the embedded `migrations/` directory (or, as
// fallback, the package-relative copy under `migrate/sqlite/`) and
// applies any not-yet-recorded files in lexical order.
func (s *Store) runMigrations(ctx context.Context) error {
	entries, err := loadMigrations()
	if err != nil {
		return err
	}

	// Make sure the bookkeeping table exists FIRST so the recorded set
	// can be queried.
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS memora_schema_migrations (
            version TEXT PRIMARY KEY,
            applied_at TEXT NOT NULL DEFAULT (datetime('now'))
        )`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM memora_schema_migrations")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()

	for _, m := range entries {
		if applied[m.version] {
			continue
		}
		// Apply each migration in a transaction.
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO memora_schema_migrations(version) VALUES (?)", m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type migration struct {
	version string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := embeddedMigrations.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	files := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := embeddedMigrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		files = append(files, migration{version: e.Name(), sql: string(b)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

// ensureLegacyAgent is part of the parent_context_id migration shim
// (F11). The reserved agent_id is auto-registered so every workspace
// that exists at startup carries the sentinel.
func (s *Store) ensureLegacyAgent(ctx context.Context) error {
	// We can't register against a workspace that doesn't exist yet.
	// For new installs this is a no-op; the first workspace creation
	// re-runs this idempotent.
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM memora_workspaces")
	if err != nil {
		return err
	}
	var wsIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		wsIDs = append(wsIDs, id)
	}
	_ = rows.Close()
	for _, wsID := range wsIDs {
		_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, identity_provider, registered_at, active)
VALUES (?, ?, 'opaque', datetime('now'), 1)
ON CONFLICT(agent_id) DO NOTHING`, types.AgentLegacyVibeflowID, wsID)
		if err != nil {
			return err
		}
	}
	return nil
}
