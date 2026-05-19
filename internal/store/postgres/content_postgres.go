package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterContent("postgres", func() adapter.ContentStore { return &ContentStore{} })
}

type ContentStore struct {
	pool *pgxpool.Pool
}

func (c *ContentStore) Open(ctx context.Context, cfg adapter.ContentConfig) error {
	if cfg.DSN == "" {
		return errors.New("postgres content: DSN required")
	}
	pool, err := openPool(ctx, cfg.DSN)
	if err != nil {
		return err
	}
	c.pool = pool
	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return fmt.Errorf("postgres content: migrate: %w", err)
	}
	return nil
}

func (c *ContentStore) Close() error {
	if c.pool != nil {
		c.pool.Close()
	}
	return nil
}

func (c *ContentStore) Ping(ctx context.Context) error {
	if c.pool == nil {
		return errors.New("postgres content: not opened")
	}
	return c.pool.Ping(ctx)
}

func (c *ContentStore) Capabilities() adapter.ContentCapabilities {
	return adapter.ContentCapabilities{
		SupportsConditionalPut: false,
		SupportsBatchGet:       true,
		RecommendedMaxObjectMB: 500,
		DurabilityClass:        "5-nines",
	}
}

func (c *ContentStore) PutMemoryContent(ctx context.Context, workspaceID, memoryID, contentMD5, content string) error {
	_, err := c.pool.Exec(ctx, `
INSERT INTO memora_content (workspace_id, memory_id, cell_id, content_md5, content)
VALUES ($1, $2, '', $3, $4)
ON CONFLICT(workspace_id, memory_id, cell_id) DO UPDATE SET
    content_md5 = excluded.content_md5,
    content     = excluded.content`,
		workspaceID, memoryID, contentMD5, content)
	return err
}

func (c *ContentStore) GetMemoryContent(ctx context.Context, workspaceID, memoryID string) (string, error) {
	var content string
	err := c.pool.QueryRow(ctx,
		`SELECT content FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id = ''`,
		workspaceID, memoryID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", types.ErrNotFound
	}
	return content, err
}

func (c *ContentStore) DeleteMemoryContent(ctx context.Context, workspaceID, memoryID string) error {
	_, err := c.pool.Exec(ctx,
		`DELETE FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id = ''`,
		workspaceID, memoryID)
	return err
}

func (c *ContentStore) PutCellContent(ctx context.Context, workspaceID, memoryID, cellID, textMD5, text string) error {
	_, err := c.pool.Exec(ctx, `
INSERT INTO memora_content (workspace_id, memory_id, cell_id, content_md5, content)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT(workspace_id, memory_id, cell_id) DO UPDATE SET
    content_md5 = excluded.content_md5,
    content     = excluded.content`,
		workspaceID, memoryID, cellID, textMD5, text)
	return err
}

func (c *ContentStore) GetCellContent(ctx context.Context, workspaceID, memoryID, cellID string) (string, error) {
	var content string
	err := c.pool.QueryRow(ctx,
		`SELECT content FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id = $3`,
		workspaceID, memoryID, cellID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", types.ErrNotFound
	}
	return content, err
}

func (c *ContentStore) GetCellContentBatch(ctx context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error) {
	if len(cellIDs) == 0 {
		return map[string]string{}, nil
	}
	rows, err := c.pool.Query(ctx,
		`SELECT cell_id, content FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id = ANY($3)`,
		workspaceID, memoryID, cellIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(cellIDs))
	for rows.Next() {
		var cellID, content string
		if err := rows.Scan(&cellID, &content); err != nil {
			return nil, err
		}
		out[cellID] = content
	}
	return out, rows.Err()
}

func (c *ContentStore) DeleteCellContent(ctx context.Context, workspaceID, memoryID, cellID string) error {
	_, err := c.pool.Exec(ctx,
		`DELETE FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id = $3`,
		workspaceID, memoryID, cellID)
	return err
}

func (c *ContentStore) DeleteAllForMemory(ctx context.Context, workspaceID, memoryID string) error {
	_, err := c.pool.Exec(ctx,
		`DELETE FROM memora_content WHERE workspace_id = $1 AND memory_id = $2`,
		workspaceID, memoryID)
	return err
}

func (c *ContentStore) ListMemoryIDs(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT DISTINCT memory_id FROM memora_content WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *ContentStore) ListCellIDs(ctx context.Context, workspaceID, memoryID string) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT cell_id FROM memora_content WHERE workspace_id = $1 AND memory_id = $2 AND cell_id != ''`, workspaceID, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *ContentStore) ListMemoryIDsOlderThan(ctx context.Context, workspaceID string, cutoff time.Time) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT DISTINCT memory_id FROM memora_content WHERE workspace_id = $1 AND created_at < $2`,
		workspaceID, cutoff.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
