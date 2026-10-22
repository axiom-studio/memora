package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// VectorConfig is the driver-agnostic input to VectorStore.Open.
type VectorConfig struct {
	Driver string `yaml:"driver" json:"driver"`
	DSN    string `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Dim    int    `yaml:"dim,omitempty" json:"dim,omitempty"`
	// Model is the active embedder's model ID. Stores record it on every
	// row and compare queries only against rows of the same model and
	// dimension, so vectors from another model are never mixed in (S-7).
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Rebuild lets a store whose layout is fixed to another dimension
	// (pgvector's vector(N) column) discard its vectors and resize at
	// Open instead of refusing to start. Only `memora-core reembed` sets it.
	Rebuild bool           `yaml:"-" json:"-"`
	Extra   map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// ModelAwareVectorStore is implemented by vector stores that record the
// model and dimension of every row (S-7). Re-embedding uses it to find
// the cells still to do; /readyz reports StaleVectors.
type ModelAwareVectorStore interface {
	// StaleVectors counts rows written by another model or dimension.
	StaleVectors(ctx context.Context) (int, error)
	// FreshCells reports which of cellIDs have a vector from the active
	// model and dimension.
	FreshCells(ctx context.Context, cellIDs []string) (map[string]bool, error)
	// VectorMemories lists, in ascending order, up to limit distinct
	// memory IDs that have vectors in workspaceID, after the memory ID
	// after ("" = from the start). Re-embedding uses it to remove the
	// vectors of memories that no longer exist.
	VectorMemories(ctx context.Context, workspaceID, after string, limit int) ([]string, error)
}

// SplitVectorKeys separates DeleteVectors keys into cell IDs and memory
// keys (workspace and memory, no cell), and refuses keys that name
// neither or a memory without its workspace.
func SplitVectorKeys(keys []VectorKey) (cellIDs []string, memories []VectorKey, err error) {
	for _, k := range keys {
		switch {
		case k.CellID != "":
			cellIDs = append(cellIDs, k.CellID)
		case k.MemoryID != "" && k.WorkspaceID != "":
			memories = append(memories, k)
		default:
			return nil, nil, fmt.Errorf("%w: a vector key needs a cell_id, or a workspace_id and memory_id", types.ErrInvalidInput)
		}
	}
	return cellIDs, memories, nil
}

// VectorCapabilities advertises what a VectorStore can do.
type VectorCapabilities struct {
	SupportsExactSearch  bool `json:"supports_exact_search"`
	SupportsANN          bool `json:"supports_ann"`
	SupportsHybridFilter bool `json:"supports_hybrid_filter"`
	MaxDimensions        int  `json:"max_dimensions"`
}

// VectorPut is one entry for PutVectorsBatch.
type VectorPut struct {
	Key       VectorKey
	Embedding []float32
	Metadata  map[string]any
}

// VectorKey is a typed reference to an embedded Cell.
type VectorKey struct {
	WorkspaceID  string
	CollectionID string
	MemoryID     string
	CellID       string
}

// MemoryFilter restricts a search to memories in a collection, written
// by given agents, created in a time range (both ends inclusive) or
// carrying given tags (each key must have one of its listed values).
// Empty fields do not restrict.
type MemoryFilter struct {
	CollectionID string
	Tags         map[string][]string
	TsAfter      *time.Time
	TsBefore     *time.Time
	AgentID      string
	AgentIDIn    []string
	AgentIDNotIn []string
}

// HasMemoryFields reports whether the filter restricts anything beyond
// the collection, which every store can filter on directly.
func (f MemoryFilter) HasMemoryFields() bool {
	return len(f.Tags) > 0 || f.TsAfter != nil || f.TsBefore != nil ||
		f.AgentID != "" || len(f.AgentIDIn) > 0 || len(f.AgentIDNotIn) > 0
}

// VectorFilter is the filter of a VectorQuery. Vector stores hold no
// memory metadata, so they apply only CollectionID; the service refills
// for the rest.
type VectorFilter = MemoryFilter

// VectorQuery is the input to VectorStore.Query.
type VectorQuery struct {
	// The scope is exactly one of: WorkspaceID (one workspace),
	// WorkspaceIDs (those workspaces) or AllWorkspaces (the whole node).
	// Node-wide search is an explicit opt-in so that an empty
	// WorkspaceID can never widen a query; Scope reports a bad scope.
	WorkspaceID   string
	WorkspaceIDs  []string
	AllWorkspaces bool
	Embedding     []float32
	K             int
	Filter        VectorFilter
}

// Scope validates the query scope and returns the workspaces to search;
// nil means every workspace on the node.
func (q VectorQuery) Scope() ([]string, error) {
	return searchScope("vector", q.WorkspaceID, q.WorkspaceIDs, q.AllWorkspaces)
}

// searchScope enforces "exactly one scope" for a search query and returns
// the workspaces to search, nil meaning every workspace on the node.
func searchScope(kind, workspaceID string, workspaceIDs []string, all bool) ([]string, error) {
	set := 0
	if workspaceID != "" {
		set++
	}
	if len(workspaceIDs) > 0 {
		set++
	}
	if all {
		set++
	}
	switch {
	case set != 1:
		return nil, fmt.Errorf("%w: %s query needs exactly one scope (one workspace, a workspace list, or all workspaces)", types.ErrInvalidInput, kind)
	case workspaceID != "":
		return []string{workspaceID}, nil
	case all:
		return nil, nil
	}
	return workspaceIDs, nil
}

// VectorHit is one row of Query output.
type VectorHit struct {
	Key      VectorKey
	Score    float64 // cosine similarity; higher is better
	Metadata map[string]any
}

// VectorStore is the embedding-search persistence contract.
type VectorStore interface {
	Open(ctx context.Context, cfg VectorConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() VectorCapabilities

	// PutVector inserts or replaces a single embedding vector. Key is the primary lookup for deletion.
	PutVector(ctx context.Context, put VectorPut) error
	// PutVectorsBatch inserts or replaces multiple vectors atomically. Implementations may batch internally.
	PutVectorsBatch(ctx context.Context, puts []VectorPut) error
	// Query returns the top-K vectors nearest to q.Embedding, filtered by q.Filter. Results are ordered by descending cosine similarity.
	Query(ctx context.Context, q VectorQuery) ([]VectorHit, error)
	// DeleteVectors removes vectors by key. A key with a CellID removes
	// that cell's vector. A key with only WorkspaceID and MemoryID removes
	// every vector of that memory in that workspace (Forget). A key with
	// neither, or a memory key without a WorkspaceID, is ErrInvalidInput.
	// No-op for keys that don't exist.
	DeleteVectors(ctx context.Context, keys []VectorKey) error
}

// VectorFactory builds a VectorStore from config.
type VectorFactory func() VectorStore
