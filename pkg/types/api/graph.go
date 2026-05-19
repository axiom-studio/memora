package api

import "github.com/axiom-studio/memora/pkg/types"

// LinkRequest creates a typed, directed edge from the path memory to
// the target memory. Source is taken from the URL.
type LinkRequest struct {
	TargetMemoryID string         `json:"target_memory_id"`
	EdgeType       string         `json:"edge_type"`
	Properties     map[string]any `json:"properties_json,omitempty"`
}

// LinkResponse echoes the persisted Edge.
type LinkResponse struct {
	Edge     types.Edge `json:"edge"`
	LedgerID string     `json:"ledger_id"`
}

// LinkBatchEntry is one row in a LinkBatch payload.
type LinkBatchEntry struct {
	SourceMemoryID string         `json:"source_memory_id"`
	TargetMemoryID string         `json:"target_memory_id"`
	EdgeType       string         `json:"edge_type"`
	Properties     map[string]any `json:"properties_json,omitempty"`
}

// LinkBatchRequest creates up to 1000 edges atomically-per-edge.
type LinkBatchRequest struct {
	Edges []LinkBatchEntry `json:"edges"`
}

// LinkResult is the per-edge outcome inside a LinkBatchResponse.
type LinkResult struct {
	Index   int    `json:"index"`
	Status  string `json:"status"` // "ok" | "error"
	EdgeID  string `json:"edge_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// LinkBatchResponse summarizes a LinkBatch call.
type LinkBatchResponse struct {
	Results []LinkResult `json:"results"`
	OK      int          `json:"ok"`
	Failed  int          `json:"failed"`
}

// NeighborsRequest returns one-hop neighbors.
type NeighborsRequest struct {
	MemoryID  string         `json:"memory_id"`
	Direction GraphDirection `json:"direction,omitempty"`
	EdgeTypes []string       `json:"edge_types,omitempty"`
	K         int            `json:"k,omitempty"`
}

// NeighborsResponse pairs edges with the neighbor Memory headers.
type NeighborsResponse struct {
	Edges     []types.Edge         `json:"edges"`
	Neighbors []types.MemoryHeader `json:"neighbors"`
}

// TraverseRequest runs BFS from a seed Memory.
type TraverseRequest struct {
	SeedMemoryID string         `json:"seed_memory_id"`
	Depth        int            `json:"depth"`
	Direction    GraphDirection `json:"direction,omitempty"`
	EdgeTypes    []string       `json:"edge_types,omitempty"`
	Filter       map[string]any `json:"filter,omitempty"`
}

// TraverseHit is one BFS layer entry.
type TraverseHit struct {
	MemoryID   string `json:"memory_id"`
	ViaEdgeID  string `json:"via_edge_id"`
	ViaEdgeType string `json:"via_edge_type"`
}

// TraverseStats describe a BFS run.
type TraverseStats struct {
	NodesVisited int  `json:"nodes_visited"`
	EdgesWalked  int  `json:"edges_walked"`
	Truncated    bool `json:"truncated"`
}

// TraverseResponse is the layered BFS output.
type TraverseResponse struct {
	Seed   types.MemoryHeader `json:"seed"`
	Layers [][]TraverseHit    `json:"layers"`
	Stats  TraverseStats      `json:"stats"`
}

// GraphStatsResponse summarizes graph cardinality.
type GraphStatsResponse struct {
	NodeCount       int            `json:"node_count"`
	EdgeCountByType map[string]int `json:"edge_count_by_type"`
}
