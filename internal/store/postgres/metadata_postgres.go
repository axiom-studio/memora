package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterMetadata("postgres", func() adapter.MetadataStore { return &MetadataStore{} })
}

type MetadataStore struct {
	pool   *pgxpool.Pool
	wmkSeq uint64
	wmkMu  sync.Mutex
}

func (s *MetadataStore) Open(ctx context.Context, cfg adapter.MetadataConfig) error {
	if cfg.DSN == "" {
		return errors.New("postgres: DSN required (e.g. postgres://user:pass@host/dbname?sslmode=verify-full)")
	}
	pool, err := openPool(ctx, cfg.DSN)
	if err != nil {
		return err
	}
	s.pool = pool
	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}

func (s *MetadataStore) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func (s *MetadataStore) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("postgres: not opened")
	}
	return s.pool.Ping(ctx)
}

func (s *MetadataStore) Capabilities() adapter.MetadataCapabilities {
	return adapter.MetadataCapabilities{
		SupportsCAS:               true,
		SupportsTransactions:      true,
		SupportsBatchUpsert:       true,
		SupportsLogicalReplication: true,
		RecommendedMaxSizeGB:      10000,
	}
}

// --- Workspace CRUD ---

func (s *MetadataStore) CreateWorkspace(ctx context.Context, w *types.Workspace) error {
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
	var metaJSON []byte
	if w.Meta != nil {
		metaJSON, _ = json.Marshal(w.Meta)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO memora_workspaces (id, name, region, chunker_id, embedding_model, meta_json, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		w.ID, w.Name, nullableStr(w.Region), nullableStr(w.ChunkerID),
		nullableStr(w.EmbeddingModel), nullableJSON(metaJSON), w.CreatedAt, w.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, identity_provider, registered_at, active)
VALUES ($1, $2, 'opaque', now(), TRUE)
ON CONFLICT(agent_id) DO NOTHING`, types.AgentLegacyVibeflowID, w.ID)
	return nil
}

func (s *MetadataStore) GetWorkspace(ctx context.Context, id string) (*types.Workspace, error) {
	w := &types.Workspace{}
	var region, chunker, embed *string
	var metaJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT id, name, region, chunker_id, embedding_model, meta_json, created_at, updated_at
FROM memora_workspaces WHERE id = $1`, id).Scan(
		&w.ID, &w.Name, &region, &chunker, &embed, &metaJSON, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if region != nil {
		w.Region = *region
	}
	if chunker != nil {
		w.ChunkerID = *chunker
	}
	if embed != nil {
		w.EmbeddingModel = *embed
	}
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &w.Meta)
	}
	return w, nil
}

