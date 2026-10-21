package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/axiom-studio/memora/pkg/adapter"
)

// SearchKeyword implements adapter.MetadataStore with the generated
// memora_memories.content_tsv column (english stemming, GIN-indexed),
// ranked by ts_rank. Terms are letter/digit runs from adapter.KeywordTerms
// joined with "|", so the tsquery is built only from word characters and
// query text never becomes tsquery syntax; OR-ing keeps natural-language
// prompts matching.
func (s *MetadataStore) SearchKeyword(ctx context.Context, q adapter.KeywordQuery) ([]adapter.KeywordHit, error) {
	workspaces, err := q.Scope()
	if err != nil {
		return nil, err
	}
	terms := adapter.KeywordTerms(q.Query)
	if len(terms) == 0 {
		return nil, nil
	}
	k := q.K
	if k <= 0 {
		k = 5
	}
	args := []any{strings.Join(terms, " | ")}
	where := []string{"content_tsv @@ to_tsquery('english', $1)", "deleted_at IS NULL"}
	if workspaces != nil {
		args = append(args, workspaces)
		where = append(where, fmt.Sprintf("workspace_id = ANY($%d)", len(args)))
	}
	if q.CollectionID != "" {
		args = append(args, q.CollectionID)
		where = append(where, fmt.Sprintf("collection_id = $%d", len(args)))
	}
	args = append(args, k)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
SELECT id, workspace_id, ts_rank(content_tsv, to_tsquery('english', $1)) AS score
FROM memora_memories
WHERE %s
ORDER BY score DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("full-text search: %w", err)
	}
	defer rows.Close()
	var hits []adapter.KeywordHit
	for rows.Next() {
		var h adapter.KeywordHit
		var score float32
		if err := rows.Scan(&h.MemoryID, &h.WorkspaceID, &score); err != nil {
			return nil, fmt.Errorf("full-text search: %w", err)
		}
		h.Score = float64(score)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
