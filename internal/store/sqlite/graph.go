package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// GraphLink creates a typed edge. CAS-like uniqueness on the live triple.
func (s *Store) GraphLink(ctx context.Context, e types.Edge) (types.Edge, error) {
	if e.EdgeID == "" {
		e.EdgeID = types.NewID(types.EdgeIDPrefix)
	}
	if err := e.Validate(); err != nil {
		return types.Edge{}, err
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.Watermark == "" {
		e.Watermark = types.NewWatermark()
	}
	// Validate both endpoints exist in the same workspace.
	if err := s.assertEndpointsInWorkspace(ctx, e.WorkspaceID, e.SourceMemoryID, e.TargetMemoryID); err != nil {
		return types.Edge{}, err
	}
	// Out-degree cap (OSS = 1000).
	var outDeg int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memora_edges WHERE workspace_id = ? AND source_memory_id = ? AND deleted_at IS NULL`,
		e.WorkspaceID, e.SourceMemoryID).Scan(&outDeg); err != nil {
		return types.Edge{}, err
	}
	if outDeg >= 1000 {
		return types.Edge{}, fmt.Errorf("%w: out-degree limit (1000) reached for memory %s", types.ErrQuotaExceeded, e.SourceMemoryID)
	}
	// Synthetic-edge guard (F11.T2): when memora_edges_view exists and
	// already carries a `parent_of` edge between (source, target) from
	// the vibeflow_contexts shim, reject the explicit Link.
	if e.EdgeType == types.EdgeTypeParentOf {
		var seen int
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memora_edges_view
WHERE workspace_id = ? AND source_memory_id = ? AND target_memory_id = ? AND edge_type = 'parent_of'
  AND edge_id LIKE 'edg_synthetic_%'`,
			e.WorkspaceID, e.SourceMemoryID, e.TargetMemoryID).Scan(&seen)
		if err == nil && seen > 0 {
			return types.Edge{}, types.ErrAlreadyLinked
		}
	}
	propsJSON, _ := json.Marshal(e.PropertiesJSON)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_edges (edge_id, workspace_id, source_memory_id, target_memory_id, edge_type,
    properties_json, created_by_agent_id, created_at, watermark)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.EdgeID, e.WorkspaceID, e.SourceMemoryID, e.TargetMemoryID, string(e.EdgeType),
		nullableStr(string(propsJSON)), e.CreatedByAgentID, e.CreatedAt, e.Watermark)
	if err != nil {
		if isUniqueViolation(err) {
			return types.Edge{}, types.ErrAlreadyExists
		}
		return types.Edge{}, err
	}
	_ = s.AppendWatermarkHistory(ctx, types.WatermarkHistoryEntry{
		TargetID: e.EdgeID, Watermark: e.Watermark, Op: "link", AgentID: e.CreatedByAgentID, CreatedAt: e.CreatedAt,
	})
	return e, nil
}

func (s *Store) assertEndpointsInWorkspace(ctx context.Context, wsID, src, tgt string) error {
	var srcWS, tgtWS string
	err := s.db.QueryRowContext(ctx, "SELECT workspace_id FROM memora_memories WHERE id = ?", src).Scan(&srcWS)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: source memory %s", types.ErrNotFound, src)
	}
	if err != nil {
		return err
	}
	err = s.db.QueryRowContext(ctx, "SELECT workspace_id FROM memora_memories WHERE id = ?", tgt).Scan(&tgtWS)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: target memory %s", types.ErrNotFound, tgt)
	}
	if err != nil {
		return err
	}
	if srcWS != wsID || tgtWS != wsID {
		return fmt.Errorf("%w: edge endpoints must be in workspace %s (src=%s, tgt=%s)", types.ErrInvalidInput, wsID, srcWS, tgtWS)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "uq_edges_live_triple")
}