func (s *MetadataStore) ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, name, region, chunker_id, embedding_model, meta_json, created_at, updated_at
FROM memora_workspaces ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Workspace
	for rows.Next() {
		var w types.Workspace
		var region, chunker, embed *string
		var metaJSON []byte
		if err := rows.Scan(&w.ID, &w.Name, &region, &chunker, &embed, &metaJSON, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		if region != nil {
			w.Region = *region
		}
		if chunker != nil {
			w.ChunkerID = *chunker
		}
		if embed != nil {
			w.EmbeddingModel = *embed
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &w.Meta)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *MetadataStore) UpdateWorkspace(ctx context.Context, w *types.Workspace) error {
	if err := w.Validate(); err != nil {
		return err
	}
	w.UpdatedAt = time.Now().UTC()
	var metaJSON []byte
	if w.Meta != nil {
		metaJSON, _ = json.Marshal(w.Meta)
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE memora_workspaces SET name=$1, region=$2, chunker_id=$3, embedding_model=$4, meta_json=$5, updated_at=$6
WHERE id=$7`, w.Name, nullableStr(w.Region), nullableStr(w.ChunkerID),
		nullableStr(w.EmbeddingModel), nullableJSON(metaJSON), w.UpdatedAt, w.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

func (s *MetadataStore) DeleteWorkspace(ctx context.Context, id string) error {
	var n int
	if err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM memora_memories WHERE workspace_id = $1 AND deleted_at IS NULL", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: workspace has %d live memories; forget them first", types.ErrNotEmpty, n)
	}
	tag, err := s.pool.Exec(ctx, "DELETE FROM memora_workspaces WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

// --- Collection CRUD ---

func (s *MetadataStore) CreateCollection(ctx context.Context, c *types.Collection) error {
	if c.ID == "" {
		c.ID = types.NewID(types.CollectionIDPrefix)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO memora_collections (id, workspace_id, name, created_at) VALUES ($1, $2, $3, $4)`,
		c.ID, c.WorkspaceID, c.Name, c.CreatedAt)
	return err
}

func (s *MetadataStore) GetCollection(ctx context.Context, id string) (*types.Collection, error) {
	c := &types.Collection{}
	err := s.pool.QueryRow(ctx,
		"SELECT id, workspace_id, name, created_at FROM memora_collections WHERE id = $1", id).
		Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	return c, err
}

func (s *MetadataStore) ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, name, created_at FROM memora_collections WHERE workspace_id = $1 ORDER BY name`,
		workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Collection
	for rows.Next() {
		var c types.Collection
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *MetadataStore) DeleteCollection(ctx context.Context, id string) error {
	var n int
	if err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM memora_memories WHERE collection_id = $1 AND deleted_at IS NULL", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: collection has %d live memories; forget them first", types.ErrNotEmpty, n)
	}
	tag, err := s.pool.Exec(ctx, "DELETE FROM memora_collections WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

// --- Memory CRUD ---

func (s *MetadataStore) ImprintMemory(ctx context.Context, m *types.Memory) (string, error) {
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
INSERT INTO memora_memories (id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
    written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		m.ID, m.WorkspaceID, nullableStr(m.CollectionID), m.Content, m.ContentMD5,
		m.HeadWatermark, m.CreatedWatermark, m.WrittenByAgentID, m.LastModifiedByAgentID,
		nullableJSON(tagsJSON), m.RecallReady, m.CreatedAt, m.UpdatedAt); err != nil {
		return "", fmt.Errorf("insert memory: %w", err)
	}

	for k, v := range m.Tags {
		if _, err := tx.Exec(ctx,
			"INSERT INTO memora_tags(memory_id, key, value) VALUES ($1, $2, $3) ON CONFLICT(memory_id, key) DO UPDATE SET value=excluded.value",
			m.ID, k, v); err != nil {
			return "", err
		}
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_after)
VALUES ($1, $2, 'imprint', $3, $4, $5)`,
		m.ID, m.HeadWatermark, m.WrittenByAgentID, m.CreatedAt, m.ContentMD5); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return m.HeadWatermark, nil
}

