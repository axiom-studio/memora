package adapter

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// MetadataConfig is the driver-agnostic input to MetadataStore.Open.
type MetadataConfig struct {
	Driver string         `yaml:"driver" json:"driver"`
	DSN    string         `yaml:"dsn" json:"dsn"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// MetadataCapabilities tells the server what a MetadataStore supports.
type MetadataCapabilities struct {
	SupportsCAS                bool `json:"supports_cas"`
	SupportsTransactions       bool `json:"supports_transactions"`
	SupportsBatchUpsert        bool `json:"supports_batch_upsert"`
	SupportsLogicalReplication bool `json:"supports_logical_replication"`
	RecommendedMaxSizeGB       int  `json:"recommended_max_size_gb"`
}

// MetadataStore is the tabular persistence contract for everything
// except content bytes and graph edges. It owns Workspaces,
// Collections, Memory headers (Content="" — real bytes live in
// ContentStore), Cell headers (Text="" — real text lives in
// ContentStore), watermark history, tags, and the agent registry.
//
// Extracted from MetadataStore per doc #301 §3 to enable the
// five-adapter architecture: Metadata + Content + Vector + Graph + Ledger.
type MetadataStore interface {
	Open(ctx context.Context, cfg MetadataConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() MetadataCapabilities

	// --- Workspace CRUD ---

	CreateWorkspace(ctx context.Context, w *types.Workspace) error
	GetWorkspace(ctx context.Context, id string) (*types.Workspace, error)
	ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error)
	ListWorkspacesPaged(ctx context.Context, cursor string, limit int) ([]types.Workspace, string, error)
	UpdateWorkspace(ctx context.Context, w *types.Workspace) error
	DeleteWorkspace(ctx context.Context, id string) error

	// --- Collection CRUD ---

	CreateCollection(ctx context.Context, c *types.Collection) error
	GetCollection(ctx context.Context, id string) (*types.Collection, error)
	ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error)
	DeleteCollection(ctx context.Context, id string) error

	// --- Memory CRUD ---
	// The MetadataStore legacy column (memora_memories.content) remains
	// authoritative for content storage until the column-drop migration
	// ships (tracked for v1.0). ContentStore is a dual-write target;
	// both stores receive the same bytes on Imprint/Append/Patch.
	// Reads return whatever is in the MetadataStore DB column.

	ImprintMemory(ctx context.Context, m *types.Memory) (watermark string, err error)
	GetMemory(ctx context.Context, id string) (*types.Memory, error)
	GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error)
	ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error)
	ListMemoriesPaged(ctx context.Context, workspaceID, collectionID, cursor string, limit int) ([]types.Memory, string, error)
	UpdateMemory(ctx context.Context, workspaceID, id, expectedWatermark string, m *types.Memory) (newWatermark string, err error)
	AppendMemory(ctx context.Context, workspaceID, id, expectedWatermark string, body string, agentID string) (newWatermark string, contentMD5 string, err error)
	PatchMemory(ctx context.Context, workspaceID, id, expectedWatermark string, ops []api.PatchOp, agentID string) (newWatermark string, deltas []types.CellDelta, newContent string, err error)
	ForgetMemory(ctx context.Context, workspaceID, id string) error

	// --- Cell CRUD ---
	// The MetadataStore legacy column (memora_cells.text) remains
	// authoritative until the column-drop migration ships (v1.0).
	// ContentStore is a dual-write target for cell text.

	UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error
	GetCells(ctx context.Context, workspaceID, memoryID string) ([]types.Cell, error)
	// GetMemories loads the live memories among ids in one call, in no
	// particular order; unknown or forgotten IDs are omitted.
	GetMemories(ctx context.Context, ids []string) ([]types.Memory, error)
	// GetCellsForMemories loads the cells of every memory in memoryIDs in
	// one call, keyed by memory ID, each list in seq order.
	GetCellsForMemories(ctx context.Context, memoryIDs []string) (map[string][]types.Cell, error)
	// DeleteCellsFrom removes the Memory's cells with seq >= fromSeq, which an
	// edit that re-chunks to fewer cells no longer produces, and returns their
	// cell_ids so the caller can drop their vectors and content. A Memory in
	// another workspace returns ErrNotFound and deletes nothing.
	DeleteCellsFrom(ctx context.Context, workspaceID, memoryID string, fromSeq int) (cellIDs []string, err error)
	UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error
	FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (flipped bool, err error)

	// --- Watermark history ---

	GetWatermarkHistory(ctx context.Context, workspaceID, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error)
	AppendWatermarkHistory(ctx context.Context, entry types.WatermarkHistoryEntry) error

	// --- Tags ---

	UpsertTag(ctx context.Context, workspaceID, memoryID, key, value string) error
	DeleteTag(ctx context.Context, workspaceID, memoryID, key string) error

	// --- Agent registry ---

	RegisterAgent(ctx context.Context, a *types.Agent) error
	GetAgent(ctx context.Context, workspaceID, id string) (*types.Agent, error)
	ListAgents(ctx context.Context, workspaceID string, limit int) ([]types.Agent, error)
	DeactivateAgent(ctx context.Context, workspaceID, id string) error
	// ResolveIdentityKey returns the agent_id bound to an identity key
	// (see IdentityKeyer), binding candidateID — a fresh UUIDv4 — when
	// the key is new. Concurrent calls for one key all get the same ID.
	ResolveIdentityKey(ctx context.Context, identityKey, candidateID string) (string, error)

	// --- Full-text search ---

	// SearchKeyword ranks live memories by full-text relevance to
	// q.Query (any of its KeywordTerms), best first, at most q.K. A query
	// with no terms returns no hits.
	SearchKeyword(ctx context.Context, q KeywordQuery) ([]KeywordHit, error)

	// --- Node identity ---

	// EnsureNodeID returns this node's persisted ID, storing candidateID
	// (a fresh UUIDv4) the first time it is called.
	EnsureNodeID(ctx context.Context, candidateID string) (string, error)

	// --- Recall pins ---

	CreatePin(ctx context.Context, p *types.Pin) error
	ListPins(ctx context.Context, workspaceID string) ([]types.Pin, error)
	DeletePin(ctx context.Context, workspaceID, pinID string) error
}

// MetadataFactory builds a MetadataStore from config.
type MetadataFactory func() MetadataStore
