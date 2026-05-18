package sqlite

import (
	"context"
	"database/sql"
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

// ensureLegacyShimView creates `memora_edges_view` per PRD #297 §19.7
// when vibeflow_contexts is present. The view UNIONs synthetic
// `parent_of` edges (one per rotation-archive row with non-null
// parent_context_id) with the live `memora_edges` table. Synthetic
// edges carry the `agent_legacy_vibeflow` sentinel and the prefix
// `edg_synthetic_` so GraphUnlink can refuse to delete them.
//
// In OSS-standalone installs (no vibeflow_contexts table) this is
// a no-op — the live memora_edges table is queried directly.
func (s *Store) ensureLegacyShimView(ctx context.Context) error {
	if !s.hasVibeflowContextsTable(ctx) {
		return nil
	}
	// Drop a prior view if present (idempotent on reboot).
	_, _ = s.db.ExecContext(ctx, `DROP VIEW IF EXISTS memora_edges_view`)
	_, err := s.db.ExecContext(ctx, `
CREATE VIEW memora_edges_view AS
SELECT
    'edg_synthetic_' || archive.id              AS edge_id,
    archive.project_id                          AS workspace_id,
    archive.id                                  AS source_memory_id,
    archive.parent_context_id                   AS target_memory_id,
    'parent_of'                                 AS edge_type,
    NULL                                        AS properties_json,
    'agent_legacy_vibeflow'                     AS created_by_agent_id,
    archive.created_at                          AS created_at,
    NULL                                        AS deleted_at,
    archive.created_at                          AS watermark
FROM vibeflow_contexts archive
WHERE archive.parent_context_id IS NOT NULL
UNION ALL
SELECT edge_id, workspace_id, source_memory_id, target_memory_id, edge_type,
       properties_json, created_by_agent_id, created_at, deleted_at, watermark
FROM memora_edges
WHERE deleted_at IS NULL`)
	if err != nil {
		// Don't fail Open() over the shim — the deployer may not need it.
		return nil //nolint:nilerr
	}
	return nil
}

// Ensure unused-helper warnings don't fire when the shim is disabled.
var _ = sql.ErrNoRows