func (s *MetadataStore) GetMemory(ctx context.Context, id string) (*types.Memory, error) {
	m := &types.Memory{}
	var coll *string
	var tagsJSON []byte
	var deletedAt *time.Time
	err := s.pool.QueryRow(ctx, `
SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE id = $1`, id).Scan(
		&m.ID, &m.WorkspaceID, &coll, &m.Content, &m.ContentMD5, &m.HeadWatermark, &m.CreatedWatermark,
		&m.WrittenByAgentID, &m.LastModifiedByAgentID, &tagsJSON, &m.RecallReady,
		&m.CreatedAt, &m.UpdatedAt, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if coll != nil {
		m.CollectionID = *coll
	}
	m.DeletedAt = deletedAt
	if len(tagsJSON) > 0 {
		_ = json.Unmarshal(tagsJSON, &m.Tags)
	}
	return m, nil
}

func (s *MetadataStore) GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error) {
	m, err := s.GetMemory(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.HeadWatermark == watermark {
		return m, nil
	}
	return nil, fmt.Errorf("%w: at-watermark read for historical wmk %s requires Snapshot (v0.5+)", types.ErrCapability, watermark)
}

func (s *MetadataStore) ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE workspace_id = $1 AND deleted_at IS NULL`
	args := []any{workspaceID}
	argN := 2
	if collectionID != "" {
		q += fmt.Sprintf(" AND collection_id = $%d", argN)
		args = append(args, collectionID)
		argN++
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argN)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Memory
	for rows.Next() {
		var m types.Memory
		var coll *string
		var tagsJSON []byte
		var deletedAt *time.Time
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &coll, &m.Content, &m.ContentMD5,
			&m.HeadWatermark, &m.CreatedWatermark, &m.WrittenByAgentID, &m.LastModifiedByAgentID,
			&tagsJSON, &m.RecallReady, &m.CreatedAt, &m.UpdatedAt, &deletedAt); err != nil {
			return nil, err
		}
		if coll != nil {
			m.CollectionID = *coll
		}
		m.DeletedAt = deletedAt
		if len(tagsJSON) > 0 {
			_ = json.Unmarshal(tagsJSON, &m.Tags)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MetadataStore) ListMemoriesPaged(ctx context.Context, workspaceID, collectionID, cursor string, limit int) ([]types.Memory, string, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT id, workspace_id, collection_id, content, content_md5, head_watermark, created_watermark,
       written_by_agent_id, last_modified_by_agent_id, tags_json, recall_ready, created_at, updated_at, deleted_at
FROM memora_memories WHERE workspace_id = $1 AND deleted_at IS NULL`
	args := []any{workspaceID}
	argN := 2
	if collectionID != "" {
		q += fmt.Sprintf(" AND collection_id = $%d", argN)
		args = append(args, collectionID)
		argN++
	}
	if cursor != "" {
		q += fmt.Sprintf(" AND id > $%d", argN)
		args = append(args, cursor)
		argN++
	}
	q += fmt.Sprintf(" ORDER BY id ASC LIMIT $%d", argN)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []types.Memory
	for rows.Next() {
		var m types.Memory
		var coll *string
		var tagsJSON []byte
		var deletedAt *time.Time
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &coll, &m.Content, &m.ContentMD5,
			&m.HeadWatermark, &m.CreatedWatermark, &m.WrittenByAgentID, &m.LastModifiedByAgentID,
			&tagsJSON, &m.RecallReady, &m.CreatedAt, &m.UpdatedAt, &deletedAt); err != nil {
			return nil, "", err
		}
		if coll != nil {
			m.CollectionID = *coll
		}
		m.DeletedAt = deletedAt
		if len(tagsJSON) > 0 {
			_ = json.Unmarshal(tagsJSON, &m.Tags)
		}
		out = append(out, m)
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

func (s *MetadataStore) UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (string, error) {
	newWmk := types.NewWatermark()
	contentMD5 := types.MD5Hex(m.Content)
	tagsJSON, _ := json.Marshal(m.Tags)
	now := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
UPDATE memora_memories
SET content=$1, content_md5=$2, head_watermark=$3, last_modified_by_agent_id=$4,
    tags_json=$5, updated_at=$6
WHERE id=$7 AND head_watermark=$8 AND deleted_at IS NULL`,
		m.Content, contentMD5, newWmk, m.LastModifiedByAgentID,
		nullableJSON(tagsJSON), now, id, expectedWatermark)
	if err != nil {
		return "", fmt.Errorf("update memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var headWmk string
		err := tx.QueryRow(ctx, "SELECT head_watermark FROM memora_memories WHERE id = $1", id).Scan(&headWmk)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", types.ErrNotFound
		}
		if err != nil {
			return "", err
		}
		return headWmk, types.ErrCAS
	}

	if _, err := tx.Exec(ctx, "DELETE FROM memora_tags WHERE memory_id = $1", id); err != nil {
		return "", err
	}
	for k, v := range m.Tags {
		if _, err := tx.Exec(ctx,
			"INSERT INTO memora_tags(memory_id, key, value) VALUES ($1, $2, $3)", id, k, v); err != nil {
			return "", err
		}
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_after)
VALUES ($1, $2, 'update', $3, $4, $5)`,
		id, newWmk, m.LastModifiedByAgentID, now, contentMD5); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return newWmk, nil
}

