package adapter

import "context"

// ContentConfig is the driver-agnostic input to ContentStore.Open.
type ContentConfig struct {
	Driver string         `yaml:"driver" json:"driver"` // "sqlite" | "file" | "postgres" | "s3"
	DSN    string         `yaml:"dsn" json:"dsn"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// ContentCapabilities advertises what a ContentStore can do.
type ContentCapabilities struct {
	SupportsConditionalPut      bool   `json:"supports_conditional_put"`
	SupportsBatchGet            bool   `json:"supports_batch_get"`
	RecommendedMaxObjectMB      int    `json:"recommended_max_object_mb"`
	SupportsServerSideRedaction bool   `json:"supports_server_side_redaction"`
	DurabilityClass             string `json:"durability_class"` // "11-nines" | "5-nines" | "single-disk"
}

// ContentStore persists Memory and Cell content bytes. Drivers are
// keyed by (workspace_id, memory_id) for memory content and
// (workspace_id, memory_id, cell_id) for cell content. Stored bytes
// are retrievable by that key for the lifetime of the Memory.
type ContentStore interface {
	Open(ctx context.Context, cfg ContentConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() ContentCapabilities

	// PutMemoryContent stores the whole-document body for a memory.
	PutMemoryContent(ctx context.Context, workspaceID, memoryID, contentMD5, content string) error
	// GetMemoryContent retrieves the whole-document body. Returns ErrNotFound if absent.
	GetMemoryContent(ctx context.Context, workspaceID, memoryID string) (string, error)
	// DeleteMemoryContent removes the whole-document body.
	DeleteMemoryContent(ctx context.Context, workspaceID, memoryID string) error

	// PutCellContent stores a single chunk's text. Idempotent on (memory_id, cell_id, text_md5).
	PutCellContent(ctx context.Context, workspaceID, memoryID, cellID, textMD5, text string) error
	// GetCellContent retrieves a single chunk's text. Returns ErrNotFound if absent.
	GetCellContent(ctx context.Context, workspaceID, memoryID, cellID string) (string, error)
	// GetCellContentBatch retrieves multiple chunks' text in one call. Missing keys are omitted from the result map.
	GetCellContentBatch(ctx context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error)
	// DeleteCellContent removes a single chunk's text.
	DeleteCellContent(ctx context.Context, workspaceID, memoryID, cellID string) error

	// DeleteAllForMemory removes all content (memory + cells) for a memory. Used by Forget cascade.
	DeleteAllForMemory(ctx context.Context, workspaceID, memoryID string) error

	// ListMemoryIDs returns distinct memory IDs that have content stored
	// in the given workspace. Used by the orphan GC sweeper to enumerate
	// content keys and cross-reference against MetadataStore.
	ListMemoryIDs(ctx context.Context, workspaceID string) ([]string, error)
}

// ContentFactory builds a ContentStore from config.
type ContentFactory func() ContentStore
