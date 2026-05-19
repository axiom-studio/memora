package ui

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types/api"
)

type ServiceDataSource struct {
	Metadata        adapter.MetadataStore
	Ledger          adapter.LedgerStore
	FederationPeers int
	HealthyPeers    int
	EmbedQueueDepth func() int
}

func (s *ServiceDataSource) DashboardStats(ctx context.Context) (DashboardStats, error) {
	stats := DashboardStats{
		FederationPeers: s.FederationPeers,
		HealthyPeers:    s.HealthyPeers,
	}

	ws, err := s.Metadata.ListWorkspaces(ctx, 1000)
	if err != nil {
		return stats, err
	}
	stats.WorkspaceCount = len(ws)

	var totalMem, readyMem int
	for _, w := range ws {
		mems, err := s.Metadata.ListMemories(ctx, w.ID, "", 10000)
		if err != nil {
			continue
		}
		totalMem += len(mems)
		for _, m := range mems {
			if m.RecallReady {
				readyMem++
			}
		}
	}
	stats.MemoryCount = totalMem
	if totalMem > 0 {
		stats.RecallReadyPct = (readyMem * 100) / totalMem
	}

	if s.EmbedQueueDepth != nil {
		stats.EmbedQueueDepth = s.EmbedQueueDepth()
	}

	return stats, nil
}

func (s *ServiceDataSource) RecentLedgerEntries(ctx context.Context, limit int) ([]api.LedgerEntry, error) {
	if s.Ledger == nil || !s.Ledger.Capabilities().SupportsQuery {
		return nil, nil
	}
	entries, _, err := s.Ledger.Query(ctx, adapter.LedgerQuery{Limit: limit})
	return entries, err
}

func (s *ServiceDataSource) LedgerEntriesSince(ctx context.Context, since time.Time, ops []string, limit int) ([]api.LedgerEntry, error) {
	if s.Ledger == nil || !s.Ledger.Capabilities().SupportsQuery {
		return nil, nil
	}
	entries, _, err := s.Ledger.Query(ctx, adapter.LedgerQuery{
		Since: &since,
		Op:    ops,
		Limit: limit,
	})
	return entries, err
}
