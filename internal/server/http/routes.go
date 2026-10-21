package http

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/memora/internal/federation"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// routes wires the entire REST surface.
func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/readyz", s.readyz)
	s.mux.HandleFunc("/metrics", s.metrics)

	s.mux.HandleFunc("/v1/search", s.handleSearch)
	s.mux.HandleFunc("/v1/federation/search", s.handleFederationSearch)
	s.mux.HandleFunc("/v1/workspaces", s.handleWorkspaces)
	s.mux.HandleFunc("/v1/workspaces/", s.handleWorkspacePath)
}

// /healthz
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, 200, api.HealthResponse{OK: true, Status: "live"})
}

// /readyz pings every adapter.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	resp := api.HealthResponse{OK: true, Status: "ready", Adapter: map[string]string{}}
	if err := s.cfg.Service.Metadata.Ping(r.Context()); err != nil {
		resp.Adapter["primary"] = "down: " + err.Error()
		resp.OK = false
	} else {
		resp.Adapter["primary"] = "ok"
	}
	if s.cfg.Service.Vector != nil {
		if err := s.cfg.Service.Vector.Ping(r.Context()); err != nil {
			resp.Adapter["vector"] = "down: " + err.Error()
			resp.OK = false
		} else {
			resp.Adapter["vector"] = "ok"
		}
	}
	if s.cfg.Service.Ledger != nil {
		if err := s.cfg.Service.Ledger.Ping(r.Context()); err != nil {
			resp.Adapter["ledger"] = "down: " + err.Error()
			resp.OK = false
		} else {
			resp.Adapter["ledger"] = "ok"
		}
	}
	if !resp.OK {
		resp.Status = "degraded"
		s.writeJSON(w, 503, resp)
		return
	}
	s.writeJSON(w, 200, resp)
}

// /metrics — minimal Prometheus exposition.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# Memora minimal metrics\nmemora_up 1\n"))
}

// /v1/workspaces (collection)
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req api.CreateWorkspaceRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		ws := &types.Workspace{Name: req.Name, Region: req.Region, ChunkerID: req.ChunkerID, EmbeddingModel: req.EmbeddingModel, Meta: req.Meta}
		if err := s.cfg.Service.Metadata.CreateWorkspace(r.Context(), ws); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 201, ws)
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 100
		}
		ws, nextCursor, err := s.cfg.Service.Metadata.ListWorkspaces(r.Context(), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		if ws == nil {
			ws = []types.Workspace{}
		}
		resp := map[string]any{"workspaces": ws}
		if nextCursor != "" {
			resp["next_cursor"] = nextCursor
		}
		s.writeJSON(w, 200, resp)
	default:
		w.Header().Set("Allow", "GET, POST")
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/... — multiplexer
func (s *Server) handleWorkspacePath(w http.ResponseWriter, r *http.Request) {
	// Split path after /v1/workspaces/.
	rest := strings.TrimPrefix(r.URL.Path, "/v1/workspaces/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		s.writeError(w, 404, "not_found", "missing workspace id", nil)
		return
	}
	wsID := parts[0]
	if !s.validID(w, "workspace_id", wsID) {
		return
	}

	if len(parts) == 1 {
		s.handleWorkspaceSingle(w, r, wsID)
		return
	}

	switch parts[1] {
	case "collections":
		s.handleCollections(w, r, wsID, parts[2:])
	case "memories":
		s.handleMemories(w, r, wsID, parts[2:])
	case "agents":
		s.handleAgents(w, r, wsID, parts[2:])
	case "edges":
		s.handleEdges(w, r, wsID, parts[2:])
	case "graph":
		s.handleGraph(w, r, wsID, parts[2:])
	case "recall":
		if len(parts) >= 3 && parts[2] == "pins" {
			s.handlePins(w, r, wsID, parts[3:])
		} else {
			s.handleRecall(w, r, wsID)
		}
	case "ledger":
		s.handleLedger(w, r, wsID)
	default:
		s.writeError(w, 404, "not_found", "unknown sub-resource", map[string]any{"path": parts[1]})
	}
}

func (s *Server) handleWorkspaceSingle(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		ws, err := s.cfg.Service.Metadata.GetWorkspace(r.Context(), id)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, ws)
	case http.MethodPut:
		var req api.CreateWorkspaceRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		ws := &types.Workspace{ID: id, Name: req.Name, Region: req.Region, ChunkerID: req.ChunkerID, EmbeddingModel: req.EmbeddingModel, Meta: req.Meta}
		if err := s.cfg.Service.Metadata.UpdateWorkspace(r.Context(), ws); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, ws)
	case http.MethodDelete:
		if err := s.cfg.Service.Metadata.DeleteWorkspace(r.Context(), id); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/collections[/{coll_id}]
