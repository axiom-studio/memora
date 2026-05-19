package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// CreateWorkspace inserts a new Workspace. ID is assigned if blank.
func (s *Store) CreateWorkspace(ctx context.Context, w *types.Workspace) error {
	if w.ID == "" {
		w.ID = types.NewID(types.WorkspaceIDPrefix)
	}
	if err := w.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if w.CreatedAt.IsZero() {
		w.CreatedAt = now
	}
	w.UpdatedAt = now
	w.AutoLinkDefaults()
	metaJSON := ""
	if w.Meta != nil {
		b, err := json.Marshal(w.Meta)
		if err != nil {
			return fmt.Errorf("marshal workspace meta: %w", err)
		}
		metaJSON = string(b)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_workspaces (id, name, region, chunker_id, embedding_model, meta_json,
    auto_link_enabled, auto_link_threshold, auto_link_max_edges, auto_link_max_incoming_per_day,
    created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Region, w.ChunkerID, w.EmbeddingModel, nullableStr(metaJSON),
		w.AutoLinkEnabled, w.AutoLinkThreshold, w.AutoLinkMaxEdges, w.AutoLinkMaxIncomingPerDay,
		w.CreatedAt, w.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	// Auto-register reserved sentinel agents in every new workspace.
	for _, agentID := range types.ReservedAgentIDs {
		_, _ = s.db.ExecContext(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, identity_provider, registered_at, active)
VALUES (?, ?, 'opaque', datetime('now'), 1)
ON CONFLICT(agent_id) DO NOTHING`, agentID, w.ID)
	}
	return nil
}

// GetWorkspace returns a single Workspace by id.
func (s *Store) GetWorkspace(ctx context.Context, id string) (*types.Workspace, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, region, chunker_id, embedding_model, meta_json,
    auto_link_enabled, auto_link_threshold, auto_link_max_edges, auto_link_max_incoming_per_day,
    created_at, updated_at
FROM memora_workspaces WHERE id = ?`, id)
	w := &types.Workspace{}
	var region, chunker, embed, metaJSON sql.NullString
	var createdAt, updatedAt sqliteTime
	if err := row.Scan(&w.ID, &w.Name, &region, &chunker, &embed, &metaJSON,
		&w.AutoLinkEnabled, &w.AutoLinkThreshold, &w.AutoLinkMaxEdges, &w.AutoLinkMaxIncomingPerDay,
		&createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}
	w.Region = region.String
	w.ChunkerID = chunker.String
	w.EmbeddingModel = embed.String
	w.CreatedAt = createdAt.Time
	w.UpdatedAt = updatedAt.Time
	if metaJSON.Valid && metaJSON.String != "" {
		_ = json.Unmarshal([]byte(metaJSON.String), &w.Meta)
	}
	return w, nil
}

// ListWorkspaces returns up to `limit` workspaces ordered by created_at.
func (s *Store) ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, region, chunker_id, embedding_model, meta_json,
    auto_link_enabled, auto_link_threshold, auto_link_max_edges, auto_link_max_incoming_per_day,
    created_at, updated_at
FROM memora_workspaces ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Workspace
	for rows.Next() {
		w, err := scanWorkspaceSqlite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListWorkspacesPaged returns up to `limit` workspaces using cursor-based
// keyset pagination on id. Returns (workspaces, nextCursor, error).
func (s *Store) ListWorkspacesPaged(ctx context.Context, cursor string, limit int) ([]types.Workspace, string, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT id, name, region, chunker_id, embedding_model, meta_json,
    auto_link_enabled, auto_link_threshold, auto_link_max_edges, auto_link_max_incoming_per_day,
    created_at, updated_at
FROM memora_workspaces`
	var args []any
	if cursor != "" {
		q += " WHERE id > ?"
		args = append(args, cursor)
	}
	q += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []types.Workspace
	for rows.Next() {
		w, err := scanWorkspaceSqlite(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].ID
	}
	return out, nextCursor, nil
}

// UpdateWorkspace replaces a Workspace's mutable fields.
func (s *Store) UpdateWorkspace(ctx context.Context, w *types.Workspace) error {
	if err := w.Validate(); err != nil {
		return err
	}
	w.AutoLinkDefaults()
	w.UpdatedAt = time.Now().UTC()
	metaJSON := ""
	if w.Meta != nil {
		b, err := json.Marshal(w.Meta)
		if err != nil {
			return err
		}
		metaJSON = string(b)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_workspaces SET name=?, region=?, chunker_id=?, embedding_model=?, meta_json=?,
    auto_link_enabled=?, auto_link_threshold=?, auto_link_max_edges=?, auto_link_max_incoming_per_day=?,
    updated_at=?
WHERE id=?`, w.Name, w.Region, w.ChunkerID, w.EmbeddingModel, nullableStr(metaJSON),
		w.AutoLinkEnabled, w.AutoLinkThreshold, w.AutoLinkMaxEdges, w.AutoLinkMaxIncomingPerDay,
		w.UpdatedAt, w.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	return nil
}

// DeleteWorkspace removes a Workspace only when no live memories remain.
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memora_memories WHERE workspace_id = ? AND deleted_at IS NULL", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: workspace has %d live memories; forget them first", types.ErrNotEmpty, n)
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM memora_workspaces WHERE id = ?", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return types.ErrNotFound
	}
	return nil
}

