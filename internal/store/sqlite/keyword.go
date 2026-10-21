package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/axiom-studio/memora/pkg/adapter"
)

// SearchKeyword implements adapter.MetadataStore with the FTS5 index
// memora_memories_fts (porter stemming), ranked by BM25. Each term is a
// quoted FTS5 string, so query text never becomes FTS5 syntax; terms are
// OR-ed so a natural-language prompt still matches. bm25() is lower-is-
// better, hence the negation. The workspace and collection filters run in
// the same statement against memora_memories, which also excludes
// forgotten memories.
func (s *Store) SearchKeyword(ctx context.Context, q adapter.KeywordQuery) ([]adapter.KeywordHit, error) {
	workspaces, err := q.Scope()
	if err != nil {
		return nil, err
	}
	terms := adapter.KeywordTerms(q.Query)
	if len(terms) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	k := q.K
	if k <= 0 {
		k = 5
	}
	where := []string{"memora_memories_fts MATCH ?", "m.deleted_at IS NULL"}
	args := []any{strings.Join(quoted, " OR ")}
	if workspaces != nil {
		where = append(where, "m.workspace_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(workspaces)), ",")+")")
		for _, ws := range workspaces {
			args = append(args, ws)
		}
	}
	if q.CollectionID != "" {
		where = append(where, "m.collection_id = ?")
		args = append(args, q.CollectionID)
	}
	args = append(args, k)
	//nolint:gosec // G202: only fixed SQL fragments and "?" placeholders are concatenated; values stay parameterized
	rows, err := s.db.QueryContext(ctx, `
SELECT m.id, m.workspace_id, -bm25(memora_memories_fts) AS score
FROM memora_memories_fts JOIN memora_memories m ON m.id = memora_memories_fts.memory_id
WHERE `+strings.Join(where, " AND ")+`
ORDER BY score DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()
	var hits []adapter.KeywordHit
	for rows.Next() {
		var h adapter.KeywordHit
		if err := rows.Scan(&h.MemoryID, &h.WorkspaceID, &h.Score); err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
