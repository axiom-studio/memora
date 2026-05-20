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

type DataSource interface {
	DashboardStats(ctx context.Context) (DashboardStats, error)
	RecentLedgerEntries(ctx context.Context, limit int) ([]api.LedgerEntry, error)
	LedgerEntriesSince(ctx context.Context, since time.Time, ops []string, limit int) ([]api.LedgerEntry, error)
	ListWorkspaces(ctx context.Context) ([]WorkspaceSummary, error)
	GetWorkspace(ctx context.Context, id string) (*WorkspaceDetail, error)
	ListAgents(ctx context.Context, wsID string) ([]AgentSummary, error)
	ListCollections(ctx context.Context, wsID string) ([]CollectionSummary, error)
}