// CreateCollection inserts a Collection inside a Workspace.
func (s *Store) CreateCollection(ctx context.Context, c *types.Collection) error {
	if c.ID == "" {
		c.ID = types.NewID(types.CollectionIDPrefix)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_collections (id, workspace_id, name, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.WorkspaceID, c.Name, c.CreatedAt)
	return err
}

// GetCollection fetches a Collection by id.
func (s *Store) GetCollection(ctx context.Context, id string) (*types.Collection, error) {
	c := &types.Collection{}
	var createdAt sqliteTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, workspace_id, name, created_at FROM memora_collections WHERE id = ?", id).
		Scan(&c.ID, &c.WorkspaceID, &c.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	c.CreatedAt = createdAt.Time
	return c, err
}

// ListCollections returns the workspace's collections.
func (s *Store) ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, name, created_at FROM memora_collections WHERE workspace_id = ? ORDER BY name`,
		workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Collection
	for rows.Next() {
		var c types.Collection
		var createdAt sqliteTime
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &createdAt); err != nil {
			return nil, err
		}
		c.CreatedAt = createdAt.Time
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCollection removes a Collection only when no live memories reference it.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memora_memories WHERE collection_id = ? AND deleted_at IS NULL", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: collection has %d live memories; forget them first", types.ErrNotEmpty, n)
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM memora_collections WHERE id = ?", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return types.ErrNotFound
	}
	return nil
}

type sqlScanner interface {
	Scan(dest ...any) error
}

func scanWorkspaceSqlite(row sqlScanner) (types.Workspace, error) {
	var w types.Workspace
	var region, chunker, embed, metaJSON sql.NullString
	var createdAt, updatedAt sqliteTime
	if err := row.Scan(&w.ID, &w.Name, &region, &chunker, &embed, &metaJSON,
		&w.AutoLinkEnabled, &w.AutoLinkThreshold, &w.AutoLinkMaxEdges, &w.AutoLinkMaxIncomingPerDay,
		&createdAt, &updatedAt); err != nil {
		return w, err
	}
	w.Region = region.String
	w.ChunkerID = chunker.String
	w.EmbeddingModel = embed.String
	w.CreatedAt = createdAt.Time
	w.UpdatedAt = updatedAt.Time
	if metaJSON.Valid && metaJSON.String != "" {
		_ = json.Unmarshal([]byte(metaJSON.String), &w.Meta)
	}
	return w, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