func (s *MetadataStore) AppendMemory(ctx context.Context, id, expectedWatermark string, body string, agentID string) (string, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	var current, headWmk string
	err = tx.QueryRow(ctx,
		"SELECT content, head_watermark FROM memora_memories WHERE id = $1 AND deleted_at IS NULL", id).
		Scan(&current, &headWmk)
	if errors.Is(err, pgx.ErrNoRows) {
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

	if _, err := tx.Exec(ctx, `
UPDATE memora_memories SET content=$1, content_md5=$2, head_watermark=$3, last_modified_by_agent_id=$4, updated_at=$5
WHERE id=$6`, newContent, newMD5, newWmk, agentID, now, id); err != nil {
		return "", "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES ($1, $2, 'append', $3, $4, $5, $6)`,
		id, newWmk, agentID, now, types.MD5Hex(current), newMD5); err != nil {
		return "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return newWmk, newMD5, nil
}

func (s *MetadataStore) PatchMemory(ctx context.Context, id, expectedWatermark string, ops []api.PatchOp, agentID string) (string, []types.CellDelta, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, "", err
	}
	defer tx.Rollback(ctx)

	var current, headWmk string
	err = tx.QueryRow(ctx,
		"SELECT content, head_watermark FROM memora_memories WHERE id = $1 AND deleted_at IS NULL", id).
		Scan(&current, &headWmk)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", types.ErrNotFound
	}
	if err != nil {
		return "", nil, "", err
	}
	if expectedWatermark != "" && expectedWatermark != headWmk {
		return headWmk, nil, "", types.ErrCAS
	}

	patched, err := applyPatchOps(current, ops)
	if err != nil {
		return "", nil, "", err
	}
	if patched == current {
		return headWmk, nil, patched, nil
	}

	newWmk := types.NewWatermark()
	newMD5 := types.MD5Hex(patched)
	oldMD5 := types.MD5Hex(current)
	now := time.Now().UTC()

	if _, err := tx.Exec(ctx, `
UPDATE memora_memories SET content=$1, content_md5=$2, head_watermark=$3, last_modified_by_agent_id=$4, updated_at=$5
WHERE id=$6`, patched, newMD5, newWmk, agentID, now, id); err != nil {
		return "", nil, "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES ($1, $2, 'patch', $3, $4, $5, $6)`,
		id, newWmk, agentID, now, oldMD5, newMD5); err != nil {
		return "", nil, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", nil, "", err
	}
	return newWmk, nil, patched, nil
}

