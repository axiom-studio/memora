package api

import (
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// CreateWorkspaceRequest creates a Workspace.
type CreateWorkspaceRequest struct {
	Name           string         `json:"name"`
	Region         string         `json:"region,omitempty"`
	ChunkerID      string         `json:"chunker_id,omitempty"`
	EmbeddingModel string         `json:"embedding_model,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

// CreateCollectionRequest creates a Collection.
type CreateCollectionRequest struct {
	Name string `json:"name"`
}

// RegisterAgentRequest registers (or upserts) an Agent.
type RegisterAgentRequest struct {
	AgentID          string         `json:"agent_id"`
	DisplayName      string         `json:"display_name,omitempty"`
	IdentityProvider string         `json:"identity_provider"`
	IdentityProof    map[string]any `json:"identity_proof,omitempty"`
	AgentType        string         `json:"agent_type,omitempty"`
	Model            string         `json:"model,omitempty"`
	Capabilities     map[string]any `json:"capabilities,omitempty"`
}

// AgentResponse echoes the persisted Agent.
type AgentResponse = types.Agent

// LedgerQueryRequest filters ledger entries.
type LedgerQueryRequest struct {
	Since         *time.Time `json:"since,omitempty"`
	Until         *time.Time `json:"until,omitempty"`
	Actor         string     `json:"actor,omitempty"` // api_key_id
	AgentID       string     `json:"agent_id,omitempty"`
	Op            []string   `json:"op,omitempty"`
	MemoryID      string     `json:"memory_id,omitempty"`
	EdgeID        string     `json:"edge_id,omitempty"`
	Limit         int        `json:"limit,omitempty"`
	SinceLedgerID string     `json:"since_ledger_id,omitempty"`
}

// LedgerEntry is one audit-log row.
type LedgerEntry struct {
	LedgerID        string         `json:"ledger_id"`
	WorkspaceID     string         `json:"workspace_id"`
	Op              string         `json:"op"`
	Target          string         `json:"target,omitempty"`
	AgentID         string         `json:"agent_id"`
	APIKeyID        string         `json:"api_key_id,omitempty"`
	UserID          string         `json:"user_id,omitempty"`
	WatermarkBefore string         `json:"watermark_before,omitempty"`
	WatermarkAfter  string         `json:"watermark_after,omitempty"`
	IP              string         `json:"ip,omitempty"`
	UserAgent       string         `json:"user_agent,omitempty"`
	Timestamp       time.Time      `json:"ts"`
	RequestID       string         `json:"request_id,omitempty"`
	LatencyMS       int            `json:"latency_ms,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Redacted        bool           `json:"redacted,omitempty"`
	RedactedFields  []string       `json:"redacted_fields,omitempty"`
}

// LedgerResponse is the paginated ledger query result.
type LedgerResponse struct {
	Entries    []LedgerEntry `json:"entries"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// HealthResponse is GET /healthz / /readyz output.
type HealthResponse struct {
	OK      bool              `json:"ok"`
	Status  string            `json:"status"`            // "live" | "ready" | "degraded"
	Adapter map[string]string `json:"adapter,omitempty"` // primary/vector/ledger -> "ok" | "down" | "degraded"
	Version string            `json:"version,omitempty"`
}

// ErrorEnvelope is the standard error response shape.
type ErrorEnvelope struct {
	ErrorCode string         `json:"error_code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// PeerRequest is POST /v1/federation/peers: create or replace the peer
// with NodeID. Keys are write-only: an empty InboundKey/InboundKeySHA256
// or APIKey keeps the stored one. InboundKey is the key the peer will
// present (stored only as its SHA-256); APIKey is the key this node
// presents to the peer (stored sealed). TLS file paths are config-only.
type PeerRequest struct {
	NodeID           string  `json:"node_id"`
	Name             string  `json:"name"`
	URL              string  `json:"url"`
	InboundKey       string  `json:"inbound_key,omitempty"`
	InboundKeySHA256 string  `json:"inbound_key_sha256,omitempty"`
	APIKey           string  `json:"api_key,omitempty"`
	ClientCertSHA256 string  `json:"client_cert_sha256,omitempty"`
	TimeoutMS        int     `json:"timeout_ms,omitempty"`
	RateLimitQPS     float64 `json:"rate_limit_qps,omitempty"`
	Revoked          bool    `json:"revoked,omitempty"`
}

// PeerView is a federation peer as the API shows it: never a key, only
// whether one is set.
type PeerView struct {
	NodeID           string    `json:"node_id"`
	Name             string    `json:"name"`
	URL              string    `json:"url"`
	TrustMode        string    `json:"trust_mode"`
	HasInboundKey    bool      `json:"has_inbound_key"`
	HasAPIKey        bool      `json:"has_api_key"`
	ClientCertSHA256 string    `json:"client_cert_sha256,omitempty"`
	TimeoutMS        int       `json:"timeout_ms,omitempty"`
	RateLimitQPS     float64   `json:"rate_limit_qps,omitempty"`
	Revoked          bool      `json:"revoked"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
