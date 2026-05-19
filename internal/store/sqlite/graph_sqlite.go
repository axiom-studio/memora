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

func init() {
	adapter.RegisterGraph("sqlite_graph", func() adapter.GraphStore { return &GraphStore{} })
}

// GraphStore implements adapter.GraphStore against SQLite, reusing
// the memora_edges table created by the MetadataStore migrations.
type GraphStore struct {
	db *sql.DB
}

func (g *GraphStore) Open(_ context.Context, cfg adapter.GraphConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite_graph: DSN required")
	}
	db, err := openSQLiteDB(cfg.DSN)
	if err != nil {
		return err
	}
	g.db = db
	return nil
}

func (g *GraphStore) Close() error {
	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

func (g *GraphStore) Ping(ctx context.Context) error {
	return g.db.PingContext(ctx)
}

func (g *GraphStore) Capabilities() adapter.GraphCapabilities {
	return adapter.GraphCapabilities{
		MaxDepth:              3,
		MaxNeighborsK:         200,
		MaxLinkBatchSize:      1000,
		SupportsBudgetedTraversal: true,
		SupportsCypher:        false,
		NativeBFS:             false,
	}
}

func (g *GraphStore) Link(ctx context.Context, e types.Edge) (types.Edge, error) {
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
	if err := g.assertEndpointsInWorkspace(ctx, e.WorkspaceID, e.SourceMemoryID, e.TargetMemoryID); err != nil {
		return types.Edge{}, err
	}
	var outDeg int
	if err := g.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memora_edges WHERE workspace_id = ? AND source_memory_id = ? AND deleted_at IS NULL`,
		e.WorkspaceID, e.SourceMemoryID).Scan(&outDeg); err != nil {
		return types.Edge{}, err
	}
	if outDeg >= 1000 {
		return types.Edge{}, fmt.Errorf("%w: out-degree limit (1000) reached for memory %s", types.ErrQuotaExceeded, e.SourceMemoryID)
	}
	if e.EdgeType == types.EdgeTypeParentOf {
		var seen int
		err := g.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memora_edges_view
WHERE workspace_id = ? AND source_memory_id = ? AND target_memory_id = ? AND edge_type = 'parent_of'
  AND edge_id LIKE 'edg_synthetic_%'`,
			e.WorkspaceID, e.SourceMemoryID, e.TargetMemoryID).Scan(&seen)
		if err == nil && seen > 0 {
			return types.Edge{}, types.ErrAlreadyLinked
		}
	}
	propsJSON, _ := json.Marshal(e.PropertiesJSON)
	_, err := g.db.ExecContext(ctx, `
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
	return e, nil
}

func (g *GraphStore) assertEndpointsInWorkspace(ctx context.Context, wsID, src, tgt string) error {
	var srcWS, tgtWS string
	err := g.db.QueryRowContext(ctx, "SELECT workspace_id FROM memora_memories WHERE id = ?", src).Scan(&srcWS)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: source memory %s", types.ErrNotFound, src)
	}
	if err != nil {
		return err
	}
	err = g.db.QueryRowContext(ctx, "SELECT workspace_id FROM memora_memories WHERE id = ?", tgt).Scan(&tgtWS)
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

func (g *GraphStore) Unlink(ctx context.Context, edgeID, agentID string) error {
	if strings.HasPrefix(edgeID, "edg_synthetic_") {
		return types.ErrSyntheticEdge
	}
	res, err := g.db.ExecContext(ctx, `
UPDATE memora_edges SET deleted_at = ? WHERE edge_id = ? AND deleted_at IS NULL`,
		time.Now().UTC(), edgeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	return nil
}

func (g *GraphStore) LinkBatch(ctx context.Context, edges []types.Edge) ([]adapter.LinkResult, error) {
	if len(edges) > 1000 {
		return nil, fmt.Errorf("%w: LinkBatch capped at 1000 edges (got %d)", types.ErrQuotaExceeded, len(edges))
	}
	results := make([]adapter.LinkResult, len(edges))
	for i := range edges {
		e, err := g.Link(ctx, edges[i])
		if err != nil {
			results[i] = adapter.LinkResult{Index: i, Status: "error", Error: err}
			continue
		}
		results[i] = adapter.LinkResult{Index: i, Status: "ok", EdgeID: e.EdgeID}
	}
	return results, nil
}

func (g *GraphStore) CascadeForget(ctx context.Context, memoryID, agentID string) (int, error) {
	now := time.Now().UTC()
	res, err := g.db.ExecContext(ctx, `
UPDATE memora_edges SET deleted_at = ?
WHERE deleted_at IS NULL AND (source_memory_id = ? OR target_memory_id = ?)`, now, memoryID, memoryID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (g *GraphStore) Neighbors(ctx context.Context, workspaceID, memoryID string, opts adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
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
		es, err := g.fetchEdges(ctx, workspaceID, memoryID, "source", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, es...)
	case api.GraphDirIn:
		es, err := g.fetchEdges(ctx, workspaceID, memoryID, "target", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, es...)
	case api.GraphDirBoth:
		out, err := g.fetchEdges(ctx, workspaceID, memoryID, "source", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		in, err := g.fetchEdges(ctx, workspaceID, memoryID, "target", opts.EdgeTypes, opts.K)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, out...)
		edges = append(edges, in...)
	}

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
	headers, err := g.memoryHeaders(ctx, neighborIDs)
	return edges, headers, err
}

func (g *GraphStore) fetchEdges(ctx context.Context, workspaceID, memoryID, side string, edgeTypes []string, k int) ([]types.Edge, error) {
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
	return g.scanEdges(ctx, q, args)
}

func (g *GraphStore) scanEdges(ctx context.Context, q string, args []any) ([]types.Edge, error) {
	rows, err := g.db.QueryContext(ctx, q, args...)
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

func (g *GraphStore) memoryHeaders(ctx context.Context, ids []string) ([]types.MemoryHeader, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := "SELECT id, workspace_id, collection_id, content_md5, head_watermark, tags_json FROM memora_memories WHERE id IN (" + placeholders(len(ids)) + ")"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := g.db.QueryContext(ctx, q, args...)
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

func (g *GraphStore) Traverse(ctx context.Context, workspaceID, seedMemoryID string, opts adapter.TraverseOpts) (adapter.TraverseResult, error) {
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

	seedHeaders, err := g.memoryHeaders(ctx, []string{seedMemoryID})
	if err != nil || len(seedHeaders) == 0 {
		return adapter.TraverseResult{}, types.ErrNotFound
	}
	res := adapter.TraverseResult{Seed: seedHeaders[0]}

	visited := map[string]int{seedMemoryID: 0}
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
			edges, _, err := g.Neighbors(ctx, workspaceID, src, adapter.NeighborsOpts{
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
				if !g.passesTraverseFilter(ctx, next, opts.Filter) {
					nextFrontier = append(nextFrontier, next)
					continue
				}
				headers, _ := g.memoryHeaders(ctx, []string{next})
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

func (g *GraphStore) passesTraverseFilter(ctx context.Context, memoryID string, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	var agentID string
	var tagsJSON sql.NullString
	err := g.db.QueryRowContext(ctx, "SELECT written_by_agent_id, tags_json FROM memora_memories WHERE id = ?", memoryID).Scan(&agentID, &tagsJSON)
	if err != nil {
		return false
	}
	if v, ok := filter["agent_id"].(string); ok && v != "" && agentID != v {
		return false
	}
	if tagsAny, ok := filter["tags"].(map[string]any); ok {
		tags := map[string]string{}
		if tagsJSON.Valid && tagsJSON.String != "" {
			_ = json.Unmarshal([]byte(tagsJSON.String), &tags)
		}
		for k, v := range tagsAny {
			want, _ := v.(string)
			if tags[k] != want {
				return false
			}
		}
	}
	return true
}

func (g *GraphStore) Stats(ctx context.Context, workspaceID string) (int, map[string]int, error) {
	var nodeCount int
	if err := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memora_memories WHERE workspace_id = ? AND deleted_at IS NULL", workspaceID).Scan(&nodeCount); err != nil {
		return 0, nil, err
	}
	rows, err := g.db.QueryContext(ctx, `
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
