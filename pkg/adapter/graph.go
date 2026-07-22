package adapter

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// GraphConfig is the driver-agnostic input to GraphStore.Open.
type GraphConfig struct {
	Driver string         `yaml:"driver" json:"driver"`
	DSN    string         `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// GraphCapabilities advertises what a GraphStore can do.
type GraphCapabilities struct {
	MaxDepth                  int  `json:"max_depth"`
	MaxNeighborsK             int  `json:"max_neighbors_k"`
	MaxLinkBatchSize          int  `json:"max_link_batch_size"`
	SupportsBudgetedTraversal bool `json:"supports_budgeted_traversal"`
	SupportsCypher            bool `json:"supports_cypher"`
	NativeBFS                 bool `json:"native_bfs"`
}

// GraphStore is the Context Graph persistence contract — edges,
// traversal, and graph statistics. Extracted from MetadataStore to
// enable dedicated graph backends (Neo4j, Kuzu, Apache AGE) for
// deployments with high fan-out or deep traversals.
type GraphStore interface {
	Open(ctx context.Context, cfg GraphConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() GraphCapabilities

	// Link creates a typed directed edge. Returns ErrAlreadyLinked if
	// a non-deleted edge already connects the pair with the same type.
	Link(ctx context.Context, edge types.Edge) (types.Edge, error)
	// Unlink soft-deletes an edge. Returns ErrNotFound if absent.
	// Returns ErrSyntheticEdge for system-generated edges.
	Unlink(ctx context.Context, edgeID, agentID string) error
	// LinkBatch creates up to MaxLinkBatchSize edges. Per-edge errors
	// are returned in LinkResult; the call itself only errors on
	// systemic failure.
	LinkBatch(ctx context.Context, edges []types.Edge) ([]LinkResult, error)
	// CascadeForget soft-deletes every edge incident to a memory
	// (GDPR Art. 17 cascade). Returns the count of deleted edges.
	CascadeForget(ctx context.Context, memoryID, agentID string) (cascadedCount int, err error)

	// Neighbors returns one-hop edges and neighbor headers for a
	// memory, filtered by direction and edge types.
	Neighbors(ctx context.Context, workspaceID, memoryID string, opts NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error)
	// Traverse runs a BFS walk from a seed memory up to opts.Depth
	// hops. Safety budgets (MaxEdges, Budget) prevent runaway scans.
	Traverse(ctx context.Context, workspaceID, seedMemoryID string, opts TraverseOpts) (TraverseResult, error)
	// Stats returns aggregate graph cardinality for a workspace.
	Stats(ctx context.Context, workspaceID string) (nodeCount int, edgeCountByType map[string]int, err error)
}

// GraphFactory builds a GraphStore from config.
type GraphFactory func() GraphStore

// NeighborsOpts is the options struct for GraphStore.Neighbors.
type NeighborsOpts struct {
	Direction api.GraphDirection
	EdgeTypes []string
	K         int
}

// TraverseOpts is the options struct for GraphStore.Traverse.
type TraverseOpts struct {
	Depth     int
	Direction api.GraphDirection
	EdgeTypes []string
	Filter    map[string]any
	MaxEdges  int           // safety budget; 0 = adapter default
	Budget    time.Duration // wall-clock safety budget
}

// TraverseLayer is one BFS layer.
type TraverseLayer struct {
	Memory      types.MemoryHeader
	ViaEdgeID   string
	ViaEdgeType string
	Layer       int
}

// TraverseResult is the output of a BFS walk.
type TraverseResult struct {
	Seed   types.MemoryHeader
	Layers [][]TraverseLayer
	Stats  api.TraverseStats
}

// LinkResult is the per-edge outcome inside a LinkBatch response.
type LinkResult struct {
	Index  int
	Status string // "ok" | "error"
	EdgeID string
	Error  error
}
