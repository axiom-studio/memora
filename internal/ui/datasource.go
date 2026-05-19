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

type DataSource interface {
	DashboardStats(ctx context.Context) (DashboardStats, error)
	RecentLedgerEntries(ctx context.Context, limit int) ([]api.LedgerEntry, error)
	LedgerEntriesSince(ctx context.Context, since time.Time, ops []string, limit int) ([]api.LedgerEntry, error)
}
