package adapter

import (
	"context"
	"time"
)

// VectorConfig is the driver-agnostic input to VectorStore.Open.
type VectorConfig struct {
	Driver string         `yaml:"driver" json:"driver"`
	DSN    string         `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Dim    int            `yaml:"dim,omitempty" json:"dim,omitempty"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
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

// VectorFilter is the metadata WHERE pushdown for Query.
type VectorFilter struct {
	CollectionID string
	Tags         map[string][]string
	TsAfter      *time.Time
	TsBefore     *time.Time
	AgentID      string
	AgentIDIn    []string
	AgentIDNotIn []string
}

// VectorQuery is the input to VectorStore.Query.
type VectorQuery struct {
	WorkspaceID string
	Embedding   []float32
	K           int
	Filter      VectorFilter
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
	// DeleteVectors removes vectors by key. No-op for keys that don't exist.
	DeleteVectors(ctx context.Context, keys []VectorKey) error
}

// VectorFactory builds a VectorStore from config.
type VectorFactory func() VectorStore
