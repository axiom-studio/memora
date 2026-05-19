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

	// CreateWorkspace persists a new Workspace; w.ID is populated on return. Returns ErrAlreadyExists on name collision.
	CreateWorkspace(ctx context.Context, w *types.Workspace) error
	// GetWorkspace returns the Workspace by ID. Returns ErrNotFound if absent.
	GetWorkspace(ctx context.Context, id string) (*types.Workspace, error)
	// ListWorkspaces returns up to limit workspaces ordered by creation time.
	ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error)
	// UpdateWorkspace replaces the mutable fields of an existing Workspace. Returns ErrNotFound if absent.
	UpdateWorkspace(ctx context.Context, w *types.Workspace) error
	// DeleteWorkspace removes a Workspace and cascades to child collections and memories. Returns ErrNotFound if absent.
	DeleteWorkspace(ctx context.Context, id string) error

	// CreateCollection persists a new Collection under the given workspace. c.ID is populated on return.
	CreateCollection(ctx context.Context, c *types.Collection) error
	// GetCollection returns the Collection by ID. Returns ErrNotFound if absent.
	GetCollection(ctx context.Context, id string) (*types.Collection, error)
	// ListCollections returns all collections belonging to a workspace.
	ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error)
	// DeleteCollection removes a Collection. Returns ErrNotFound if absent.
	DeleteCollection(ctx context.Context, id string) error

	// ImprintMemory creates a new Memory, assigns an ID and initial watermark. Side-effect: appends a watermark history entry.
	ImprintMemory(ctx context.Context, m *types.Memory) (watermark string, err error)
	// GetMemory returns the head revision of a Memory. Returns ErrNotFound if absent or forgotten.
	GetMemory(ctx context.Context, id string) (*types.Memory, error)
	// GetMemoryAtWatermark returns the Memory as it existed at a specific watermark. Returns ErrNotFound if the watermark is unknown.
	GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error)
	// ListMemories returns up to limit memories in a workspace, optionally filtered by collectionID (empty = all).
	ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error)
	// UpdateMemory performs a CAS update: succeeds only if the current watermark matches expectedWatermark. Returns ErrCAS on mismatch.
	UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (newWatermark string, err error)
	// AppendMemory appends content to an existing Memory under CAS. Returns ErrCAS on watermark mismatch.
	AppendMemory(ctx context.Context, id, expectedWatermark string, body string, agentID string) (newWatermark string, contentMD5 string, err error)
	// PatchMemory applies structured patch ops under CAS. Returns ErrCAS on mismatch, ErrPatchAnchor if an anchor is not found.
	PatchMemory(ctx context.Context, id, expectedWatermark string, ops []api.PatchOp, agentID string) (newWatermark string, deltas []types.CellDelta, newContent string, err error)
	// ForgetMemory soft-deletes a Memory. Does not cascade edges (caller must call GraphCascadeForget separately).
	ForgetMemory(ctx context.Context, id string) error

	// UpsertCells inserts or replaces cells for a memory. Keyed by cell_id.
	UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error
	// GetCells returns all cells for a memory, ordered by seq.
	GetCells(ctx context.Context, memoryID string) ([]types.Cell, error)
	// UpdateCellVectorKey sets the vector_key and embedding_model on a cell after embedding completes.
	UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error

	// FlipRecallReadyIfAllEmbedded atomically sets recall_ready=1 on the
	// memory iff every cell of that memory has a non-empty vector_key.
	// The flipped return value reports whether this call mutated the row
	// (false means either some cells still need embedding, or the row was
	// already at recall_ready=1). Implementations MUST do the check and
	// flip in a single statement so concurrent embed workers can't race
	// against each other.
	FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (flipped bool, err error)

	// GetWatermarkHistory returns watermark entries for a target since the given time. OSS retains 7 days. The workspaceID scopes the query for tenant isolation.
	GetWatermarkHistory(ctx context.Context, workspaceID, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error)
	// AppendWatermarkHistory records a new watermark transition for auditing.
	AppendWatermarkHistory(ctx context.Context, entry types.WatermarkHistoryEntry) error

	// UpsertTag sets a key-value tag on a memory. Creates or overwrites. The workspaceID scopes the mutation for tenant isolation.
	UpsertTag(ctx context.Context, workspaceID, memoryID, key, value string) error
	// DeleteTag removes a tag by key. No-op if not present. The workspaceID scopes the mutation for tenant isolation.
	DeleteTag(ctx context.Context, workspaceID, memoryID, key string) error

	// RegisterAgent upserts an Agent by agent_id (idempotent). Used by identity middleware on first write.
	RegisterAgent(ctx context.Context, a *types.Agent) error
	// GetAgent returns the Agent by ID within a workspace. Returns ErrNotFound if absent or belongs to a different workspace.
	GetAgent(ctx context.Context, workspaceID, id string) (*types.Agent, error)
	// ListAgents returns all agents registered in a workspace.
	ListAgents(ctx context.Context, workspaceID string) ([]types.Agent, error)
	// DeactivateAgent soft-deletes an agent within a workspace. Returns ErrNotFound if absent or belongs to a different workspace.
	DeactivateAgent(ctx context.Context, workspaceID, id string) error

	// GraphLink creates a typed directed edge. Returns ErrAlreadyLinked if a non-deleted edge already connects the pair with the same type.
	GraphLink(ctx context.Context, edge types.Edge) (types.Edge, error)
	// GraphUnlink soft-deletes an edge. Returns ErrNotFound if absent. Returns ErrSyntheticEdge for system-generated edges.
	GraphUnlink(ctx context.Context, edgeID, agentID string) error
	// GraphLinkBatch creates up to MaxLinkBatchSize edges. Per-edge errors are returned in LinkResult; the call itself only errors on systemic failure.
	GraphLinkBatch(ctx context.Context, edges []types.Edge) ([]LinkResult, error)
	// GraphCascadeForget removes all edges incident to a memory (GDPR Art. 17 cascade). Returns the count of deleted edges.
	GraphCascadeForget(ctx context.Context, memoryID, agentID string) (cascadedCount int, err error)

	// GraphNeighbors returns one-hop edges and neighbor headers for a memory, filtered by direction and edge types.
	GraphNeighbors(ctx context.Context, workspaceID, memoryID string, opts NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error)
	// GraphTraverse runs a BFS walk from a seed memory up to opts.Depth hops. Safety budgets (MaxEdges, Budget) prevent runaway scans.
	GraphTraverse(ctx context.Context, workspaceID, seedMemoryID string, opts TraverseOpts) (TraverseResult, error)
	// GraphStats returns aggregate graph cardinality for a workspace.
	GraphStats(ctx context.Context, workspaceID string) (nodeCount int, edgeCountByType map[string]int, err error)
}

// PrimaryFactory builds a PrimaryStore from config. Implementations
// register themselves at boot time.
type PrimaryFactory func() PrimaryStore
