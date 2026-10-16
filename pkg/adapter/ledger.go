package adapter

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

// LedgerConfig is the driver-agnostic input to LedgerStore.Open.
type LedgerConfig struct {
	Driver string         `yaml:"driver" json:"driver"`
	DSN    string         `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Extra  map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// LedgerCapabilities advertises what a LedgerStore supports.
type LedgerCapabilities struct {
	SupportsAppend      bool `json:"supports_append"`
	SupportsBatchAppend bool `json:"supports_batch_append"`
	SupportsQuery       bool `json:"supports_query"`
	SupportsRedaction   bool `json:"supports_redaction"`
	DurableOnAppend     bool `json:"durable_on_append"`
	EstimatedAppendQPS  int  `json:"estimated_append_qps"`
}

// LedgerQuery is the filter set for LedgerStore.Query.
type LedgerQuery struct {
	WorkspaceID   string
	Since         *time.Time
	Until         *time.Time
	Actor         string
	AgentID       string
	Op            []string
	MemoryID      string
	EdgeID        string
	Limit         int
	SinceLedgerID string
}

// LedgerStore is the audit-log persistence contract.
type LedgerStore interface {
	Open(ctx context.Context, cfg LedgerConfig) error
	Close() error
	Ping(ctx context.Context) error
	Capabilities() LedgerCapabilities

	// Append writes a single audit-log entry. Implementations must be durable on return (fsync or WAL commit).
	Append(ctx context.Context, e api.LedgerEntry) error
	// AppendBatch writes multiple entries atomically. Returns error if any entry fails.
	AppendBatch(ctx context.Context, entries []api.LedgerEntry) error
	// Query returns ledger entries matching the filter, plus a cursor for pagination. Requires SupportsQuery capability.
	Query(ctx context.Context, q LedgerQuery) (entries []api.LedgerEntry, nextCursor string, err error)
	// Redact tombstones specific fields on a ledger entry (GDPR Art. 17). The row is preserved with Redacted=true. Requires SupportsRedaction capability.
	Redact(ctx context.Context, ledgerID string, fields []string) error
}

// LedgerFactory builds a LedgerStore from config.
type LedgerFactory func() LedgerStore