func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 1 && !s.validID(w, "collection_id", rest[0]) {
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		coll, err := s.cfg.Service.Metadata.ListCollections(r.Context(), wsID)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, map[string]any{"collections": coll})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var req api.CreateCollectionRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		c := &types.Collection{WorkspaceID: wsID, Name: req.Name}
		if err := s.cfg.Service.Metadata.CreateCollection(r.Context(), c); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 201, c)
	case len(rest) == 1 && r.Method == http.MethodGet:
		c, err := s.cfg.Service.Metadata.GetCollection(r.Context(), rest[0])
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, c)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if err := s.cfg.Service.Metadata.DeleteCollection(r.Context(), rest[0]); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/memories[/{mem_id}[:append|/edges|/tags/...|@{wmk}|/watermarks]]
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		s.handleImprint(w, r, wsID)
	case len(rest) == 0 && r.Method == http.MethodGet:
		s.handleListMemories(w, r, wsID)
	case len(rest) >= 1:
		// rest[0] is a memory ID, possibly with an ':append' or '@{wmk}' suffix.
		seg := rest[0]
		if idx := strings.Index(seg, ":append"); idx >= 0 {
			if s.validID(w, "memory_id", seg[:idx]) {
				s.handleAppend(w, r, wsID, seg[:idx])
			}
			return
		}
		memID, wmk, ok := parseMemoryAtWatermark(seg)
		if ok && len(rest) == 1 && r.Method == http.MethodGet {
			if s.validID(w, "memory_id", memID) && s.validID(w, "watermark", wmk) {
				s.handleGetAtWatermark(w, r, wsID, memID, wmk)
			}
			return
		}
		memID = seg
		if !s.validID(w, "memory_id", memID) {
			return
		}
		if len(rest) == 1 {
			switch r.Method {
			case http.MethodGet:
				s.handleLookup(w, r, wsID, memID)
			case http.MethodPut:
				s.handleUpdate(w, r, wsID, memID)
			case http.MethodPatch:
				s.handlePatch(w, r, wsID, memID)
			case http.MethodDelete:
				s.handleForget(w, r, wsID, memID)
			default:
				s.writeError(w, 405, "method_not_allowed", "", nil)
			}
			return
		}
		// sub-resources
		switch rest[1] {
		case "edges":
			s.handleMemoryEdges(w, r, wsID, memID, rest[2:])
		case "watermarks":
			s.handleWatermarks(w, r, wsID, memID)
		case "tags":
			s.handleTags(w, r, wsID, memID, rest[2:])
		default:
			s.writeError(w, 404, "not_found", "unknown memory sub-resource", nil)
		}
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

func parseMemoryAtWatermark(seg string) (memID, wmk string, ok bool) {
	idx := strings.Index(seg, "@")
	if idx <= 0 {
		return "", "", false
	}
	return seg[:idx], seg[idx+1:], true
}

