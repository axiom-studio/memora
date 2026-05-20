package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

type ServiceDataSource struct {
	Metadata        adapter.MetadataStore
	Ledger          adapter.LedgerStore
	Graph           adapter.GraphStore
	RecallFunc      func(ctx context.Context, wsID string, req api.RecallRequest) (*api.RecallResponse, error)
	ImprintFunc     func(ctx context.Context, wsID, agentID string, req api.ImprintRequest) (*api.ImprintResponse, error)
	UpdateFunc      func(ctx context.Context, wsID, memID, agentID, ifMatch string, req api.UpdateRequest) (*api.UpdateResponse, error)
	PatchFunc       func(ctx context.Context, wsID, memID, agentID, ifMatch string, req api.PatchRequest) (*api.PatchResponse, error)
	AppendFunc      func(ctx context.Context, wsID, memID, agentID, ifMatch string, req api.AppendRequest) (*api.AppendResponse, error)
	ForgetFunc      func(ctx context.Context, wsID, memID, agentID string) (*api.ForgetResponse, error)
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

func (s *ServiceDataSource) CreateWorkspace(ctx context.Context, input CreateWorkspaceInput) (string, error) {
	ws := &types.Workspace{
		Name:           input.Name,
		Region:         input.Region,
		ChunkerID:      input.ChunkerID,
		EmbeddingModel: input.EmbeddingModel,
	}
	if err := s.Metadata.CreateWorkspace(ctx, ws); err != nil {
		return "", err
	}
	return ws.ID, nil
}

func (s *ServiceDataSource) UpdateWorkspace(ctx context.Context, id string, input UpdateWorkspaceInput) error {
	ws := &types.Workspace{
		ID:             id,
		Name:           input.Name,
		Region:         input.Region,
		ChunkerID:      input.ChunkerID,
		EmbeddingModel: input.EmbeddingModel,
	}
	return s.Metadata.UpdateWorkspace(ctx, ws)
}

func (s *ServiceDataSource) DeleteWorkspace(ctx context.Context, id string) error {
	return s.Metadata.DeleteWorkspace(ctx, id)
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

func (s *ServiceDataSource) RegisterAgent(ctx context.Context, wsID string, req RegisterAgentInput) error {
	a := &types.Agent{
		AgentID:          req.AgentID,
		WorkspaceID:      wsID,
		DisplayName:      req.DisplayName,
		IdentityProvider: req.IdentityProvider,
		AgentType:        req.AgentType,
		Model:            req.Model,
		Active:           true,
	}
	return s.Metadata.RegisterAgent(ctx, a)
}

func (s *ServiceDataSource) DeactivateAgent(ctx context.Context, wsID, agentID string) error {
	return s.Metadata.DeactivateAgent(ctx, wsID, agentID)
}

func (s *ServiceDataSource) ListCollections(ctx context.Context, wsID string) ([]CollectionSummary, error) {
	colls, err := s.Metadata.ListCollections(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]CollectionSummary, len(colls))
	for i, c := range colls {
		mems, _ := s.Metadata.ListMemories(ctx, wsID, c.ID, 10000)
		out[i] = CollectionSummary{
			ID:          c.ID,
			Name:        c.Name,
			MemoryCount: len(mems),
			CreatedAt:   c.CreatedAt,
		}
	}
	return out, nil
}

func (s *ServiceDataSource) CreateCollection(ctx context.Context, input CreateCollectionInput) (string, error) {
	c := &types.Collection{
		WorkspaceID: input.WorkspaceID,
		Name:        input.Name,
	}
	if err := s.Metadata.CreateCollection(ctx, c); err != nil {
		return "", err
	}
	return c.ID, nil
}

func (s *ServiceDataSource) DeleteCollection(ctx context.Context, id string) error {
	return s.Metadata.DeleteCollection(ctx, id)
}

func (s *ServiceDataSource) ImprintMemory(ctx context.Context, wsID string, req api.ImprintRequest) (*api.ImprintResponse, error) {
	if s.ImprintFunc == nil {
		return nil, fmt.Errorf("imprint not configured")
	}
	return s.ImprintFunc(ctx, wsID, "ui-admin", req)
}

func (s *ServiceDataSource) UpdateMemory(ctx context.Context, wsID, memID string, req api.UpdateRequest) (*api.UpdateResponse, error) {
	if s.UpdateFunc == nil {
		return nil, fmt.Errorf("update not configured")
	}
	return s.UpdateFunc(ctx, wsID, memID, "ui-admin", "", req)
}

func (s *ServiceDataSource) PatchMemory(ctx context.Context, wsID, memID string, req api.PatchRequest) (*api.PatchResponse, error) {
	if s.PatchFunc == nil {
		return nil, fmt.Errorf("patch not configured")
	}
	return s.PatchFunc(ctx, wsID, memID, "ui-admin", "", req)
}

func (s *ServiceDataSource) AppendMemory(ctx context.Context, wsID, memID string, req api.AppendRequest) (*api.AppendResponse, error) {
	if s.AppendFunc == nil {
		return nil, fmt.Errorf("append not configured")
	}
	return s.AppendFunc(ctx, wsID, memID, "ui-admin", "", req)
}

func (s *ServiceDataSource) ForgetMemory(ctx context.Context, wsID, memID string) (*api.ForgetResponse, error) {
	if s.ForgetFunc == nil {
		return nil, fmt.Errorf("forget not configured")
	}
	return s.ForgetFunc(ctx, wsID, memID, "ui-admin")
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

func (s *ServiceDataSource) RecallFull(ctx context.Context, wsID string, req api.RecallRequest) (*api.RecallResponse, error) {
	if s.RecallFunc == nil {
		return nil, fmt.Errorf("recall not configured")
	}
	if req.K <= 0 {
		req.K = 10
	}
	return s.RecallFunc(ctx, wsID, req)
}

func (s *ServiceDataSource) GraphData(ctx context.Context, wsID string, seedMemID string, depth int) (*GraphData, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("graph store not configured")
	}
	if depth <= 0 {
		depth = 2
	}

	nodeCount, edgeCountByType, _ := s.Graph.Stats(ctx, wsID)

	if seedMemID != "" {
		result, err := s.Graph.Traverse(ctx, wsID, seedMemID, adapter.TraverseOpts{
			Depth:     depth,
			Direction: "both",
			MaxEdges:  500,
		})
		if err != nil {
			return nil, fmt.Errorf("traverse: %w", err)
		}

		nodeSet := map[string]bool{result.Seed.MemoryID: true}
		var nodes []GraphNode
		nodes = append(nodes, GraphNode{
			ID:    result.Seed.MemoryID,
			Label: truncateStr(result.Seed.MemoryID, 16),
			Type:  "seed",
		})

		var edges []GraphEdge
		for _, layer := range result.Layers {
			for _, hit := range layer {
				if !nodeSet[hit.Memory.MemoryID] {
					nodeSet[hit.Memory.MemoryID] = true
					nodes = append(nodes, GraphNode{
						ID:    hit.Memory.MemoryID,
						Label: truncateStr(hit.Memory.MemoryID, 16),
						Type:  "neighbor",
					})
				}
				edges = append(edges, GraphEdge{
					ID:     hit.ViaEdgeID,
					Source: result.Seed.MemoryID,
					Target: hit.Memory.MemoryID,
					Label:  hit.ViaEdgeType,
				})
			}
		}

		return &GraphData{
			Nodes:           nodes,
			Edges:           edges,
			NodeCount:       nodeCount,
			EdgeCountByType: edgeCountByType,
		}, nil
	}

	mems, err := s.Metadata.ListMemories(ctx, wsID, "", 200)
	if err != nil {
		return nil, err
	}

	nodeSet := map[string]bool{}
	var nodes []GraphNode
	var edges []GraphEdge

	for _, m := range mems {
		nodeSet[m.ID] = true
		nodes = append(nodes, GraphNode{
			ID:    m.ID,
			Label: truncateStr(m.ID, 16),
			Type:  "memory",
		})
	}

	for _, m := range mems {
		neighborEdges, _, _ := s.Graph.Neighbors(ctx, wsID, m.ID, adapter.NeighborsOpts{
			Direction: "both",
			K:         50,
		})
		for _, e := range neighborEdges {
			if !nodeSet[e.TargetMemoryID] {
				nodeSet[e.TargetMemoryID] = true
				nodes = append(nodes, GraphNode{
					ID:    e.TargetMemoryID,
					Label: truncateStr(e.TargetMemoryID, 16),
					Type:  "memory",
				})
			}
			if !nodeSet[e.SourceMemoryID] {
				nodeSet[e.SourceMemoryID] = true
				nodes = append(nodes, GraphNode{
					ID:    e.SourceMemoryID,
					Label: truncateStr(e.SourceMemoryID, 16),
					Type:  "memory",
				})
			}
			edges = append(edges, GraphEdge{
				ID:     e.EdgeID,
				Source: e.SourceMemoryID,
				Target: e.TargetMemoryID,
				Label:  string(e.EdgeType),
			})
		}
	}

	seen := map[string]bool{}
	deduped := edges[:0]
	for _, e := range edges {
		if !seen[e.ID] {
			seen[e.ID] = true
			deduped = append(deduped, e)
		}
	}

	return &GraphData{
		Nodes:           nodes,
		Edges:           deduped,
		NodeCount:       nodeCount,
		EdgeCountByType: edgeCountByType,
	}, nil
}

func (s *ServiceDataSource) GraphNeighbors(ctx context.Context, wsID, memoryID, direction string, edgeTypes []string, k int) (*GraphData, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("graph store not configured")
	}
	if k <= 0 {
		k = 50
	}
	if direction == "" {
		direction = "both"
	}

	edges, headers, err := s.Graph.Neighbors(ctx, wsID, memoryID, adapter.NeighborsOpts{
		Direction: api.GraphDirection(direction),
		EdgeTypes: edgeTypes,
		K:         k,
	})
	if err != nil {
		return nil, err
	}

	nodeSet := map[string]bool{memoryID: true}
	nodes := []GraphNode{{ID: memoryID, Label: truncateStr(memoryID, 16), Type: "seed"}}
	for _, h := range headers {
		if !nodeSet[h.MemoryID] {
			nodeSet[h.MemoryID] = true
			nodes = append(nodes, GraphNode{ID: h.MemoryID, Label: truncateStr(h.MemoryID, 16), Type: "neighbor"})
		}
	}

	var graphEdges []GraphEdge
	for _, e := range edges {
		if !nodeSet[e.SourceMemoryID] {
			nodeSet[e.SourceMemoryID] = true
			nodes = append(nodes, GraphNode{ID: e.SourceMemoryID, Label: truncateStr(e.SourceMemoryID, 16), Type: "neighbor"})
		}
		if !nodeSet[e.TargetMemoryID] {
			nodeSet[e.TargetMemoryID] = true
			nodes = append(nodes, GraphNode{ID: e.TargetMemoryID, Label: truncateStr(e.TargetMemoryID, 16), Type: "neighbor"})
		}
		graphEdges = append(graphEdges, GraphEdge{
			ID:     e.EdgeID,
			Source: e.SourceMemoryID,
			Target: e.TargetMemoryID,
			Label:  string(e.EdgeType),
		})
	}

	nodeCount, edgeCountByType, _ := s.Graph.Stats(ctx, wsID)
	return &GraphData{
		Nodes:           nodes,
		Edges:           graphEdges,
		NodeCount:       nodeCount,
		EdgeCountByType: edgeCountByType,
	}, nil
}

