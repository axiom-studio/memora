// Package sqlite is the OSS-default Memora MetadataStore adapter
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
	adapter.RegisterMetadata("sqlite", func() adapter.MetadataStore { return &Store{} })
}

// Store implements adapter.MetadataStore against SQLite.
type Store struct {
	db     *sql.DB
	dsn    string
	wmkSeq uint64
	wmkMu  sync.Mutex
}

// Open implements adapter.MetadataStore.
func (s *Store) Open(ctx context.Context, cfg adapter.MetadataConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite: DSN required (e.g. ./data/memora.db)")
	}
	// Always append our safety pragmas — even when the caller supplied
	// a `?...` suffix in the DSN. Skipping them based on `strings.Contains`
	// would silently disable foreign-key enforcement and WAL mode, which
	// is a defense-in-depth gap (SECURITY-2101).
	dsn := cfg.DSN
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
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
	if err := s.ensureLegacyShimView(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: ensure legacy shim view: %w", err)
	}
	return nil
}

// Close implements adapter.MetadataStore.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping implements adapter.MetadataStore.
func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return errors.New("sqlite: not opened")
	}
	return s.db.PingContext(ctx)
}

// Capabilities implements adapter.MetadataStore.
func (s *Store) Capabilities() adapter.MetadataCapabilities {
	return adapter.MetadataCapabilities{
		SupportsCAS:          true,
		SupportsTransactions: true,
		SupportsBatchUpsert:  true,
		RecommendedMaxSizeGB: 50,
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

// ensureLegacyAgent seeds all reserved sentinel agent_ids into every
// existing workspace at startup. Idempotent via ON CONFLICT DO NOTHING.
func (s *Store) ensureLegacyAgent(ctx context.Context) error {
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
		for _, agentID := range types.ReservedAgentIDs {
			_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, identity_provider, registered_at, active)
VALUES (?, ?, 'opaque', datetime('now'), 1)
ON CONFLICT(agent_id) DO NOTHING`, agentID, wsID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