func (s *Server) handleImprint(w http.ResponseWriter, r *http.Request, wsID string) {
	var req api.ImprintRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Imprint(r.Context(), wsID, agentFrom(r.Context()), req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 201, resp)
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request, wsID string) {
	if !s.validOptionalID(w, "collection_id", r.URL.Query().Get("collection_id")) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	mems, next, err := s.cfg.Service.Metadata.ListMemories(r.Context(), wsID, r.URL.Query().Get("collection_id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	if mems == nil {
		mems = []types.Memory{}
	}
	resp := map[string]any{"memories": mems}
	if next != "" {
		resp["next_cursor"] = next
	}
	s.writeJSON(w, 200, resp)
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request, wsID, id string) {
	m, err := s.cfg.Service.Metadata.GetMemory(r.Context(), id)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	if m.WorkspaceID != wsID {
		s.writeError(w, 404, "not_found", "memory not found", nil)
		return
	}
	cells, _ := s.cfg.Service.Metadata.GetCells(r.Context(), wsID, id)
	s.writeJSON(w, 200, api.MemoryEnvelope{Memory: m, Cells: cells})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request, wsID, id string) {
	var req api.UpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Update(r.Context(), wsID, id, agentFrom(r.Context()), r.Header.Get("If-Match"), req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, resp)
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request, wsID, id string) {
	var req api.PatchRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Patch(r.Context(), wsID, id, agentFrom(r.Context()), r.Header.Get("If-Match"), req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, resp)
}

func (s *Server) handleAppend(w http.ResponseWriter, r *http.Request, wsID, id string) {
	var req api.AppendRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Append(r.Context(), wsID, id, agentFrom(r.Context()), r.Header.Get("If-Match"), req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, resp)
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request, wsID, id string) {
	resp, err := s.cfg.Service.Forget(r.Context(), wsID, id, agentFrom(r.Context()))
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	if r.URL.Query().Get("redact_audit") == "true" && s.cfg.Service.Ledger != nil {
		caps := s.cfg.Service.Ledger.Capabilities()
		if caps.SupportsRedaction && caps.SupportsQuery {
			entries, _, _ := s.cfg.Service.Ledger.Query(r.Context(), adapter.LedgerQuery{
				WorkspaceID: wsID, MemoryID: id, Limit: 1000,
			})
			for _, e := range entries {
				_ = s.cfg.Service.Ledger.Redact(r.Context(), e.LedgerID, []string{"metadata", "ip", "user_agent"})
			}
		}
	}
	s.writeJSON(w, 200, resp)
}

func (s *Server) handleGetAtWatermark(w http.ResponseWriter, r *http.Request, wsID, memID, wmk string) {
	m, err := s.cfg.Service.Metadata.GetMemoryAtWatermark(r.Context(), memID, wmk)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	if m.WorkspaceID != wsID {
		s.writeError(w, 404, "not_found", "memory not found", nil)
		return
	}
	s.writeJSON(w, 200, m)
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request, wsID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	var req api.RecallRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Recall(r.Context(), wsID, req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, resp)
}

// /v1/search — node-wide search. In multi-tenant mode the request is
// pinned to the X-Memora-Workspace tenant and refused without one:
// node-wide must never mean cross-tenant, so the pin fails closed.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	var req api.SearchRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	tenant, _ := r.Context().Value(ctxKeyWorkspace).(string)
	if req.Federated && s.cfg.Mode == "multi-tenant" {
		// Peers search their whole node; a tenant must not reach that.
		s.writeError(w, 403, "federation_multi_tenant", "federated search is not available in multi-tenant mode", nil)
		return
	}
	switch {
	case tenant != "":
		req.WorkspaceIDs = []string{tenant}
	case s.cfg.Mode == "multi-tenant":
		s.writeError(w, 400, "missing_workspace", "X-Memora-Workspace is required in multi-tenant mode", nil)
		return
	}
	resp, err := s.cfg.Service.Search(r.Context(), req)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, resp)
}

