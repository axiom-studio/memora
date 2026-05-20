package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types/api"
)

type ServiceDataSource struct {
	Metadata        adapter.MetadataStore
	Ledger          adapter.LedgerStore
	Graph           adapter.GraphStore
	RecallFunc      func(ctx context.Context, wsID string, req api.RecallRequest) (*api.RecallResponse, error)
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

func (s *ServiceDataSource) ListMemories(ctx context.Context, wsID string, limit int) ([]MemorySummary, error) {
	mems, err := s.Metadata.ListMemories(ctx, wsID, "", limit)
	if err != nil {
		return nil, err
	}
	out := make([]MemorySummary, len(mems))
	for i, m := range mems {
		out[i] = MemorySummary{
			ID:          m.ID,
			Content:     m.Content,
			ContentMD5:  m.ContentMD5,
			Watermark:   m.HeadWatermark,
			AgentID:     m.WrittenByAgentID,
			RecallReady: m.RecallReady,
			Tags:        m.Tags,
			CreatedAt:   m.CreatedAt,
			UpdatedAt:   m.UpdatedAt,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) GetMemory(ctx context.Context, memID string) (*MemorySummary, error) {
	m, err := s.Metadata.GetMemory(ctx, memID)
	if err != nil {
		return nil, err
	}
	return &MemorySummary{
		ID:          m.ID,
		Content:     m.Content,
		ContentMD5:  m.ContentMD5,
		Watermark:   m.HeadWatermark,
		AgentID:     m.WrittenByAgentID,
		RecallReady: m.RecallReady,
		Tags:        m.Tags,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}, nil
}

func (s *ServiceDataSource) GetCells(ctx context.Context, memID string) ([]CellSummary, error) {
	cells, err := s.Metadata.GetCells(ctx, memID)
	if err != nil {
		return nil, err
	}
	out := make([]CellSummary, len(cells))
	for i, c := range cells {
		out[i] = CellSummary{
			CellID:   c.CellID,
			Text:     c.Text,
			TextMD5:  c.TextMD5,
			Sequence: c.Seq,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) GetEdges(ctx context.Context, wsID, memID string) ([]EdgeSummary, error) {
	if s.Graph == nil {
		return nil, nil
	}
	edges, _, err := s.Graph.Neighbors(ctx, wsID, memID, adapter.NeighborsOpts{Direction: api.GraphDirection("both")})
	if err != nil {
		return nil, err
	}
	out := make([]EdgeSummary, len(edges))
	for i, e := range edges {
		out[i] = EdgeSummary{
			EdgeID:         e.EdgeID,
			SourceMemoryID: e.SourceMemoryID,
			TargetMemoryID: e.TargetMemoryID,
			EdgeType:       string(e.EdgeType),
			Properties:     marshalProps(e.PropertiesJSON),
			AgentID:        e.CreatedByAgentID,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) Recall(ctx context.Context, wsID string, query string, mode string, k int) (*api.RecallResponse, error) {
	if s.RecallFunc == nil {
		return nil, fmt.Errorf("recall not configured")
	}
	if k <= 0 {
		k = 10
	}
	req := api.RecallRequest{
		Query: query,
		Mode:  api.RecallMode(mode),
		K:     k,
	}
	return s.RecallFunc(ctx, wsID, req)
}

func (s *ServiceDataSource) AuditQuery(ctx context.Context, wsID, agentID string, ops []string, since, until *time.Time, cursor string, limit int) ([]api.LedgerEntry, string, error) {
	if s.Ledger == nil || !s.Ledger.Capabilities().SupportsQuery {
		return nil, "", nil
	}
	if limit <= 0 {
		limit = 50
	}
	q := adapter.LedgerQuery{
		WorkspaceID:   wsID,
		AgentID:       agentID,
		Op:            ops,
		Since:         since,
		Until:         until,
		SinceLedgerID: cursor,
		Limit:         limit,
	}
	return s.Ledger.Query(ctx, q)
}

func marshalProps(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}
