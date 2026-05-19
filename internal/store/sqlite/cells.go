package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// UpsertCells inserts or updates cells for a Memory. Idempotent on
// (memory_id, seq).
func (s *Store) UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
		if _, err := tx.ExecContext(ctx, `
INSERT INTO memora_cells (cell_id, memory_id, seq, text, text_md5, written_by_agent_id, embedding_model, vector_key, metadata_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(memory_id, seq) DO UPDATE SET
    text=excluded.text,
    text_md5=excluded.text_md5,
    written_by_agent_id=excluded.written_by_agent_id,
    embedding_model=excluded.embedding_model,
    vector_key=excluded.vector_key,
    metadata_json=excluded.metadata_json`,
			c.CellID, c.MemoryID, c.Seq, c.Text, c.TextMD5, c.WrittenByAgentID,
			nullableStr(c.EmbeddingModel), nullableStr(c.VectorKey), nullableStr(string(mdJSON)), c.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetCells returns a Memory's cells ordered by seq.
func (s *Store) GetCells(ctx context.Context, memoryID string) ([]types.Cell, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT cell_id, memory_id, seq, text, text_md5, written_by_agent_id, embedding_model, vector_key, metadata_json, created_at
FROM memora_cells WHERE memory_id = ? ORDER BY seq`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Cell
	for rows.Next() {
		var c types.Cell
		var emb, vk, mdj sql.NullString
		var createdAt sqliteTime
		if err := rows.Scan(&c.CellID, &c.MemoryID, &c.Seq, &c.Text, &c.TextMD5, &c.WrittenByAgentID,
			&emb, &vk, &mdj, &createdAt); err != nil {
			return nil, err
		}
		c.EmbeddingModel = emb.String
		c.VectorKey = vk.String
		c.CreatedAt = createdAt.Time
		if mdj.Valid && mdj.String != "" {
			_ = json.Unmarshal([]byte(mdj.String), &c.MetadataJSON)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCellVectorKey records the vector backend's reference after embedding.
func (s *Store) UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_cells SET vector_key=?, embedding_model=? WHERE cell_id=?`,
		vectorKey, embeddingModel, cellID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	return nil
}

// FlipRecallReadyIfAllEmbedded sets recall_ready=1 on a memory iff every
// one of its cells has a non-empty vector_key. Atomic via a single
// conditional UPDATE so concurrent embed workers can't race.
func (s *Store) FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_memories
SET    recall_ready = 1,
       updated_at   = ?
WHERE  id           = ?
  AND  recall_ready = 0
  AND  EXISTS (SELECT 1 FROM memora_cells WHERE memory_id = ?)
  AND  NOT EXISTS (
           SELECT 1 FROM memora_cells
           WHERE memory_id = ?
             AND (vector_key IS NULL OR vector_key = '')
       )`, time.Now().UTC(), memoryID, memoryID, memoryID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