// GraphUnlink soft-deletes an edge.
func (s *Store) GraphUnlink(ctx context.Context, edgeID, agentID string) error {
	if strings.HasPrefix(edgeID, "edg_synthetic_") {
		return types.ErrSyntheticEdge
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_edges SET deleted_at = ? WHERE edge_id = ? AND deleted_at IS NULL`,
		time.Now().UTC(), edgeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	_ = s.AppendWatermarkHistory(ctx, types.WatermarkHistoryEntry{
		TargetID: edgeID, Watermark: types.NewWatermark(), Op: "unlink", AgentID: agentID, CreatedAt: time.Now().UTC(),
	})
	return nil
}

// GraphLinkBatch inserts up to 1000 edges, atomic-per-edge.
func (s *Store) GraphLinkBatch(ctx context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	if len(edges) > 1000 {
		return nil, fmt.Errorf("%w: LinkBatch capped at 1000 edges (got %d)", types.ErrQuotaExceeded, len(edges))
	}
	results := make([]adapter.LinkResult, len(edges))
	for i := range edges {
		e, err := s.GraphLink(ctx, edges[i])
		if err != nil {
			results[i] = adapter.LinkResult{Index: i, Status: "error", Error: err}
			continue
		}
		results[i] = adapter.LinkResult{Index: i, Status: "ok", EdgeID: e.EdgeID}
	}
	return results, nil
}

// GraphCascadeForget soft-deletes every edge incident to a memory.
func (s *Store) GraphCascadeForget(ctx context.Context, memoryID, agentID string) (int, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_edges SET deleted_at = ?
WHERE deleted_at IS NULL AND (source_memory_id = ? OR target_memory_id = ?)`, now, memoryID, memoryID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_ = s.AppendWatermarkHistory(ctx, types.WatermarkHistoryEntry{
			TargetID: memoryID, Watermark: types.NewWatermark(), Op: "edge_cascade_forget", AgentID: agentID, CreatedAt: now,
		})
	}
	return int(n), nil
}

