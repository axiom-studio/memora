// Package adapter defines the three Memora pluggable persistence
// contracts — PrimaryStore (tabular), VectorStore (ANN index), and
// LedgerStore (append-only audit log) — plus the IdentityProvider
// contract and the boot-time driver registry.
//
// Every concrete backend (SQLite, Postgres, sqlite-vec, pgvector,
// file-based ledger, S3 Vectors in Cloud, etc.) is a package that
// imports this one and calls RegisterPrimary / RegisterVector /
// RegisterLedger / RegisterIdentity in its init().
package adapter

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// PrimaryConfig is the driver-agnostic input to PrimaryStore.Open.
// Driver-specific fields go in Extra (a YAML/JSON blob).
type PrimaryConfig struct {
	Driver string         `yaml:"driver" json:"driver"`
	DSN    string         `yaml:"dsn" json:"dsn"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// PrimaryCapabilities tells the server what an adapter supports.
type PrimaryCapabilities struct {
	SupportsCAS              bool `json:"supports_cas"`
	SupportsTransactions     bool `json:"supports_transactions"`
	SupportsBatchUpsert      bool `json:"supports_batch_upsert"`
	SupportsLogicalReplication bool `json:"supports_logical_replication"`
	RecommendedMaxSizeGB     int  `json:"recommended_max_size_gb"`
	MaxGraphDepth            int  `json:"max_graph_depth"`            // OSS caps at 3
	MaxNeighborsK            int  `json:"max_neighbors_k"`            // OSS caps at 200
	MaxLinkBatchSize         int  `json:"max_link_batch_size"`        // OSS caps at 1000
}

// NeighborsOpts is the options struct for PrimaryStore.GraphNeighbors.
type NeighborsOpts struct {
	Direction api.GraphDirection
	EdgeTypes []string
	K         int
}

// TraverseOpts is the options struct for PrimaryStore.GraphTraverse.
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
	Index   int
	Status  string // "ok" | "error"
	EdgeID  string
	Error   error
}

// PrimaryStore is the tabular persistence contract — Memories, Cells,
// Workspaces, Collections, Tags, Watermark history, the agent
// registry, and the Context Graph edge table.
type PrimaryStore interface {
	Open(ctx context.Context, cfg PrimaryConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() PrimaryCapabilities

	// Workspace CRUD.
	CreateWorkspace(ctx context.Context, w *types.Workspace) error
	GetWorkspace(ctx context.Context, id string) (*types.Workspace, error)
	ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error)
	UpdateWorkspace(ctx context.Context, w *types.Workspace) error
	DeleteWorkspace(ctx context.Context, id string) error

	// Collection CRUD.
	CreateCollection(ctx context.Context, c *types.Collection) error
	GetCollection(ctx context.Context, id string) (*types.Collection, error)
	ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error)
	DeleteCollection(ctx context.Context, id string) error

	// Memory CRUD with CAS.
	ImprintMemory(ctx context.Context, m *types.Memory) (watermark string, err error)
	GetMemory(ctx context.Context, id string) (*types.Memory, error)
	GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error)
	ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error)
	UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (newWatermark string, err error)
	AppendMemory(ctx context.Context, id, expectedWatermark string, body string, agentID string) (newWatermark string, contentMD5 string, err error)
	PatchMemory(ctx context.Context, id, expectedWatermark string, ops []api.PatchOp, agentID string) (newWatermark string, deltas []types.CellDelta, newContent string, err error)
	ForgetMemory(ctx context.Context, id string) error

	// Cell operations.
	UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error
	GetCells(ctx context.Context, memoryID string) ([]types.Cell, error)
	UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error

	// FlipRecallReadyIfAllEmbedded atomically sets recall_ready=1 on the
	// memory iff every cell of that memory has a non-empty vector_key.
	// The flipped return value reports whether this call mutated the row
	// (false means either some cells still need embedding, or the row was
	// already at recall_ready=1). Implementations MUST do the check and
	// flip in a single statement so concurrent embed workers can't race
	// against each other.
	FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (flipped bool, err error)

	// Watermark history (OSS retention 7d).
	GetWatermarkHistory(ctx context.Context, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error)
	AppendWatermarkHistory(ctx context.Context, entry types.WatermarkHistoryEntry) error

	// Tag CRUD.
	UpsertTag(ctx context.Context, memoryID, key, value string) error
	DeleteTag(ctx context.Context, memoryID, key string) error

	// Agent registry.
	RegisterAgent(ctx context.Context, a *types.Agent) error
	GetAgent(ctx context.Context, id string) (*types.Agent, error)
	ListAgents(ctx context.Context, workspaceID string) ([]types.Agent, error)
	DeactivateAgent(ctx context.Context, id string) error

	// Context Graph — Edge CRUD.
	GraphLink(ctx context.Context, edge types.Edge) (types.Edge, error)
	GraphUnlink(ctx context.Context, edgeID, agentID string) error
	GraphLinkBatch(ctx context.Context, edges []types.Edge) ([]LinkResult, error)
	GraphCascadeForget(ctx context.Context, memoryID, agentID string) (cascadedCount int, err error)

	// Context Graph — Traversal.
	GraphNeighbors(ctx context.Context, workspaceID, memoryID string, opts NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error)
	GraphTraverse(ctx context.Context, workspaceID, seedMemoryID string, opts TraverseOpts) (TraverseResult, error)
	GraphStats(ctx context.Context, workspaceID string) (nodeCount int, edgeCountByType map[string]int, err error)
}

// PrimaryFactory builds a PrimaryStore from config. Implementations
// register themselves at boot time.
type PrimaryFactory func() PrimaryStore
