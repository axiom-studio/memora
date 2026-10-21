// Package sqlitevec implements the Memora VectorStore against a
// SQLite database. v0.1 ships a pure-Go cosine-similarity scan rather
// than a vendored vec0 extension — modernc.org/sqlite does not support
// loadable extensions today, so a real sqlite-vec integration would
// require switching the driver. This implementation matches the same
// interface and exposes the same Capabilities so callers see no
// difference at the API surface; only Query performance differs at
// scale. See F12 (Packaging) / R-OSS-2 for the upgrade path.
package sqlitevec

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"

	_ "modernc.org/sqlite"
)

func init() {
	adapter.RegisterVector("sqlite-vec", func() adapter.VectorStore { return &Store{} })
}

// Store implements adapter.VectorStore against SQLite.
type Store struct {
	db    *sql.DB
	dim   int
	model string // active embedder model ID; rows record theirs
}

// Open implements adapter.VectorStore.
func (s *Store) Open(ctx context.Context, cfg adapter.VectorConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite-vec: DSN required")
	}
	dsn := cfg.DSN
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sqlite-vec: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return err
	}
	db.SetMaxOpenConns(1)
	s.db = db
	s.dim = cfg.Dim
	if s.dim <= 0 {
		s.dim = 768
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS memora_cell_vectors (
    workspace_id  TEXT NOT NULL,
    collection_id TEXT,
    memory_id     TEXT NOT NULL,
    cell_id       TEXT PRIMARY KEY,
    dim           INTEGER NOT NULL,
    vector_blob   BLOB NOT NULL,
    metadata_json TEXT,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cellvec_workspace ON memora_cell_vectors(workspace_id);
CREATE INDEX IF NOT EXISTS idx_cellvec_memory   ON memora_cell_vectors(memory_id);`); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite-vec: migrate: %w", err)
	}
	s.model = cfg.Model
	if err := s.migrateModel(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite-vec: migrate model column: %w", err)
	}
	return nil
}

// migrateModel adds the per-row model column (S-7). Rows written before
// it have no model: those at the active dimension are adopted as the
// active model's, as they were treated until now; others stay stale.
func (s *Store) migrateModel(ctx context.Context) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('memora_cell_vectors') WHERE name = 'model'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE memora_cell_vectors ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if s.model != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE memora_cell_vectors SET model = ? WHERE model = '' AND dim = ?`, s.model, s.dim); err != nil {
			return err
		}
	}
	return nil
}

// StaleVectors implements adapter.ModelAwareVectorStore.
func (s *Store) StaleVectors(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memora_cell_vectors WHERE dim != ? OR model != ?`, s.dim, s.model).Scan(&n)
	return n, err
}

// FreshCells implements adapter.ModelAwareVectorStore.
func (s *Store) FreshCells(ctx context.Context, cellIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(cellIDs))
	if len(cellIDs) == 0 {
		return out, nil
	}
	args := []any{s.dim, s.model}
	for _, id := range cellIDs {
		args = append(args, id)
	}
	//nolint:gosec // G202: only "?" placeholders are concatenated; values stay parameterized
	rows, err := s.db.QueryContext(ctx, `SELECT cell_id FROM memora_cell_vectors WHERE dim = ? AND model = ? AND cell_id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(cellIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Close implements adapter.VectorStore.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping implements adapter.VectorStore.
func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return errors.New("sqlite-vec: not opened")
	}
	return s.db.PingContext(ctx)
}

// Capabilities implements adapter.VectorStore.
func (s *Store) Capabilities() adapter.VectorCapabilities {
	return adapter.VectorCapabilities{
		SupportsExactSearch:  true,
		SupportsANN:          false, // pure-Go cosine scan; v1 upgrade adds vec0
		SupportsHybridFilter: true,
		MaxDimensions:        4096,
	}
}

// PutVector implements adapter.VectorStore.
func (s *Store) PutVector(ctx context.Context, p adapter.VectorPut) error {
	return s.PutVectorsBatch(ctx, []adapter.VectorPut{p})
}