// /v1/workspaces/{ws_id}/recall/pins[/{pin_id}]
func (s *Server) handlePins(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 1 && !s.validID(w, "pin_id", rest[0]) {
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		pins, err := s.cfg.Service.Metadata.ListPins(r.Context(), wsID)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		entries := make([]api.PinEntry, len(pins))
		for i, p := range pins {
			entries[i] = api.PinEntry{
				PinID: p.PinID, Query: p.Query, Mode: p.Mode, K: p.K,
				Watermark: p.Watermark, Label: p.Label, CreatedBy: p.CreatedBy,
				CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
			}
		}
		s.writeJSON(w, 200, api.PinListResponse{Pins: entries})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var req api.PinRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		if req.Query == "" {
			s.writeError(w, 400, "invalid_input", "query is required", nil)
			return
		}
		if req.Mode == "" {
			req.Mode = "hybrid"
		}
		if req.K <= 0 {
			req.K = 10
		}
		p := &types.Pin{
			WorkspaceID: wsID,
			Query:       req.Query,
			Mode:        req.Mode,
			K:           req.K,
			Watermark:   req.Watermark,
			Label:       req.Label,
			CreatedBy:   agentFrom(r.Context()),
		}
		if err := s.cfg.Service.Metadata.CreatePin(r.Context(), p); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 201, api.PinResponse{PinID: p.PinID, Watermark: p.Watermark})
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if err := s.cfg.Service.Metadata.DeletePin(r.Context(), wsID, rest[0]); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/agents[/{agent_id}]
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 1 && !s.validID(w, "agent_id", rest[0]) {
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		ags, err := s.cfg.Service.Metadata.ListAgents(r.Context(), wsID, 0)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, map[string]any{"agents": ags})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var req api.RegisterAgentRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		a := &types.Agent{
			AgentID:          req.AgentID,
			WorkspaceID:      wsID,
			DisplayName:      req.DisplayName,
			IdentityProvider: req.IdentityProvider,
			IdentityProof:    req.IdentityProof,
			AgentType:        req.AgentType,
			Model:            req.Model,
			Capabilities:     req.Capabilities,
		}
		if err := s.cfg.Service.Metadata.RegisterAgent(r.Context(), a); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 201, a)
	case len(rest) == 1 && r.Method == http.MethodGet:
		a, err := s.cfg.Service.Metadata.GetAgent(r.Context(), wsID, rest[0])
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, a)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if err := s.cfg.Service.Metadata.DeactivateAgent(r.Context(), wsID, rest[0]); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/memories/{mem_id}/edges (GET/POST)
func (s *Server) handleMemoryEdges(w http.ResponseWriter, r *http.Request, wsID, memID string, rest []string) {
	switch r.Method {
	case http.MethodGet:
		direction := r.URL.Query().Get("direction")
		if direction == "" {
			direction = "both"
		}
		typesParam := r.URL.Query().Get("type")
		var et []string
		if typesParam != "" {
			et = strings.Split(typesParam, ",")
		}
		edges, headers, err := s.cfg.Service.GraphNeighbors(r.Context(), wsID, memID,
			adapter.NeighborsOpts{Direction: api.GraphDirection(direction), EdgeTypes: et})
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, api.NeighborsResponse{Edges: edges, Neighbors: headers})
	case http.MethodPost:
		var req api.LinkRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		e := types.Edge{
			WorkspaceID:      wsID,
			SourceMemoryID:   memID,
			TargetMemoryID:   req.TargetMemoryID,
			EdgeType:         types.EdgeType(req.EdgeType),
			PropertiesJSON:   req.Properties,
			CreatedByAgentID: agentFrom(r.Context()),
		}
		out, err := s.cfg.Service.GraphLink(r.Context(), e)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		ledgerID := s.cfg.Service.AppendLedger(r.Context(), api.LedgerEntry{
			WorkspaceID: wsID, Op: "link", Target: out.EdgeID, AgentID: agentFrom(r.Context()),
			WatermarkAfter: out.Watermark, Metadata: map[string]any{"edge_type": string(out.EdgeType)},
		})
		s.writeJSON(w, 201, api.LinkResponse{Edge: out, LedgerID: ledgerID})
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/edges/{edge_id} (DELETE) or /edges:batch (POST)
func (s *Server) handleEdges(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 0 {
		s.writeError(w, 404, "not_found", "missing edge id or :batch", nil)
		return
	}
	seg := rest[0]
	if seg == ":batch" {
		if r.Method != http.MethodPost {
			s.writeError(w, 405, "method_not_allowed", "", nil)
			return
		}
		var req api.LinkBatchRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		agent := agentFrom(r.Context())
		edges := make([]types.Edge, len(req.Edges))
		for i, e := range req.Edges {
			edges[i] = types.Edge{
				WorkspaceID:      wsID,
				SourceMemoryID:   e.SourceMemoryID,
				TargetMemoryID:   e.TargetMemoryID,
				EdgeType:         types.EdgeType(e.EdgeType),
				PropertiesJSON:   e.Properties,
				CreatedByAgentID: agent,
			}
		}
		results, err := s.cfg.Service.GraphLinkBatch(r.Context(), edges)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		apiResults := make([]api.LinkResult, len(results))
		ok, failed := 0, 0
		for i, r := range results {
			apiResults[i] = api.LinkResult{Index: r.Index, Status: r.Status, EdgeID: r.EdgeID}
			if r.Error != nil {
				apiResults[i].Error = r.Error.Error()
			}
			if r.Status == "ok" {
				ok++
			} else {
				failed++
			}
		}
		s.writeJSON(w, 200, api.LinkBatchResponse{Results: apiResults, OK: ok, Failed: failed})
		return
	}
	if r.Method != http.MethodDelete {
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	if !s.validID(w, "edge_id", seg) {
		return
	}
	if err := s.cfg.Service.GraphUnlink(r.Context(), wsID, seg, agentFrom(r.Context())); err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	w.WriteHeader(204)
}

// /v1/workspaces/{ws_id}/graph/(neighbors|traverse|stats)
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 0 {
		s.writeError(w, 404, "not_found", "missing graph sub-resource", nil)
		return
	}
	switch rest[0] {
	case "neighbors":
		if r.Method != http.MethodPost {
			s.writeError(w, 405, "method_not_allowed", "", nil)
			return
		}
		var req api.NeighborsRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		if !s.validID(w, "memory_id", req.MemoryID) {
			return
		}
		edges, headers, err := s.cfg.Service.GraphNeighbors(r.Context(), wsID, req.MemoryID,
			adapter.NeighborsOpts{Direction: req.Direction, EdgeTypes: req.EdgeTypes, K: req.K})
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, api.NeighborsResponse{Edges: edges, Neighbors: headers})
	case "traverse":
		if r.Method != http.MethodPost {
			s.writeError(w, 405, "method_not_allowed", "", nil)
			return
		}
		var req api.TraverseRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, 400, "invalid_input", err.Error(), nil)
			return
		}
		if !s.validID(w, "seed_memory_id", req.SeedMemoryID) {
			return
		}
		tr, err := s.cfg.Service.GraphTraverse(r.Context(), wsID, req.SeedMemoryID,
			adapter.TraverseOpts{Depth: req.Depth, Direction: req.Direction, EdgeTypes: req.EdgeTypes, Filter: req.Filter})
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		// Translate to api shape.
		var layers [][]api.TraverseHit
		for _, l := range tr.Layers {
			lyr := make([]api.TraverseHit, len(l))
			for i, h := range l {
				lyr[i] = api.TraverseHit{MemoryID: h.Memory.MemoryID, ViaEdgeID: h.ViaEdgeID, ViaEdgeType: h.ViaEdgeType}
			}
			layers = append(layers, lyr)
		}
		s.writeJSON(w, 200, api.TraverseResponse{Seed: tr.Seed, Layers: layers, Stats: tr.Stats})
	case "stats":
		if r.Method != http.MethodGet {
			s.writeError(w, 405, "method_not_allowed", "", nil)
			return
		}
		n, byType, err := s.cfg.Service.GraphStats(r.Context(), wsID)
		if err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		s.writeJSON(w, 200, api.GraphStatsResponse{NodeCount: n, EdgeCountByType: byType})
	default:
		s.writeError(w, 404, "not_found", "unknown graph endpoint", nil)
	}
}

