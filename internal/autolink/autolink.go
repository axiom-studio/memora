package autolink

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Deps bundles the adapters the auto-link engine needs.
type Deps struct {
	Metadata adapter.MetadataStore
	Vector   adapter.VectorStore
	Graph   adapter.GraphStore
	Ledger  adapter.LedgerStore
	Logger  *slog.Logger
}

// candidate is one deduplicated memory-level hit with provenance.
type candidate struct {
	MemoryID     string
	Score        float64
	SourceCellID string
	TargetCellID string
}

// AutoLink finds vector-similar existing memories and creates
// vector_neighbor edges from newMemory to each neighbor.
// embeddings[i] corresponds to cells[i].
// embeddingModel is recorded in edge provenance.
func AutoLink(
	ctx context.Context,
	deps Deps,
	workspaceID string,
	newMemory *types.Memory,
	cells []types.Cell,
	embeddings [][]float32,
	embeddingModel string,
) ([]types.Edge, error) {
	ws, err := deps.Metadata.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !ws.AutoLinkEnabled {
		return nil, nil
	}

	ws.AutoLinkDefaults()
	maxEdges := ws.AutoLinkMaxEdges
	threshold := ws.AutoLinkThreshold

	// Collect the best vector neighbors across all cells, deduped at memory level.
	best := make(map[string]*candidate) // target memory ID → best candidate
	for i, emb := range embeddings {
		if len(emb) == 0 {
			continue
		}
		queryCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		hits, err := deps.Vector.Query(queryCtx, adapter.VectorQuery{
			WorkspaceID: workspaceID,
			Embedding:   emb,
			K:           maxEdges,
		})
		cancel()
		if err != nil {
			ledgerSkip(ctx, deps, workspaceID, newMemory.ID, err)
			return nil, nil
		}

		cellID := ""
		if i < len(cells) {
			cellID = cells[i].CellID
		}
		for _, hit := range hits {
			if hit.Key.MemoryID == newMemory.ID {
				continue
			}
			if hit.Score < threshold {
				continue
			}
			if existing, ok := best[hit.Key.MemoryID]; !ok || hit.Score > existing.Score {
				best[hit.Key.MemoryID] = &candidate{
					MemoryID:     hit.Key.MemoryID,
					Score:        hit.Score,
					SourceCellID: cellID,
					TargetCellID: hit.Key.CellID,
				}
			}
		}
	}

	if len(best) == 0 {
		return nil, nil
	}

	// Sort by score descending, take top maxEdges.
	candidates := make([]*candidate, 0, len(best))
	for _, c := range best {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	if len(candidates) > maxEdges {
		candidates = candidates[:maxEdges]
	}

	// Per-target incoming throttle: skip targets that already received
	// too many auto-link edges in the last 24h.
	maxIncoming := ws.AutoLinkMaxIncomingPerDay
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	throttled := candidates[:0]
	for _, c := range candidates {
		edges, _, err := deps.Graph.Neighbors(ctx, workspaceID, c.MemoryID, adapter.NeighborsOpts{
			Direction: api.GraphDirIn,
			EdgeTypes: []string{string(types.EdgeTypeVectorNeighbor)},
			K:         maxIncoming + 1,
		})
		if err != nil {
			throttled = append(throttled, c)
			continue
		}
		recent := 0
		for _, e := range edges {
			if !e.CreatedAt.Before(cutoff) {
				recent++
			}
		}
		if recent >= maxIncoming {
			deps.Logger.Info("auto_link_throttled",
				"workspace", workspaceID, "source", newMemory.ID,
				"target", c.MemoryID, "recent_incoming", recent, "limit", maxIncoming)
			continue
		}
		throttled = append(throttled, c)
	}
	candidates = throttled

	if len(candidates) == 0 {
		return nil, nil
	}

	// Build edges.
	edges := make([]types.Edge, 0, len(candidates))
	for _, c := range candidates {
		edges = append(edges, types.Edge{
			EdgeID:         types.NewID(types.EdgeIDPrefix),
			WorkspaceID:    workspaceID,
			SourceMemoryID: newMemory.ID,
			TargetMemoryID: c.MemoryID,
			EdgeType:       types.EdgeTypeVectorNeighbor,
			PropertiesJSON: map[string]any{
				"auto_source":     "vector_neighbor",
				"cosine_score":    c.Score,
				"via_cell_pair":   []string{c.SourceCellID, c.TargetCellID},
				"embedding_model": embeddingModel,
			},
			CreatedByAgentID: types.AgentSystemAutoLinkID,
			CreatedAt:        time.Now().UTC(),
		})
	}

	results, err := deps.Graph.LinkBatch(ctx, edges)
	if err != nil {
		deps.Logger.Warn("auto_link_graph_error", "workspace", workspaceID, "memory", newMemory.ID, "err", err)
		return nil, nil
	}

	// Ledger each successfully created edge.
	var created []types.Edge
	for i, r := range results {
		if r.Status != "ok" {
			continue
		}
		e := edges[i]
		e.EdgeID = r.EdgeID
		created = append(created, e)
		_ = deps.Ledger.Append(ctx, api.LedgerEntry{
			WorkspaceID: workspaceID,
			Op:          "graph_link",
			Target:      newMemory.ID,
			AgentID:     types.AgentSystemAutoLinkID,
			Timestamp:   time.Now().UTC(),
			Metadata: map[string]any{
				"trigger":           "auto_link",
				"edge_id":           r.EdgeID,
				"edge_type":         string(types.EdgeTypeVectorNeighbor),
				"source_memory_id":  e.SourceMemoryID,
				"target_memory_id":  e.TargetMemoryID,
				"cosine_score":      e.PropertiesJSON["cosine_score"],
				"via_cell_pair":     e.PropertiesJSON["via_cell_pair"],
				"embedding_model":   embeddingModel,
			},
		})
	}

	deps.Logger.Info("auto_link_done",
		"workspace", workspaceID,
		"memory", newMemory.ID,
		"edges_created", len(created),
	)
	return created, nil
}

func ledgerSkip(ctx context.Context, deps Deps, workspaceID, memoryID string, cause error) {
	deps.Logger.Warn("auto_link_skipped", "workspace", workspaceID, "memory", memoryID, "reason", cause)
	_ = deps.Ledger.Append(ctx, api.LedgerEntry{
		WorkspaceID: workspaceID,
		Op:          "auto_link_skipped",
		Target:      memoryID,
		AgentID:     types.AgentSystemAutoLinkID,
		Timestamp:   time.Now().UTC(),
		Metadata: map[string]any{
			"reason": cause.Error(),
		},
	})
}
