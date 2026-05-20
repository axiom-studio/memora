package ui

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types/api"
)

type DashboardStats struct {
	WorkspaceCount  int
	MemoryCount     int
	RecallReadyPct  int
	EmbedQueueDepth int
	FederationPeers int
	HealthyPeers    int
}

type WorkspaceSummary struct {
	ID             string
	Name           string
	EmbeddingModel string
	AgentCount     int
	MemoryCount    int
	CreatedAt      time.Time
}

type WorkspaceDetail struct {
	WorkspaceSummary
	Region          string
	ChunkerID       string
	AutoLinkEnabled bool
	Meta            map[string]any
}

type AgentSummary struct {
	AgentID          string
	DisplayName      string
	IdentityProvider string
	AgentType        string
	Model            string
	Deactivated      bool
	CreatedAt        time.Time
}

type CollectionSummary struct {
	ID          string
	Name        string
	MemoryCount int
	CreatedAt   time.Time
}

type MemorySummary struct {
	ID           string
	Content      string
	ContentMD5   string
	Watermark    string
	AgentID      string
	RecallReady  bool
	Tags         map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type EdgeSummary struct {
	EdgeID         string
	SourceMemoryID string
	TargetMemoryID string
	EdgeType       string
	Properties     string
	AgentID        string
}

type CellSummary struct {
	CellID   string
	Text     string
	TextMD5  string
	Sequence int
}

type SettingsInfo struct {
	ServerAddr      string
	ServerMode      string
	MCPEnabled      bool
	TLSEnabled      bool
	TLSCertFile     string
	TLSAutoSelfSign bool
	DataDir         string
	MetadataDriver  string
	VectorDriver    string
	LedgerDriver    string
	GraphDriver     string
	ContentDriver   string
	EmbeddingModel  string
	FederationEnabled bool
	FederationID      string
	PeerCount         int
	TelemetryLogLevel  string
	TelemetryLogFormat string
}

type FederationInfo struct {
	FederationID string
	Peers        []PeerInfo
}

type PeerInfo struct {
	ID        string
	Name      string
	Endpoint  string
	TrustMode string
	Workspaces []string
}

type CreateCollectionInput struct {
	WorkspaceID string
	Name        string
}

type CreateWorkspaceInput struct {
	Name           string
	Region         string
	ChunkerID      string
	EmbeddingModel string
}

type UpdateWorkspaceInput struct {
	Name           string
	Region         string
	ChunkerID      string
	EmbeddingModel string
}

type DataSource interface {
	DashboardStats(ctx context.Context) (DashboardStats, error)
	RecentLedgerEntries(ctx context.Context, limit int) ([]api.LedgerEntry, error)
	LedgerEntriesSince(ctx context.Context, since time.Time, ops []string, limit int) ([]api.LedgerEntry, error)
	ListWorkspaces(ctx context.Context) ([]WorkspaceSummary, error)
	GetWorkspace(ctx context.Context, id string) (*WorkspaceDetail, error)
	CreateWorkspace(ctx context.Context, input CreateWorkspaceInput) (string, error)
	UpdateWorkspace(ctx context.Context, id string, input UpdateWorkspaceInput) error
	DeleteWorkspace(ctx context.Context, id string) error
	ListAgents(ctx context.Context, wsID string) ([]AgentSummary, error)
	ListCollections(ctx context.Context, wsID string) ([]CollectionSummary, error)
	CreateCollection(ctx context.Context, input CreateCollectionInput) (string, error)
	DeleteCollection(ctx context.Context, id string) error
	ImprintMemory(ctx context.Context, wsID string, req api.ImprintRequest) (*api.ImprintResponse, error)
	UpdateMemory(ctx context.Context, wsID, memID string, req api.UpdateRequest) (*api.UpdateResponse, error)
	PatchMemory(ctx context.Context, wsID, memID string, req api.PatchRequest) (*api.PatchResponse, error)
	AppendMemory(ctx context.Context, wsID, memID string, req api.AppendRequest) (*api.AppendResponse, error)
	ForgetMemory(ctx context.Context, wsID, memID string) (*api.ForgetResponse, error)
	ListMemories(ctx context.Context, wsID string, limit int) ([]MemorySummary, error)
	GetMemory(ctx context.Context, memID string) (*MemorySummary, error)
	GetCells(ctx context.Context, memID string) ([]CellSummary, error)
	GetEdges(ctx context.Context, wsID, memID string) ([]EdgeSummary, error)
	Recall(ctx context.Context, wsID string, query string, mode string, k int) (*api.RecallResponse, error)
	AuditQuery(ctx context.Context, wsID, agentID string, ops []string, since, until *time.Time, cursor string, limit int) ([]api.LedgerEntry, string, error)
}
