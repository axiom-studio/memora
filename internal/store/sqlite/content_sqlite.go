package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterContent("sqlite", func() adapter.ContentStore { return &ContentStore{} })
}

// ContentStore is the SQLite-backed ContentStore. It stores content in
// the memora_content table within the same database as the primary
// store (shared DSN). This is the transitional driver for Phase A.
type ContentStore struct {
	db *sql.DB
}

func (c *ContentStore) Open(ctx context.Context, cfg adapter.ContentConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite content: DSN required")
	}
	db, err := openSQLiteDB(cfg.DSN)
	if err != nil {
		return err
	}
	c.db = db
	_, err = c.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memora_content (
    workspace_id TEXT NOT NULL,
    memory_id    TEXT NOT NULL,
    cell_id      TEXT NOT NULL DEFAULT '',
    content_md5  TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (workspace_id, memory_id, cell_id)
)`)
	return err
}

func (c *ContentStore) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

func (c *ContentStore) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

func (c *ContentStore) Capabilities() adapter.ContentCapabilities {
	return adapter.ContentCapabilities{
		SupportsConditionalPut: false,
		SupportsBatchGet:       true,
		RecommendedMaxObjectMB: 50,
		DurabilityClass:        "single-disk",
	}
}

func (c *ContentStore) PutMemoryContent(ctx context.Context, workspaceID, memoryID, contentMD5, content string) error {
	_, err := c.db.ExecContext(ctx, `
INSERT INTO memora_content (workspace_id, memory_id, cell_id, content_md5, content)
VALUES (?, ?, '', ?, ?)
ON CONFLICT(workspace_id, memory_id, cell_id) DO UPDATE SET
    content_md5 = excluded.content_md5,
    content     = excluded.content`,
		workspaceID, memoryID, contentMD5, content)
	return err
}

func (c *ContentStore) GetMemoryContent(ctx context.Context, workspaceID, memoryID string) (string, error) {
	var content string
	err := c.db.QueryRowContext(ctx,
		`SELECT content FROM memora_content WHERE workspace_id = ? AND memory_id = ? AND cell_id = ''`,
		workspaceID, memoryID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", types.ErrNotFound
	}
	return content, err
}

func (c *ContentStore) DeleteMemoryContent(ctx context.Context, workspaceID, memoryID string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM memora_content WHERE workspace_id = ? AND memory_id = ? AND cell_id = ''`,
		workspaceID, memoryID)
	return err
}

func (c *ContentStore) PutCellContent(ctx context.Context, workspaceID, memoryID, cellID, textMD5, text string) error {
	_, err := c.db.ExecContext(ctx, `
INSERT INTO memora_content (workspace_id, memory_id, cell_id, content_md5, content)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, memory_id, cell_id) DO UPDATE SET
    content_md5 = excluded.content_md5,
    content     = excluded.content`,
		workspaceID, memoryID, cellID, textMD5, text)
	return err
}

func (c *ContentStore) GetCellContent(ctx context.Context, workspaceID, memoryID, cellID string) (string, error) {
	var content string
	err := c.db.QueryRowContext(ctx,
		`SELECT content FROM memora_content WHERE workspace_id = ? AND memory_id = ? AND cell_id = ?`,
		workspaceID, memoryID, cellID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", types.ErrNotFound
	}
	return content, err
}

func (c *ContentStore) GetCellContentBatch(ctx context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error) {
	if len(cellIDs) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]string, len(cellIDs))
	args := make([]any, 0, len(cellIDs)+2)
	args = append(args, workspaceID, memoryID)
	for i, id := range cellIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT cell_id, content FROM memora_content WHERE workspace_id = ? AND memory_id = ? AND cell_id IN (` + joinStrings(placeholders, ",") + `)`
	rows, err := c.db.QueryContext(ctx, query, args...)
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
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM memora_content WHERE workspace_id = ? AND memory_id = ? AND cell_id = ?`,
		workspaceID, memoryID, cellID)
	return err
}

func (c *ContentStore) DeleteAllForMemory(ctx context.Context, workspaceID, memoryID string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM memora_content WHERE workspace_id = ? AND memory_id = ?`,
		workspaceID, memoryID)
	return err
}

func (c *ContentStore) ListMemoryIDs(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT DISTINCT memory_id FROM memora_content WHERE workspace_id = ?`, workspaceID)
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
	rows, err := c.db.QueryContext(ctx,
		`SELECT DISTINCT memory_id FROM memora_content
		 WHERE workspace_id = ? AND created_at < ?`, workspaceID, cutoff.UTC().Format(time.RFC3339Nano))
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

func joinStrings(s []string, sep string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}
