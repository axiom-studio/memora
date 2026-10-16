package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// ImprintMemory inserts a fresh Memory and returns its head watermark.
func (s *Store) ImprintMemory(ctx context.Context, m *types.Memory) (string, error) {
	if m.ID == "" {
		m.ID = types.NewID(types.MemoryIDPrefix)
	}
	if err := m.Validate(); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	m.ContentMD5 = types.MD5Hex(m.Content)
	if m.LastModifiedByAgentID == "" {
		m.LastModifiedByAgentID = m.WrittenByAgentID
	}
	wmk := types.NewWatermark()
	m.HeadWatermark = wmk
	m.CreatedWatermark = wmk

	tagsJSON, _ := json.Marshal(m.Tags)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_memories (id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
    written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.WorkspaceID, nullableStr(m.CollectionID), m.Content, m.ContentMD5,
		m.HeadWatermark, m.CreatedWatermark, m.WrittenByAgentID, m.LastModifiedByAgentID,
		nullableStr(string(tagsJSON)), boolToInt(m.RecallReady), m.CreatedAt, m.UpdatedAt); err != nil {
		return "", fmt.Errorf("insert memory: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memora_memories_fts(memory_id, workspace_id, content) VALUES (?, ?, ?)`,
		m.ID, m.WorkspaceID, m.Content); err != nil {
		return "", fmt.Errorf("insert fts: %w", err)
	}
	// Upsert tags into the indexed table for filter pushdown.
	for k, v := range m.Tags {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO memora_tags(memory_id, key, value) VALUES (?, ?, ?) ON CONFLICT(memory_id, key) DO UPDATE SET value=excluded.value",
			m.ID, k, v); err != nil {
			return "", err
		}
	}
	// Append watermark history.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_after)
VALUES (?, ?, 'imprint', ?, ?, ?)`,
		m.ID, m.HeadWatermark, m.WrittenByAgentID, m.CreatedAt, m.ContentMD5); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return m.HeadWatermark, nil
}

// GetMemory returns a Memory by id.
func (s *Store) GetMemory(ctx context.Context, id string) (*types.Memory, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE id = ? AND deleted_at IS NULL`, id)
	return scanMemory(row)
}

// GetMemoryAtWatermark returns the Memory body at a historical watermark.
func (s *Store) GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error) {
	// History only stores md5; for v0 we return current row if watermark
	// matches head, else error (full body-at-watermark requires snapshot).
	m, err := s.GetMemory(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.HeadWatermark == watermark {
		return m, nil
	}
	return nil, fmt.Errorf("%w: at-watermark read for historical wmk %s requires Snapshot (v0.5+)", types.ErrCapability, watermark)
}

// ListMemories returns up to `limit` memories, filtered by workspace
// and optionally collection.
func (s *Store) ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE workspace_id = ? AND deleted_at IS NULL`
	args := []any{workspaceID}
	if collectionID != "" {
		q += " AND collection_id = ?"
		args = append(args, collectionID)
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *Store) ListMemoriesPaged(ctx context.Context, workspaceID, collectionID, cursor string, limit int) ([]types.Memory, string, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE workspace_id = ? AND deleted_at IS NULL`
	args := []any{workspaceID}
	if collectionID != "" {
		q += " AND collection_id = ?"
		args = append(args, collectionID)
	}
	if cursor != "" {
		q += " AND id > ?"
		args = append(args, cursor)
	}
	q += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []types.Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *m)
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