// /v1/workspaces/{ws_id}/memories/{mem_id}/watermarks
func (s *Server) handleWatermarks(w http.ResponseWriter, r *http.Request, wsID, memID string) {
	if r.Method != http.MethodGet {
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	hist, err := s.cfg.Service.Metadata.GetWatermarkHistory(r.Context(), wsID, memID, time.Now().AddDate(0, 0, -7))
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"watermarks": hist})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, wsID, memID string, rest []string) {
	if len(rest) != 1 {
		s.writeError(w, 404, "not_found", "tag key required", nil)
		return
	}
	key := rest[0]
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var body struct {
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.cfg.Service.Metadata.UpsertTag(r.Context(), wsID, memID, key, body.Value); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	case http.MethodDelete:
		if err := s.cfg.Service.Metadata.DeleteTag(r.Context(), wsID, memID, key); err != nil {
			s.writeErrorFromService(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		s.writeError(w, 405, "method_not_allowed", "", nil)
	}
}

// /v1/workspaces/{ws_id}/ledger
func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request, wsID string) {
	if r.Method != http.MethodGet {
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	if !s.validOptionalID(w, "memory_id", r.URL.Query().Get("memory_id")) ||
		!s.validOptionalID(w, "since_ledger_id", r.URL.Query().Get("since_ledger_id")) {
		return
	}
	if s.cfg.Service.Ledger == nil {
		s.writeError(w, 501, "capability_unavailable", "no LedgerStore is configured", nil)
		return
	}
	caps := s.cfg.Service.Ledger.Capabilities()
	if !caps.SupportsQuery {
		s.writeError(w, 501, "capability_unavailable", "configured LedgerStore does not support query", nil)
		return
	}
	q := adapter.LedgerQuery{WorkspaceID: wsID}
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err == nil {
			q.Since = &t
		}
	}
	if v := r.URL.Query().Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err == nil {
			q.Until = &t
		}
	}
	q.Actor = r.URL.Query().Get("actor")
	q.AgentID = r.URL.Query().Get("agent_id")
	q.MemoryID = r.URL.Query().Get("memory_id")
	q.EdgeID = r.URL.Query().Get("edge_id")
	if v := r.URL.Query().Get("limit"); v != "" {
		q.Limit, _ = strconv.Atoi(v)
	}
	q.SinceLedgerID = r.URL.Query().Get("since_ledger_id")
	if v := r.URL.Query().Get("op"); v != "" {
		q.Op = strings.Split(v, ",")
	}
	entries, next, err := s.cfg.Service.Ledger.Query(r.Context(), q)
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	s.writeJSON(w, 200, api.LedgerResponse{Entries: entries, NextCursor: next})
}