// PutVectorsBatch implements adapter.VectorStore.
func (s *Store) PutVectorsBatch(ctx context.Context, puts []adapter.VectorPut) error {
	if len(puts) == 0 {
		return nil
	}
	for _, p := range puts {
		if len(p.Embedding) != s.dim {
			return fmt.Errorf("%w: cell %s vector has %d dimensions, store is sized for %d (re-embed after changing the embedding model)",
				types.ErrInvalidInput, p.Key.CellID, len(p.Embedding), s.dim)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO memora_cell_vectors (workspace_id, collection_id, memory_id, cell_id, dim, model, vector_blob, metadata_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(cell_id) DO UPDATE SET
    workspace_id=excluded.workspace_id,
    collection_id=excluded.collection_id,
    memory_id=excluded.memory_id,
    dim=excluded.dim,
    model=excluded.model,
    vector_blob=excluded.vector_blob,
    metadata_json=excluded.metadata_json`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for _, p := range puts {
		mdJSON, _ := json.Marshal(p.Metadata)
		if _, err := stmt.ExecContext(ctx,
			p.Key.WorkspaceID, nullableStr(p.Key.CollectionID), p.Key.MemoryID, p.Key.CellID,
			len(p.Embedding), s.model, encodeFloats(p.Embedding), nullableStr(string(mdJSON)), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteVectors implements adapter.VectorStore.
func (s *Store) DeleteVectors(ctx context.Context, keys []adapter.VectorKey) error {
	if len(keys) == 0 {
		return nil
	}
	args := make([]any, len(keys))
	ph := make([]string, len(keys))
	for i, k := range keys {
		args[i] = k.CellID
		ph[i] = "?"
	}
	//nolint:gosec // G202: only fixed SQL fragments and "?" placeholders are concatenated; values stay parameterized
	q := "DELETE FROM memora_cell_vectors WHERE cell_id IN (" + strings.Join(ph, ",") + ")"
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// Query implements adapter.VectorStore via cosine similarity over the
// candidate set after filter pushdown.
func (s *Store) Query(ctx context.Context, q adapter.VectorQuery) ([]adapter.VectorHit, error) {
	if q.K <= 0 {
		q.K = 5
	}
	if len(q.Embedding) != s.dim {
		return nil, fmt.Errorf("%w: query vector has %d dimensions, store is sized for %d", types.ErrInvalidInput, len(q.Embedding), s.dim)
	}
	workspaces, err := q.Scope()
	if err != nil {
		return nil, err
	}
	// Rows from another model or width (written before an embedding-model
	// change) are not comparable; skip them rather than mixing scores from
	// two vector spaces. `memora-core reembed` brings them back.
	where := []string{"dim = ?", "model = ?"}
	args := []any{s.dim, s.model}
	if workspaces != nil {
		where = append(where, "workspace_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(workspaces)), ",")+")")
		for _, ws := range workspaces {
			args = append(args, ws)
		}
	}
	if q.Filter.CollectionID != "" {
		where = append(where, "collection_id = ?")
		args = append(args, q.Filter.CollectionID)
	}
	//nolint:gosec // G202: only fixed SQL fragments and "?" placeholders are concatenated; values stay parameterized
	sql := `SELECT workspace_id, collection_id, memory_id, cell_id, vector_blob, metadata_json
FROM memora_cell_vectors WHERE ` + strings.Join(where, " AND ")
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []adapter.VectorHit
	for rows.Next() {
		var (
			ws, mem, cell string
			coll          sql2NullString
			blob          []byte
			mdj           sql2NullString
		)
		if err := rows.Scan(&ws, &coll, &mem, &cell, &blob, &mdj); err != nil {
			return nil, err
		}
		vec, err := decodeFloats(blob)
		if err != nil {
			continue
		}
		score := cosine(q.Embedding, vec)
		hit := adapter.VectorHit{
			Key: adapter.VectorKey{
				WorkspaceID:  ws,
				CollectionID: coll.String,
				MemoryID:     mem,
				CellID:       cell,
			},
			Score: score,
		}
		if mdj.Valid && mdj.String != "" {
			_ = json.Unmarshal([]byte(mdj.String), &hit.Metadata)
		}
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > q.K {
		hits = hits[:q.K]
	}
	return hits, rows.Err()
}

type sql2NullString = sql.NullString

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func encodeFloats(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

func decodeFloats(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("vector blob length %d not multiple of 4", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
