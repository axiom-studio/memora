package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

type ctxKey int

const ctxKeyAgent ctxKey = iota

func agentFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAgent).(string)
	return v
}

// dispatchTool routes a tool call to the corresponding service-layer
// method. Returns a JSON-marshallable result or an error.
func (s *Server) dispatchTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	pinned := agentFrom(ctx)

	switch name {
	case "memora_imprint":
		var in struct {
			WorkspaceID   string            `json:"workspace_id"`
			AgentID       string            `json:"agent_id"`
			CollectionID  string            `json:"collection_id"`
			Content       string            `json:"content"`
			Tags          map[string]string `json:"tags"`
			ChunkerID     string            `json:"chunker_id"`
			ChunkerConfig map[string]string `json:"chunker_config"`
			AutoLink      *bool             `json:"auto_link"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		return s.svc.Imprint(ctx, in.WorkspaceID, in.AgentID, api.ImprintRequest{
			CollectionID: in.CollectionID, Content: in.Content, Tags: in.Tags,
			ChunkerID: in.ChunkerID, ChunkerConfig: in.ChunkerConfig,
			AutoLink: in.AutoLink,
		})
	case "memora_lookup":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			MemoryID    string `json:"memory_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		m, err := s.svc.Metadata.GetMemory(ctx, in.MemoryID)
		if err != nil {
			return nil, err
		}
		return m, nil
	case "memora_update":
		var in struct {
			WorkspaceID       string `json:"workspace_id"`
			MemoryID          string `json:"memory_id"`
			AgentID           string `json:"agent_id"`
			Content           string `json:"content"`
			ExpectedWatermark string `json:"expected_watermark"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		return s.svc.Update(ctx, in.WorkspaceID, in.MemoryID, in.AgentID, in.ExpectedWatermark, api.UpdateRequest{
			Content: in.Content, ExpectedWatermark: in.ExpectedWatermark,
		})
	case "memora_patch":
		var in struct {
			WorkspaceID       string        `json:"workspace_id"`
			MemoryID          string        `json:"memory_id"`
			AgentID           string        `json:"agent_id"`
			ExpectedWatermark string        `json:"expected_watermark"`
			Patch             []api.PatchOp `json:"patch"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		return s.svc.Patch(ctx, in.WorkspaceID, in.MemoryID, in.AgentID, in.ExpectedWatermark, api.PatchRequest{
			Patch: in.Patch, ExpectedWatermark: in.ExpectedWatermark,
		})
	case "memora_append":
		var in struct {
			WorkspaceID       string `json:"workspace_id"`
			MemoryID          string `json:"memory_id"`
			AgentID           string `json:"agent_id"`
			Content           string `json:"content"`
			ExpectedWatermark string `json:"expected_watermark"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		return s.svc.Append(ctx, in.WorkspaceID, in.MemoryID, in.AgentID, in.ExpectedWatermark, api.AppendRequest{
			Content: in.Content, ExpectedWatermark: in.ExpectedWatermark,
		})
	case "memora_forget":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			MemoryID    string `json:"memory_id"`
			AgentID     string `json:"agent_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		return s.svc.Forget(ctx, in.WorkspaceID, in.MemoryID, in.AgentID)
	case "memora_recall":
		var in api.RecallRequest
		var wrapper struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(args, &wrapper)
		return s.svc.Recall(ctx, wrapper.WorkspaceID, in)
	case "memora_list_workspaces":
		return s.svc.Metadata.ListWorkspaces(ctx, 100)
	case "memora_create_workspace":
		var in api.CreateWorkspaceRequest
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		ws := &types.Workspace{Name: in.Name, Region: in.Region, ChunkerID: in.ChunkerID, EmbeddingModel: in.EmbeddingModel, Meta: in.Meta}
		if err := s.svc.Metadata.CreateWorkspace(ctx, ws); err != nil {
			return nil, err
		}
		return ws, nil
	case "memora_get_workspace":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return s.svc.Metadata.GetWorkspace(ctx, in.WorkspaceID)
	case "memora_delete_workspace":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if err := s.svc.Metadata.DeleteWorkspace(ctx, in.WorkspaceID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": in.WorkspaceID}, nil
	case "memora_list_collections":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return s.svc.Metadata.ListCollections(ctx, in.WorkspaceID)
	case "memora_create_collection":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			Name        string `json:"name"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		c := &types.Collection{WorkspaceID: in.WorkspaceID, Name: in.Name}
		if err := s.svc.Metadata.CreateCollection(ctx, c); err != nil {
			return nil, err
		}
		return c, nil
	case "memora_delete_collection":
		var in struct {
			CollectionID string `json:"collection_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if err := s.svc.Metadata.DeleteCollection(ctx, in.CollectionID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": in.CollectionID}, nil
	case "memora_list_memories":
		var in struct {
			WorkspaceID  string `json:"workspace_id"`
			CollectionID string `json:"collection_id"`
			Limit        int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return s.svc.Metadata.ListMemories(ctx, in.WorkspaceID, in.CollectionID, in.Limit)
	case "memora_get_watermark_history":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			MemoryID    string `json:"memory_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		hist, err := s.svc.Metadata.GetWatermarkHistory(ctx, in.WorkspaceID, in.MemoryID, sevenDaysAgo())
		if err != nil {
			return nil, err
		}
		return map[string]any{"watermarks": hist}, nil
	case "memora_register_agent":
		var in api.RegisterAgentRequest
		var wrapper struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(args, &wrapper)
		if pinned != "" {
			in.AgentID = pinned
		}
		a := &types.Agent{
			AgentID:          in.AgentID,
			WorkspaceID:      wrapper.WorkspaceID,
			DisplayName:      in.DisplayName,
			IdentityProvider: in.IdentityProvider,
			IdentityProof:    in.IdentityProof,
			AgentType:        in.AgentType,
			Model:            in.Model,
			Capabilities:     in.Capabilities,
		}
		if err := s.svc.Metadata.RegisterAgent(ctx, a); err != nil {
			return nil, err
		}
		return a, nil
	case "memora_list_agents":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return s.svc.Metadata.ListAgents(ctx, in.WorkspaceID, 0)
	case "memora_link":
		var in struct {
			WorkspaceID    string         `json:"workspace_id"`
			SourceMemoryID string         `json:"source_memory_id"`
			TargetMemoryID string         `json:"target_memory_id"`
			EdgeType       string         `json:"edge_type"`
			AgentID        string         `json:"agent_id"`
			Properties     map[string]any `json:"properties_json"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		out, err := s.svc.GraphLink(ctx, types.Edge{
			WorkspaceID:      in.WorkspaceID,
			SourceMemoryID:   in.SourceMemoryID,
			TargetMemoryID:   in.TargetMemoryID,
			EdgeType:         types.EdgeType(in.EdgeType),
			PropertiesJSON:   in.Properties,
			CreatedByAgentID: in.AgentID,
		})
		if err != nil {
			return nil, err
		}
		s.svc.AppendLedger(ctx, api.LedgerEntry{
			WorkspaceID: in.WorkspaceID, Op: "link", Target: out.EdgeID, AgentID: in.AgentID,
			WatermarkAfter: out.Watermark, Metadata: map[string]any{"edge_type": in.EdgeType},
		})
		return out, nil
	case "memora_unlink":
		var in struct {
			EdgeID  string `json:"edge_id"`
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if pinned != "" {
			in.AgentID = pinned
		}
		if err := s.svc.GraphUnlink(ctx, in.EdgeID, in.AgentID); err != nil {
			return nil, err
		}
		return map[string]any{"edge_id": in.EdgeID, "unlinked": true}, nil
	case "memora_neighbors":
		var in struct {
			WorkspaceID string   `json:"workspace_id"`
			MemoryID    string   `json:"memory_id"`
			Direction   string   `json:"direction"`
			EdgeTypes   []string `json:"edge_types"`
			K           int      `json:"k"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		edges, headers, err := s.svc.GraphNeighbors(ctx, in.WorkspaceID, in.MemoryID,
			adapter.NeighborsOpts{Direction: api.GraphDirection(in.Direction), EdgeTypes: in.EdgeTypes, K: in.K})
		if err != nil {
			return nil, err
		}
		return map[string]any{"edges": edges, "neighbors": headers}, nil
	case "memora_traverse":
		var in struct {
			WorkspaceID  string         `json:"workspace_id"`
			SeedMemoryID string         `json:"seed_memory_id"`
			Depth        int            `json:"depth"`
			Direction    string         `json:"direction"`
			EdgeTypes    []string       `json:"edge_types"`
			Filter       map[string]any `json:"filter"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return s.svc.GraphTraverse(ctx, in.WorkspaceID, in.SeedMemoryID,
			adapter.TraverseOpts{Depth: in.Depth, Direction: api.GraphDirection(in.Direction), EdgeTypes: in.EdgeTypes, Filter: in.Filter})
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func sevenDaysAgo() time.Time { return time.Now().AddDate(0, 0, -7) }