// validID writes a 400 and returns false unless id is a UUID. field
// names the parameter in the error, since a UUID doesn't say what kind
// of thing it points at.
func (s *Server) validID(w http.ResponseWriter, field, id string) bool {
	if _, err := types.ParseID(field, id); err != nil {
		s.writeErrorFromService(w, err)
		return false
	}
	return true
}

// validOptionalID is validID for optional parameters: empty is allowed.
func (s *Server) validOptionalID(w http.ResponseWriter, field, id string) bool {
	return id == "" || s.validID(w, field, id)
}

// Used by handlers to bubble named errors up.
var _ = errors.New

// handleFederationSearch is POST /v1/federation/search, called by an
// authenticated peer (the middleware has run AuthenticatePeer, including
// the loop guard). It searches every workspace on this node with this
// node's own embedder and never fans out further: federation is one hop.
func (s *Server) handleFederationSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return
	}
	peer := peerFrom(r.Context())
	if peer == nil { // unreachable: the middleware authenticates this route
		s.writeError(w, 401, "federation_unauthorized", "peer authentication required", nil)
		return
	}
	if s.cfg.Mode == "multi-tenant" {
		// Peers are trusted per node, not per tenant; a node-wide search
		// would cross tenants, so it fails closed.
		s.writeError(w, 403, "federation_multi_tenant", "federation search is not available in multi-tenant mode", nil)
		return
	}
	if !s.inbound.Allow(peer.PeerID) {
		s.ledgerRejected(r, peer.PeerID, "rate_limited")
		w.Header().Set("Retry-After", s.inboundRetryAfter())
		s.writeError(w, 429, "rate_limited", "peer rate limit exceeded", nil)
		return
	}
	var req api.FederationSearchRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, 400, "invalid_input", err.Error(), nil)
		return
	}
	resp, err := s.cfg.Service.Search(r.Context(), api.SearchRequest{
		Query: req.Query, Mode: req.Mode, K: req.K,
		IncludeContent: req.IncludeContent, MaxContentBytes: req.MaxContentBytes,
	})
	if err != nil {
		s.writeErrorFromService(w, err)
		return
	}
	for i := range resp.Results {
		resp.Results[i].NodeID = s.cfg.Service.NodeID
		resp.Results[i].NodeName = s.cfg.Service.NodeName
	}
	s.writeJSON(w, 200, resp)
}

// ledgerRejected records a refused peer call as a node-level ledger entry
// (no workspace). F-8 extends this to every rejection reason.
func (s *Server) ledgerRejected(r *http.Request, peerID, reason string) {
	if s.cfg.Service == nil {
		return
	}
	e := federation.NewRejectedEntry("", peerID, reason)
	e.Target = r.URL.Path
	s.cfg.Service.AppendLedger(r.Context(), e)
}

// inboundRetryAfter is the Retry-After for a rate-limited peer: the
// seconds until the limiter refills one token, at least 1.
func (s *Server) inboundRetryAfter() string {
	qps := s.cfg.FederationInboundQPS
	if qps <= 0 {
		qps = federation.DefaultInboundQPS
	}
	return strconv.Itoa(int(math.Max(1, math.Ceil(1/qps))))
}