// UpdateMemory replaces a Memory's content with CAS via expected watermark.
func (s *Store) UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (string, error) {
	newWmk := types.NewWatermark()
	contentMD5 := types.MD5Hex(m.Content)
	tagsJSON, _ := json.Marshal(m.Tags)
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// CAS update.
	res, err := tx.ExecContext(ctx, `
UPDATE memora_memories
SET content=?, content_md5=?, head_watermark=?, last_modified_by_agent_id=?,
    tags_json=?, updated_at=?
WHERE id=? AND head_watermark=? AND deleted_at IS NULL`,
		m.Content, contentMD5, newWmk, m.LastModifiedByAgentID, nullableStr(string(tagsJSON)), now,
		id, expectedWatermark)
	if err != nil {
		return "", fmt.Errorf("update memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "not found" from "CAS conflict".
		var headWmk string
		err := tx.QueryRowContext(ctx, "SELECT head_watermark FROM memora_memories WHERE id = ?", id).Scan(&headWmk)
		if errors.Is(err, sql.ErrNoRows) {
			return "", types.ErrNotFound
		}
		if err != nil {
			return "", err
		}
		return headWmk, types.ErrCAS
	}

	// Refresh FTS.
	if _, err := tx.ExecContext(ctx, `UPDATE memora_memories_fts SET content = ? WHERE memory_id = ?`,
		m.Content, id); err != nil {
		return "", err
	}
	// Replace tags.
	if _, err := tx.ExecContext(ctx, "DELETE FROM memora_tags WHERE memory_id = ?", id); err != nil {
		return "", err
	}
	for k, v := range m.Tags {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO memora_tags(memory_id, key, value) VALUES (?, ?, ?)", id, k, v); err != nil {
			return "", err
		}
	}
	// Append watermark history.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_after)
VALUES (?, ?, 'update', ?, ?, ?)`,
		id, newWmk, m.LastModifiedByAgentID, now, contentMD5); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newWmk, nil
}

// AppendMemory tacks content onto an existing Memory and bumps watermark.
// Returns the new watermark and content_md5.
func (s *Store) AppendMemory(ctx context.Context, id, expectedWatermark string, body string, agentID string) (string, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	var current, headWmk string
	err = tx.QueryRowContext(ctx, "SELECT content, head_watermark FROM memora_memories WHERE id = ? AND deleted_at IS NULL", id).
		Scan(&current, &headWmk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", types.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if expectedWatermark != "" && expectedWatermark != headWmk {
		return headWmk, "", types.ErrCAS
	}

	newContent := current + body
	newWmk := types.NewWatermark()
	newMD5 := types.MD5Hex(newContent)
	now := time.Now().UTC()

	if _, err := tx.ExecContext(ctx, `
UPDATE memora_memories SET content=?, content_md5=?, head_watermark=?, last_modified_by_agent_id=?, updated_at=?
WHERE id=?`, newContent, newMD5, newWmk, agentID, now, id); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memora_memories_fts SET content = ? WHERE memory_id = ?`,
		newContent, id); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES (?, ?, 'append', ?, ?, ?, ?)`,
		id, newWmk, agentID, now, types.MD5Hex(current), newMD5); err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return newWmk, newMD5, nil
}

// PatchMemory applies diff ops atomically. Returns the new watermark,
// per-cell deltas (computed by the caller via chunker), and the new
// content. The actual cell-diffing happens in the service layer because
// it depends on the chunker — this primitive only handles the content
// edit + CAS + watermark + history bookkeeping.
func (s *Store) PatchMemory(ctx context.Context, id, expectedWatermark string, ops []api.PatchOp, agentID string) (string, []types.CellDelta, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, "", err
	}
	defer tx.Rollback()

	var current, headWmk string
	err = tx.QueryRowContext(ctx, "SELECT content, head_watermark FROM memora_memories WHERE id = ? AND deleted_at IS NULL", id).
		Scan(&current, &headWmk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, "", types.ErrNotFound
	}
	if err != nil {
		return "", nil, "", err
	}
	if expectedWatermark != "" && expectedWatermark != headWmk {
		return headWmk, nil, "", types.ErrCAS
	}

	patched, err := ApplyPatchOps(current, ops)
	if err != nil {
		return "", nil, "", err
	}
	if patched == current {
		// No-op; still bump watermark? Per PRD: short-circuit if md5 unchanged.
		return headWmk, nil, patched, nil
	}

	newWmk := types.NewWatermark()
	newMD5 := types.MD5Hex(patched)
	oldMD5 := types.MD5Hex(current)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE memora_memories SET content=?, content_md5=?, head_watermark=?, last_modified_by_agent_id=?, updated_at=?
WHERE id=?`, patched, newMD5, newWmk, agentID, now, id); err != nil {
		return "", nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memora_memories_fts SET content = ? WHERE memory_id = ?`,
		patched, id); err != nil {
		return "", nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES (?, ?, 'patch', ?, ?, ?, ?)`,
		id, newWmk, agentID, now, oldMD5, newMD5); err != nil {
		return "", nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, "", err
	}
	// Cell deltas are returned empty here — the service layer recomputes
	// chunks against `patched` vs the existing cells and decides which
	// cells need re-embedding. F6.T2 wires that selective re-embed loop.
	return newWmk, nil, patched, nil
}

