package sqlite

import (
	"context"
	"database/sql"

	"github.com/axiom-studio/memora/pkg/types"
)

// hasVibeflowContextsTable returns true when the SQLite database also
// hosts a `vibeflow_contexts` table — i.e. Memora is running against
// an AxiomCloud-internal database that still has the rotation-archive
// data. In the OSS standalone case the table does not exist and the
// shim becomes a no-op.
func (s *Store) hasVibeflowContextsTable(ctx context.Context) bool {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='vibeflow_contexts' LIMIT 1`).Scan(&name)
	return err == nil && name == "vibeflow_contexts"
}

// sqlUUIDv4 is a SQLite expression yielding a fresh lowercase UUIDv4
// (version nibble 4, RFC 4122 variant 8–b), evaluated once per row.
const sqlUUIDv4 = `lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
    substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) ||
    substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))`

// ensureLegacyShimView creates `memora_edges_view` per PRD #297 §19.7
// when vibeflow_contexts is present. The view UNIONs synthetic
// `parent_of` edges (one per rotation-archive row with non-null
// parent_context_id) with the live `memora_edges` table. Synthetic
// edges carry the types.AgentLegacyVibeflowID sentinel, a persisted
// UUIDv4 edge_id from memora_synthetic_edges, and is_synthetic = 1 so
// GraphUnlink can refuse to delete them.
//
// In OSS-standalone installs (no vibeflow_contexts table) this is
// a no-op — the live memora_edges table is queried directly.
func (s *Store) ensureLegacyShimView(ctx context.Context) error {
	if !s.hasVibeflowContextsTable(ctx) {
		return nil
	}
	// Mint IDs for archive rows that have none yet. Archive rows written
	// after startup get theirs on the next start; until then the view
	// shows them with a NULL edge_id, which nothing can unlink anyway.
	_, _ = s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO memora_synthetic_edges (archive_id, edge_id)
SELECT CAST(id AS TEXT), `+sqlUUIDv4+`
FROM vibeflow_contexts
WHERE parent_context_id IS NOT NULL`)
	// Drop a prior view if present (idempotent on reboot).
	_, _ = s.db.ExecContext(ctx, `DROP VIEW IF EXISTS memora_edges_view`)
	_, err := s.db.ExecContext(ctx, `
CREATE VIEW memora_edges_view AS
SELECT
    syn.edge_id                                 AS edge_id,
    archive.project_id                          AS workspace_id,
    archive.id                                  AS source_memory_id,
    archive.parent_context_id                   AS target_memory_id,
    'parent_of'                                 AS edge_type,
    NULL                                        AS properties_json,
    '`+types.AgentLegacyVibeflowID+`'      AS created_by_agent_id,
    archive.created_at                          AS created_at,
    NULL                                        AS deleted_at,
    archive.created_at                          AS watermark,
    1                                           AS is_synthetic
FROM vibeflow_contexts archive
LEFT JOIN memora_synthetic_edges syn ON syn.archive_id = CAST(archive.id AS TEXT)
WHERE archive.parent_context_id IS NOT NULL
UNION ALL
SELECT edge_id, workspace_id, source_memory_id, target_memory_id, edge_type,
       properties_json, created_by_agent_id, created_at, deleted_at, watermark,
       0 AS is_synthetic
FROM memora_edges
WHERE deleted_at IS NULL`)
	if err != nil {
		return nil //nolint:nilerr // the legacy edges view is optional; Open() must not fail over it
	}
	return nil
}

// isSyntheticEdge reports whether edgeID names a synthetic shim edge.
// Without the shim view there are no synthetic edges, so a failed
// lookup means false.
func isSyntheticEdge(ctx context.Context, db *sql.DB, workspaceID, edgeID string) bool {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memora_edges_view WHERE edge_id = ? AND workspace_id = ? AND is_synthetic = 1`,
		edgeID, workspaceID).Scan(&n)
	return err == nil && n > 0
}

// Ensure unused-helper warnings don't fire when the shim is disabled.
var _ = sql.ErrNoRows