func (s *ServiceDataSource) GraphTraverse(ctx context.Context, wsID, seedMemID, direction string, edgeTypes []string, depth int) (*GraphData, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("graph store not configured")
	}
	if depth <= 0 {
		depth = 2
	}
	if direction == "" {
		direction = "both"
	}

	result, err := s.Graph.Traverse(ctx, wsID, seedMemID, adapter.TraverseOpts{
		Depth:     depth,
		Direction: api.GraphDirection(direction),
		EdgeTypes: edgeTypes,
		MaxEdges:  500,
	})
	if err != nil {
		return nil, fmt.Errorf("traverse: %w", err)
	}

	nodeSet := map[string]bool{result.Seed.MemoryID: true}
	nodes := []GraphNode{{ID: result.Seed.MemoryID, Label: truncateStr(result.Seed.MemoryID, 16), Type: "seed"}}
	var edges []GraphEdge

	for layerIdx, layer := range result.Layers {
		layerType := fmt.Sprintf("layer_%d", layerIdx+1)
		for _, hit := range layer {
			if !nodeSet[hit.Memory.MemoryID] {
				nodeSet[hit.Memory.MemoryID] = true
				nodes = append(nodes, GraphNode{ID: hit.Memory.MemoryID, Label: truncateStr(hit.Memory.MemoryID, 16), Type: layerType})
			}
			edges = append(edges, GraphEdge{
				ID:     hit.ViaEdgeID,
				Source: result.Seed.MemoryID,
				Target: hit.Memory.MemoryID,
				Label:  hit.ViaEdgeType,
			})
		}
	}

	nodeCount, edgeCountByType, _ := s.Graph.Stats(ctx, wsID)
	return &GraphData{
		Nodes:           nodes,
		Edges:           edges,
		NodeCount:       nodeCount,
		EdgeCountByType: edgeCountByType,
	}, nil
}

func (s *ServiceDataSource) GraphStats(ctx context.Context, wsID string) (int, map[string]int, error) {
	if s.Graph == nil {
		return 0, nil, fmt.Errorf("graph store not configured")
	}
	return s.Graph.Stats(ctx, wsID)
}

func (s *ServiceDataSource) LinkEdge(ctx context.Context, wsID, sourceMemID, targetMemID, edgeType string) (string, error) {
	if s.Graph == nil {
		return "", fmt.Errorf("graph store not configured")
	}
	e := types.Edge{
		WorkspaceID:      wsID,
		SourceMemoryID:   sourceMemID,
		TargetMemoryID:   targetMemID,
		EdgeType:         types.EdgeType(edgeType),
		CreatedByAgentID: "ui-admin",
	}
	out, err := s.Graph.Link(ctx, e)
	if err != nil {
		return "", err
	}
	return out.EdgeID, nil
}

func (s *ServiceDataSource) UnlinkEdge(ctx context.Context, edgeID string) error {
	if s.Graph == nil {
		return fmt.Errorf("graph store not configured")
	}
	return s.Graph.Unlink(ctx, edgeID, "ui-admin")
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