// ApplyPatchOps applies a sequence of PatchOps to content. Returns the
// new content or an error if any op anchor is missing/ambiguous.
// Anchors must be UNIQUE in the current state unless replace_all=true.
// Same semantics as vibeflow's AXIOMCLOUD-454 patch contract.
func ApplyPatchOps(content string, ops []api.PatchOp) (string, error) {
	out := content
	for i, op := range ops {
		if op.OldString == "" {
			return "", fmt.Errorf("%w: op[%d].old_string is empty", types.ErrPatchAnchor, i)
		}
		count := strings.Count(out, op.OldString)
		if count == 0 {
			return "", fmt.Errorf("%w: op[%d].old_string not found", types.ErrPatchAnchor, i)
		}
		if !op.ReplaceAll && count > 1 {
			return "", fmt.Errorf("%w: op[%d].old_string occurs %d times (need unique unless replace_all=true)", types.ErrPatchAnchor, i, count)
		}
		if op.ReplaceAll {
			out = strings.ReplaceAll(out, op.OldString, op.NewString)
		} else {
			out = strings.Replace(out, op.OldString, op.NewString, 1)
		}
	}
	return out, nil
}

// ForgetMemory soft-deletes a Memory (cascade to edges happens in
// GraphCascadeForget which the server orchestrates).
func (s *Store) ForgetMemory(ctx context.Context, workspaceID, id string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_memories SET deleted_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, now, id, workspaceID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM memora_memories_fts WHERE memory_id = ?`, id)
	return nil
}

func scanMemory(row *sql.Row) (*types.Memory, error) {
	m := &types.Memory{}
	var coll, tags, deletedAt sql.NullString
	var createdAt, updatedAt sqliteTime
	var recallReady int
	if err := row.Scan(&m.ID, &m.WorkspaceID, &coll, &m.Content, &m.ContentMD5, &m.HeadWatermark, &m.CreatedWatermark,
		&m.WrittenByAgentID, &m.LastModifiedByAgentID, &tags, &recallReady, &createdAt, &updatedAt, &deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, types.ErrNotFound
		}
		return nil, err
	}
	m.CollectionID = coll.String
	m.RecallReady = recallReady != 0
	m.CreatedAt = createdAt.Time
	m.UpdatedAt = updatedAt.Time
	m.DeletedAt = nullableTime(deletedAt)
	if tags.Valid && tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &m.Tags)
	}
	return m, nil
}

func scanMemoryFromRows(rows *sql.Rows) (*types.Memory, error) {
	m := &types.Memory{}
	var coll, tags, deletedAt sql.NullString
	var createdAt, updatedAt sqliteTime
	var recallReady int
	if err := rows.Scan(&m.ID, &m.WorkspaceID, &coll, &m.Content, &m.ContentMD5, &m.HeadWatermark, &m.CreatedWatermark,
		&m.WrittenByAgentID, &m.LastModifiedByAgentID, &tags, &recallReady, &createdAt, &updatedAt, &deletedAt); err != nil {
		return nil, err
	}
	m.CollectionID = coll.String
	m.RecallReady = recallReady != 0
	m.CreatedAt = createdAt.Time
	m.UpdatedAt = updatedAt.Time
	m.DeletedAt = nullableTime(deletedAt)
	if tags.Valid && tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &m.Tags)
	}
	return m, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