// GraphNeighbors returns one-hop neighbors.
func (s *Store) GraphNeighbors(ctx context.Context, workspaceID, memoryID string, opts adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
	if opts.K <= 0 {
		opts.K = 50
	}
	if opts.K > 200 {
		opts.K = 200
	}
	dir := opts.Direction
	if dir == "" {
		dir = api.GraphDirBoth
	}

	var edges []types.Edge
	switch dir {
	case api.GraphDirOut:
		es, err := s.fetchEdges(ctx, workspaceID, memoryID, "source", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, es...)
	case api.GraphDirIn:
		es, err := s.fetchEdges(ctx, workspaceID, memoryID, "target", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, es...)
	case api.GraphDirBoth:
		out, err := s.fetchEdges(ctx, workspaceID, memoryID, "source", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		in, err := s.fetchEdges(ctx, workspaceID, memoryID, "target", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, out...)
		edges = append(edges, in...)
	}

	// Materialize neighbor headers.
	seen := map[string]bool{}
	var neighborIDs []string
	for _, e := range edges {
		other := e.TargetMemoryID
		if other == memoryID {
			other = e.SourceMemoryID
		}
		if !seen[other] {
			seen[other] = true
			neighborIDs = append(neighborIDs, other)
		}
	}
	headers, err := s.memoryHeaders(ctx, neighborIDs)
	return edges, headers, err
}

func (s *Store) fetchEdges(ctx context.Context, workspaceID, memoryID, side string, edgeTypes []string, k int) ([]types.Edge, error) {
	col := "source_memory_id"
	if side == "target" {
		col = "target_memory_id"
	}
	q := fmt.Sprintf(`
SELECT edge_id, workspace_id, source_memory_id, target_memory_id, edge_type, properties_json,
       created_by_agent_id, created_at, deleted_at, watermark
FROM memora_edges
WHERE workspace_id = ? AND %s = ? AND deleted_at IS NULL`, col)
	args := []any{workspaceID, memoryID}
	if len(edgeTypes) > 0 {
		q += " AND edge_type IN (" + placeholders(len(edgeTypes)) + ")"
		for _, t := range edgeTypes {
			args = append(args, t)
		}
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, k)
	return s.scanEdges(ctx, q, args)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := strings.Repeat("?,", n)
	return out[:len(out)-1]
}

func (s *Store) scanEdges(ctx context.Context, q string, args []any) ([]types.Edge, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Edge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEdge(rows *sql.Rows) (types.Edge, error) {
	var e types.Edge
	var props, deletedAt sql.NullString
	var createdAt sqliteTime
	var etype string
	if err := rows.Scan(&e.EdgeID, &e.WorkspaceID, &e.SourceMemoryID, &e.TargetMemoryID, &etype, &props,
		&e.CreatedByAgentID, &createdAt, &deletedAt, &e.Watermark); err != nil {
		return e, err
	}
	e.EdgeType = types.EdgeType(etype)
	e.CreatedAt = createdAt.Time
	e.DeletedAt = nullableTime(deletedAt)
	if props.Valid && props.String != "" {
		_ = json.Unmarshal([]byte(props.String), &e.PropertiesJSON)
	}
	return e, nil
}

func (s *Store) memoryHeaders(ctx context.Context, ids []string) ([]types.MemoryHeader, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := "SELECT id, workspace_id, collection_id, content_md5, head_watermark, tags_json FROM memora_memories WHERE id IN (" + placeholders(len(ids)) + ")"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.MemoryHeader
	for rows.Next() {
		var h types.MemoryHeader
		var coll, tags sql.NullString
		if err := rows.Scan(&h.MemoryID, &h.WorkspaceID, &coll, &h.ContentMD5, &h.HeadWatermark, &tags); err != nil {
			return nil, err
		}
		h.CollectionID = coll.String
		if tags.Valid && tags.String != "" {
			_ = json.Unmarshal([]byte(tags.String), &h.Tags)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GraphTraverse runs BFS from a seed Memory. Cap at depth 3 in OSS.
func (s *Store) GraphTraverse(ctx context.Context, workspaceID, seedMemoryID string, opts adapter.TraverseOpts) (adapter.TraverseResult, error) {
	if opts.Depth <= 0 {
		opts.Depth = 1
	}
	if opts.Depth > 3 {
		return adapter.TraverseResult{}, fmt.Errorf("%w: requested depth %d > OSS cap 3", types.ErrDepthExceeded, opts.Depth)
	}
	if opts.MaxEdges <= 0 {
		opts.MaxEdges = 10000
	}
	if opts.Budget <= 0 {
		opts.Budget = 5 * time.Second
	}
	dir := opts.Direction
	if dir == "" {
		dir = api.GraphDirOut
	}

	seedHeaders, err := s.memoryHeaders(ctx, []string{seedMemoryID})
	if err != nil || len(seedHeaders) == 0 {
		return adapter.TraverseResult{}, types.ErrNotFound
	}
	res := adapter.TraverseResult{Seed: seedHeaders[0]}

	visited := map[string]int{seedMemoryID: 0} // memory_id -> first-seen layer
	frontier := []string{seedMemoryID}
	stats := api.TraverseStats{}
	deadline := time.Now().Add(opts.Budget)

	for layerIdx := 1; layerIdx <= opts.Depth; layerIdx++ {
		if len(frontier) == 0 {
			break
		}
		var nextFrontier []string
		var layer []adapter.TraverseLayer
		for _, src := range frontier {
			if time.Now().After(deadline) || stats.EdgesWalked >= opts.MaxEdges {
				stats.Truncated = true
				break
			}
			edges, _, err := s.GraphNeighbors(ctx, workspaceID, src, adapter.NeighborsOpts{
				Direction: dir,
				EdgeTypes: opts.EdgeTypes,
				K:         200,
			})
			if err != nil {
				return adapter.TraverseResult{}, err
			}
			for _, e := range edges {
				stats.EdgesWalked++
				next := e.TargetMemoryID
				if dir == api.GraphDirIn || (dir == api.GraphDirBoth && e.TargetMemoryID == src) {
					next = e.SourceMemoryID
				}
				if _, seen := visited[next]; seen {
					continue
				}
				visited[next] = layerIdx
				// Apply optional Memory filter (tags / agent_id).
				if !s.passesTraverseFilter(ctx, next, opts.Filter) {
					nextFrontier = append(nextFrontier, next)
					continue
				}
				headers, _ := s.memoryHeaders(ctx, []string{next})
				if len(headers) > 0 {
					layer = append(layer, adapter.TraverseLayer{
						Memory:      headers[0],
						ViaEdgeID:   e.EdgeID,
						ViaEdgeType: string(e.EdgeType),
						Layer:       layerIdx,
					})
					stats.NodesVisited++
				}
				nextFrontier = append(nextFrontier, next)
			}
		}
		if len(layer) > 0 {
			res.Layers = append(res.Layers, layer)
		}
		frontier = nextFrontier
		if stats.Truncated {
			break
		}
	}
	res.Stats = stats
	return res, nil
}

func (s *Store) passesTraverseFilter(ctx context.Context, memoryID string, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	m, err := s.GetMemory(ctx, memoryID)
	if err != nil {
		return false
	}
	if v, ok := filter["agent_id"].(string); ok && v != "" && m.WrittenByAgentID != v {
		return false
	}
	if tagsAny, ok := filter["tags"].(map[string]any); ok {
		for k, v := range tagsAny {
			want, _ := v.(string)
			if got := m.Tags[k]; got != want {
				return false
			}
		}
	}
	return true
}

// GraphStats summarizes graph cardinality.
func (s *Store) GraphStats(ctx context.Context, workspaceID string) (int, map[string]int, error) {
	var nodeCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memora_memories WHERE workspace_id = ? AND deleted_at IS NULL", workspaceID).Scan(&nodeCount); err != nil {
		return 0, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT edge_type, COUNT(*) FROM memora_edges WHERE workspace_id = ? AND deleted_at IS NULL GROUP BY edge_type`, workspaceID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	byType := map[string]int{}
	for rows.Next() {
		var et string
		var n int
		if err := rows.Scan(&et, &n); err != nil {
			return 0, nil, err
		}
		byType[et] = n
	}
	return nodeCount, byType, rows.Err()
}