func applyPatchOps(content string, ops []api.PatchOp) (string, error) {
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

func (s *MetadataStore) ForgetMemory(ctx context.Context, id string) error {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx, `
UPDATE memora_memories SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`, now, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

// --- Cells ---

func (s *MetadataStore) UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i := range cells {
		c := &cells[i]
		if c.CellID == "" {
			c.CellID = types.NewID(types.CellIDPrefix)
		}
		c.MemoryID = memoryID
		if c.CreatedAt.IsZero() {
			c.CreatedAt = time.Now().UTC()
		}
		mdJSON, _ := json.Marshal(c.MetadataJSON)
		if _, err := tx.Exec(ctx, `
INSERT INTO memora_cells (cell_id, memory_id, seq, text, text_md5, written_by_agent_id, embedding_model, vector_key, metadata_json, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT(memory_id, seq) DO UPDATE SET
    text=excluded.text,
    text_md5=excluded.text_md5,
    written_by_agent_id=excluded.written_by_agent_id,
    embedding_model=excluded.embedding_model,
    vector_key=excluded.vector_key,
    metadata_json=excluded.metadata_json`,
			c.CellID, c.MemoryID, c.Seq, c.Text, c.TextMD5, c.WrittenByAgentID,
			nullableStr(c.EmbeddingModel), nullableStr(c.VectorKey), nullableJSON(mdJSON), c.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *MetadataStore) GetCells(ctx context.Context, memoryID string) ([]types.Cell, error) {
	rows, err := s.pool.Query(ctx, `
SELECT cell_id, memory_id, seq, text, text_md5, written_by_agent_id, embedding_model, vector_key, metadata_json, created_at
FROM memora_cells WHERE memory_id = $1 ORDER BY seq`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Cell
	for rows.Next() {
		var c types.Cell
		var emb, vk *string
		var mdJSON []byte
		if err := rows.Scan(&c.CellID, &c.MemoryID, &c.Seq, &c.Text, &c.TextMD5,
			&c.WrittenByAgentID, &emb, &vk, &mdJSON, &c.CreatedAt); err != nil {
			return nil, err
		}
		if emb != nil {
			c.EmbeddingModel = *emb
		}
		if vk != nil {
			c.VectorKey = *vk
		}
		if len(mdJSON) > 0 {
			_ = json.Unmarshal(mdJSON, &c.MetadataJSON)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *MetadataStore) UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE memora_cells SET vector_key=$1, embedding_model=$2 WHERE cell_id=$3`,
		vectorKey, embeddingModel, cellID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

func (s *MetadataStore) FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE memora_memories
SET    recall_ready = TRUE,
       updated_at   = $1
WHERE  id           = $2
  AND  recall_ready = FALSE
  AND  EXISTS (SELECT 1 FROM memora_cells WHERE memory_id = $2)
  AND  NOT EXISTS (
           SELECT 1 FROM memora_cells
           WHERE memory_id = $2
             AND (vector_key IS NULL OR vector_key = '')
       )`, time.Now().UTC(), memoryID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// --- Watermark history ---

func (s *MetadataStore) GetWatermarkHistory(ctx context.Context, workspaceID, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error) {
	rows, err := s.pool.Query(ctx, `
SELECT wh.target_id, wh.watermark, wh.op, wh.agent_id, wh.created_at, wh.content_md5_before, wh.content_md5_after
FROM memora_watermark_history wh
INNER JOIN memora_memories m ON m.id = wh.target_id AND m.workspace_id = $1
WHERE wh.target_id = $2 AND wh.created_at >= $3 ORDER BY wh.created_at DESC`,
		workspaceID, targetID, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WatermarkHistoryEntry
	for rows.Next() {
		var e types.WatermarkHistoryEntry
		var b, a *string
		if err := rows.Scan(&e.TargetID, &e.Watermark, &e.Op, &e.AgentID, &e.CreatedAt, &b, &a); err != nil {
			return nil, err
		}
		if b != nil {
			e.ContentMD5Before = *b
		}
		if a != nil {
			e.ContentMD5After = *a
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *MetadataStore) AppendWatermarkHistory(ctx context.Context, e types.WatermarkHistoryEntry) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.TargetID, e.Watermark, e.Op, e.AgentID, e.CreatedAt, nullableStr(e.ContentMD5Before), nullableStr(e.ContentMD5After))
	return err
}

// --- Tags ---

func (s *MetadataStore) UpsertTag(ctx context.Context, workspaceID, memoryID, key, value string) error {
	tag, err := s.pool.Exec(ctx, `
INSERT INTO memora_tags(memory_id, key, value)
SELECT $1, $2, $3
WHERE EXISTS (SELECT 1 FROM memora_memories WHERE id = $1 AND workspace_id = $4)
ON CONFLICT(memory_id, key) DO UPDATE SET value = excluded.value`,
		memoryID, key, value, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

func (s *MetadataStore) DeleteTag(ctx context.Context, workspaceID, memoryID, key string) error {
	_, err := s.pool.Exec(ctx, `
DELETE FROM memora_tags WHERE memory_id = $1 AND key = $2
AND EXISTS (SELECT 1 FROM memora_memories WHERE id = $1 AND workspace_id = $3)`,
		memoryID, key, workspaceID)
	return err
}

// --- Agent registry ---

func (s *MetadataStore) RegisterAgent(ctx context.Context, a *types.Agent) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.RegisteredAt.IsZero() {
		a.RegisteredAt = time.Now().UTC()
	}
	a.Active = true
	a.IdentityProofHash = types.HashIdentityProof(a.IdentityProof)
	capsJSON, _ := json.Marshal(a.Capabilities)
	_, err := s.pool.Exec(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, display_name, identity_provider, identity_proof,
    agent_type, model, capabilities_json, registered_at, last_seen_at, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE)
ON CONFLICT(agent_id) DO UPDATE SET
    display_name=excluded.display_name,
    identity_provider=excluded.identity_provider,
    identity_proof=excluded.identity_proof,
    agent_type=excluded.agent_type,
    model=excluded.model,
    capabilities_json=excluded.capabilities_json,
    last_seen_at=excluded.last_seen_at,
    active=TRUE`,
		a.AgentID, a.WorkspaceID, nullableStr(a.DisplayName), a.IdentityProvider,
		nullableStr(a.IdentityProofHash), nullableStr(a.AgentType), nullableStr(a.Model),
		nullableJSON(capsJSON), a.RegisteredAt, time.Now().UTC())
	return err
}

func (s *MetadataStore) GetAgent(ctx context.Context, workspaceID, id string) (*types.Agent, error) {
	a := &types.Agent{}
	var dn, proof, at, model *string
	var capsJSON []byte
	var lastSeen *time.Time
	err := s.pool.QueryRow(ctx, `
SELECT agent_id, workspace_id, display_name, identity_provider, identity_proof, agent_type, model,
    capabilities_json, registered_at, last_seen_at, active
FROM memora_agents WHERE agent_id = $1 AND workspace_id = $2`, id, workspaceID).Scan(
		&a.AgentID, &a.WorkspaceID, &dn, &a.IdentityProvider, &proof, &at, &model, &capsJSON,
		&a.RegisteredAt, &lastSeen, &a.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if dn != nil {
		a.DisplayName = *dn
	}
	if proof != nil {
		a.IdentityProofHash = *proof
	}
	if at != nil {
		a.AgentType = *at
	}
	if model != nil {
		a.Model = *model
	}
	a.LastSeenAt = lastSeen
	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &a.Capabilities)
	}
	return a, nil
}

func (s *MetadataStore) ListAgents(ctx context.Context, workspaceID string, limit int) ([]types.Agent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT agent_id, workspace_id, display_name, identity_provider, identity_proof, agent_type, model,
    capabilities_json, registered_at, last_seen_at, active
FROM memora_agents WHERE workspace_id = $1 ORDER BY registered_at DESC LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Agent
	for rows.Next() {
		var a types.Agent
		var dn, proof, at, model *string
		var capsJSON []byte
		var lastSeen *time.Time
		if err := rows.Scan(&a.AgentID, &a.WorkspaceID, &dn, &a.IdentityProvider, &proof, &at, &model,
			&capsJSON, &a.RegisteredAt, &lastSeen, &a.Active); err != nil {
			return nil, err
		}
		if dn != nil {
			a.DisplayName = *dn
		}
		if proof != nil {
			a.IdentityProofHash = *proof
		}
		if at != nil {
			a.AgentType = *at
		}
		if model != nil {
			a.Model = *model
		}
		a.LastSeenAt = lastSeen
		if len(capsJSON) > 0 {
			_ = json.Unmarshal(capsJSON, &a.Capabilities)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *MetadataStore) DeactivateAgent(ctx context.Context, workspaceID, id string) error {
	tag, err := s.pool.Exec(ctx, "UPDATE memora_agents SET active = FALSE WHERE agent_id = $1 AND workspace_id = $2", id, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
