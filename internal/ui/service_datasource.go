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

func (s *ServiceDataSource) ListWorkspaces(ctx context.Context) ([]WorkspaceSummary, error) {
	ws, err := s.Metadata.ListWorkspaces(ctx, 1000)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceSummary, len(ws))
	for i, w := range ws {
		agents, _ := s.Metadata.ListAgents(ctx, w.ID, 0)
		mems, _ := s.Metadata.ListMemories(ctx, w.ID, "", 10000)
		out[i] = WorkspaceSummary{
			ID:             w.ID,
			Name:           w.Name,
			EmbeddingModel: w.EmbeddingModel,
			AgentCount:     len(agents),
			MemoryCount:    len(mems),
			CreatedAt:      w.CreatedAt,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) GetWorkspace(ctx context.Context, id string) (*WorkspaceDetail, error) {
	w, err := s.Metadata.GetWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	agents, _ := s.Metadata.ListAgents(ctx, w.ID, 0)
	mems, _ := s.Metadata.ListMemories(ctx, w.ID, "", 10000)
	return &WorkspaceDetail{
		WorkspaceSummary: WorkspaceSummary{
			ID:             w.ID,
			Name:           w.Name,
			EmbeddingModel: w.EmbeddingModel,
			AgentCount:     len(agents),
			MemoryCount:    len(mems),
			CreatedAt:      w.CreatedAt,
		},
		Region:          w.Region,
		ChunkerID:       w.ChunkerID,
		AutoLinkEnabled: w.AutoLinkEnabled,
		Meta:            w.Meta,
	}, nil
}

func (s *ServiceDataSource) ListAgents(ctx context.Context, wsID string) ([]AgentSummary, error) {
	agents, err := s.Metadata.ListAgents(ctx, wsID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]AgentSummary, len(agents))
	for i, a := range agents {
		out[i] = AgentSummary{
			AgentID:          a.AgentID,
			DisplayName:      a.DisplayName,
			IdentityProvider: a.IdentityProvider,
			AgentType:        a.AgentType,
			Model:            a.Model,
			Deactivated:      !a.Active,
			CreatedAt:        a.RegisteredAt,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) ListCollections(ctx context.Context, wsID string) ([]CollectionSummary, error) {
	colls, err := s.Metadata.ListCollections(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]CollectionSummary, len(colls))
	for i, c := range colls {
		out[i] = CollectionSummary{
			ID:        c.ID,
			Name:      c.Name,
			CreatedAt: c.CreatedAt,
		}
	}
	return out, nil
}
