// Package pgvector implements the Memora VectorStore using Postgres
// with the pgvector extension. Supports HNSW ANN search via the <=>
// (cosine distance) operator.
package pgvector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/axiom-studio/memora/pkg/adapter"
)

func init() {
	adapter.RegisterVector("pgvector", func() adapter.VectorStore { return &Store{} })
}

type Store struct {
	pool *pgxpool.Pool
	dim  int
}

func (s *Store) Open(ctx context.Context, cfg adapter.VectorConfig) error {
	if cfg.DSN == "" {
		return errors.New("pgvector: DSN required")
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return fmt.Errorf("pgvector: parse DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("pgvector: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("pgvector: ping: %w", err)
	}
	s.pool = pool
	s.dim = cfg.Dim
	if s.dim <= 0 {
		s.dim = 768
	}

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		pool.Close()
		return fmt.Errorf("pgvector: create extension: %w", err)
	}

	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS memora_cell_vectors (
    workspace_id  TEXT NOT NULL,
    collection_id TEXT,
    memory_id     TEXT NOT NULL,
    cell_id       TEXT PRIMARY KEY,
    embedding     vector(%d) NOT NULL,
    metadata_json JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
)`, s.dim)); err != nil {
		pool.Close()
		return fmt.Errorf("pgvector: create table: %w", err)
	}

	for _, idx := range []string{
		"CREATE INDEX IF NOT EXISTS idx_cellvec_workspace ON memora_cell_vectors(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_cellvec_memory ON memora_cell_vectors(memory_id)",
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_cellvec_hnsw ON memora_cell_vectors USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64)"),
	} {
		if _, err := pool.Exec(ctx, idx); err != nil {
			pool.Close()
			return fmt.Errorf("pgvector: create index: %w", err)
		}
	}

	return nil
}

func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("pgvector: not opened")
	}
	return s.pool.Ping(ctx)
}

func (s *Store) Capabilities() adapter.VectorCapabilities {
	return adapter.VectorCapabilities{
		SupportsExactSearch:  true,
		SupportsANN:          true,
		SupportsHybridFilter: true,
		MaxDimensions:        16000,
	}
}

func (s *Store) PutVector(ctx context.Context, p adapter.VectorPut) error {
	return s.PutVectorsBatch(ctx, []adapter.VectorPut{p})
}

func (s *Store) PutVectorsBatch(ctx context.Context, puts []adapter.VectorPut) error {
	if len(puts) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	for _, p := range puts {
		mdJSON, _ := json.Marshal(p.Metadata)
		vecStr := floatsToVectorLiteral(p.Embedding)
		var collID any
		if p.Key.CollectionID != "" {
			collID = p.Key.CollectionID
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO memora_cell_vectors (workspace_id, collection_id, memory_id, cell_id, embedding, metadata_json, created_at)
VALUES ($1, $2, $3, $4, $5::vector, $6, $7)
ON CONFLICT(cell_id) DO UPDATE SET
    workspace_id=excluded.workspace_id,
    collection_id=excluded.collection_id,
    memory_id=excluded.memory_id,
    embedding=excluded.embedding,
    metadata_json=excluded.metadata_json`,
			p.Key.WorkspaceID, collID, p.Key.MemoryID, p.Key.CellID,
			vecStr, nullableJSON(mdJSON), now); err != nil {
			return fmt.Errorf("pgvector: put %s: %w", p.Key.CellID, err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Query(ctx context.Context, q adapter.VectorQuery) ([]adapter.VectorHit, error) {
	if q.K <= 0 {
		q.K = 5
	}

	where := []string{"workspace_id = $1"}
	args := []any{q.WorkspaceID}
	argN := 2

	if q.Filter.CollectionID != "" {
		where = append(where, fmt.Sprintf("collection_id = $%d", argN))
		args = append(args, q.Filter.CollectionID)
		argN++
	}

	vecStr := floatsToVectorLiteral(q.Embedding)
	whereClause := strings.Join(where, " AND ")

	query := fmt.Sprintf(`
SELECT workspace_id, collection_id, memory_id, cell_id,
       1 - (embedding <=> $%d::vector) AS score,
       metadata_json
FROM memora_cell_vectors
WHERE %s
ORDER BY embedding <=> $%d::vector
LIMIT $%d`, argN, whereClause, argN, argN+1)
	args = append(args, vecStr, q.K)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector: query: %w", err)
	}
	defer rows.Close()

	var hits []adapter.VectorHit
	for rows.Next() {
		var h adapter.VectorHit
		var coll *string
		var mdJSON []byte
		if err := rows.Scan(&h.Key.WorkspaceID, &coll, &h.Key.MemoryID, &h.Key.CellID,
			&h.Score, &mdJSON); err != nil {
			return nil, err
		}
		if coll != nil {
			h.Key.CollectionID = *coll
		}
		if len(mdJSON) > 0 {
			_ = json.Unmarshal(mdJSON, &h.Metadata)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func (s *Store) DeleteVectors(ctx context.Context, keys []adapter.VectorKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.CellID
	}
	_, err := s.pool.Exec(ctx,
		"DELETE FROM memora_cell_vectors WHERE cell_id = ANY($1)", ids)
	return err
}

func floatsToVectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteByte(']')
	return b.String()
}

func nullableJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}

// Ensure Store implements VectorStore at compile time.
var _ adapter.VectorStore = (*Store)(nil)

// Compile-time check that pgx.ErrNoRows is available (import used).
var _ = pgx.ErrNoRows
