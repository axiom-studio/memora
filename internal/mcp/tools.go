package mcp

// toolCatalog returns the 25 OSS MCP tools, in display order. Schemas
// are JSON Schema draft-07 fragments with the smallest precise set of
// constraints — the LLM-facing description is what drives correct
// usage, validation is best-effort.
func toolCatalog() []Tool {
	stringP := func() map[string]any { return map[string]any{"type": "string"} }
	intP := func(minimum, maximum, def int) map[string]any {
		return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum, "default": def}
	}

	return []Tool{
		{
			Name:        "memora_imprint",
			Description: "Create a new Memory. Requires workspace_id, content, agent_id. Tags are key/value strings. Use chunker_id to select a chunker (default, markdown, csv, jsonl, no-chunk). Use chunker_config to pass chunker-specific options (csv: rows_per_cell, has_header, delimiter; jsonl: lines_per_cell, validate).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":  stringP(),
					"agent_id":      stringP(),
					"collection_id": stringP(),
					"content":       stringP(),
					"tags":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"chunker_id":    stringP(),
					"chunker_config": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				},
				"required": []string{"workspace_id", "agent_id", "content"},
			},
		},
		{
			Name:        "memora_lookup",
			Description: "Fetch a Memory (with optional cells) by memory_id.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "memory_id": stringP()},
				"required":   []string{"workspace_id", "memory_id"},
			},
		},
		{
			Name:        "memora_update",
			Description: "Replace a Memory's content with optimistic concurrency via expected_watermark.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":       stringP(),
					"memory_id":          stringP(),
					"agent_id":           stringP(),
					"content":            stringP(),
					"expected_watermark": stringP(),
				},
				"required": []string{"workspace_id", "memory_id", "agent_id", "content", "expected_watermark"},
			},
		},
		{
			Name:        "memora_patch",
			Description: "Apply diff-op patches to a Memory. Each old_string must exist; with replace_all=false (default), it must be UNIQUE. Atomic — any anchor miss rejects the whole patch. Returns cells_re_embedded / cells_skipped — the moat metric.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":       stringP(),
					"memory_id":          stringP(),
					"agent_id":           stringP(),
					"expected_watermark": stringP(),
					"patch": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"old_string":  stringP(),
								"new_string":  stringP(),
								"replace_all": map[string]any{"type": "boolean", "default": false},
							},
							"required": []string{"old_string", "new_string"},
						},
					},
				},
				"required": []string{"workspace_id", "memory_id", "agent_id", "patch", "expected_watermark"},
			},
		},
		{
			Name:        "memora_append",
			Description: "Append content to an existing Memory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":       stringP(),
					"memory_id":          stringP(),
					"agent_id":           stringP(),
					"content":            stringP(),
					"expected_watermark": stringP(),
				},
				"required": []string{"workspace_id", "memory_id", "agent_id", "content"},
			},
		},
		{
			Name:        "memora_forget",
			Description: "Soft-delete a Memory and cascade incident edges.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "memory_id": stringP(), "agent_id": stringP()},
				"required":   []string{"workspace_id", "memory_id", "agent_id"},
			},
		},
		{
			Name:        "memora_recall",
			Description: "Search Memories with optional Context Graph expansion. mode = lookup | keyword | vector | hybrid (default).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id": stringP(),
					"query":        stringP(),
					"mode":         map[string]any{"type": "string", "enum": []string{"lookup", "keyword", "vector", "hybrid"}, "default": "hybrid"},
					"k":            intP(1, 100, 5),
					"filters": map[string]any{"type": "object",
						"properties": map[string]any{
							"collection_id": stringP(),
							"agent_id":      stringP(),
							"tags":          map[string]any{"type": "object"},
						},
					},
					"graph_expansion": map[string]any{"type": "object",
						"properties": map[string]any{
							"depth":      intP(0, 3, 0),
							"edge_types": map[string]any{"type": "array", "items": stringP()},
							"direction":  map[string]any{"type": "string", "enum": []string{"out", "in", "both"}, "default": "out"},
							"weight":     map[string]any{"type": "number", "default": 0.2},
							"max_neighbors": intP(0, 200, 10),
						},
					},
				},
				"required": []string{"workspace_id", "query"},
			},
		},
		{
			Name:        "memora_pin",
			Description: "Bind a Recall query to a watermark for reproducible reads.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "query": map[string]any{"type": "object"}, "watermark": stringP()},
				"required":   []string{"workspace_id", "query", "watermark"},
			},
		},
		{
			Name:        "memora_unpin",
			Description: "Release a previously-created pin.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pin_id": stringP()}, "required": []string{"pin_id"}},
		},
		{
			Name:        "memora_list_workspaces",
			Description: "List all visible workspaces.",
			InputSchema: map[string]any{"type": "object"},
		},
		{
			Name:        "memora_create_workspace",
			Description: "Create a workspace. Requires name.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": stringP(), "region": stringP(), "chunker_id": stringP(), "embedding_model": stringP()},
				"required":   []string{"name"},
			},
		},
		{
			Name:        "memora_get_workspace",
			Description: "Fetch a workspace by id.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workspace_id": stringP()}, "required": []string{"workspace_id"}},
		},
		{
			Name:        "memora_update_workspace",
			Description: "Update a workspace's mutable fields.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workspace_id": stringP(), "name": stringP()}, "required": []string{"workspace_id"}},
		},
		{
			Name:        "memora_delete_workspace",
			Description: "Delete a workspace (admin only; cascades).",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workspace_id": stringP()}, "required": []string{"workspace_id"}},
		},
		{
			Name:        "memora_list_collections",
			Description: "List collections inside a workspace.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workspace_id": stringP()}, "required": []string{"workspace_id"}},
		},
		{
			Name:        "memora_create_collection",
			Description: "Create a collection inside a workspace.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "name": stringP()},
				"required":   []string{"workspace_id", "name"},
			},
		},
		{
			Name:        "memora_delete_collection",
			Description: "Delete a collection.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"collection_id": stringP()}, "required": []string{"collection_id"}},
		},
		{
			Name:        "memora_list_memories",
			Description: "List memories in a workspace (optionally filtered by collection_id).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "collection_id": stringP(), "limit": intP(1, 200, 50)},
				"required":   []string{"workspace_id"},
			},
		},
		{
			Name:        "memora_get_watermark_history",
			Description: "Return the watermark history for a Memory (last 7 days in OSS).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workspace_id": stringP(), "memory_id": stringP()},
				"required":   []string{"workspace_id", "memory_id"},
			},
		},
		{
			Name:        "memora_register_agent",
			Description: "Register or update an agent in the workspace registry.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":      stringP(),
					"agent_id":          stringP(),
					"identity_provider": map[string]any{"type": "string", "enum": []string{"opaque", "anthropic_session", "a2a", "did", "oauth_agent", "oidc_agent"}},
					"display_name":      stringP(),
					"model":             stringP(),
				},
				"required": []string{"workspace_id", "agent_id", "identity_provider"},
			},
		},
		{
			Name:        "memora_list_agents",
			Description: "List agents registered in a workspace.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workspace_id": stringP()}, "required": []string{"workspace_id"}},
		},
		{
			Name:        "memora_link",
			Description: "Create a typed Context Graph edge from source_memory_id to target_memory_id. edge_type in {parent_of, derived_from, supersedes, references, session_of, mentions}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":     stringP(),
					"source_memory_id": stringP(),
					"target_memory_id": stringP(),
					"edge_type":        map[string]any{"type": "string", "enum": []string{"parent_of", "derived_from", "supersedes", "references", "session_of", "mentions"}},
					"agent_id":         stringP(),
					"properties_json":  map[string]any{"type": "object"},
				},
				"required": []string{"workspace_id", "source_memory_id", "target_memory_id", "edge_type", "agent_id"},
			},
		},
		{
			Name:        "memora_unlink",
			Description: "Soft-delete a Context Graph edge.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"edge_id": stringP(), "agent_id": stringP()},
				"required":   []string{"edge_id", "agent_id"},
			},
		},
		{
			Name:        "memora_neighbors",
			Description: "Return one-hop neighbors of a Memory. direction in {out, in, both}; edge_types filter optional; k caps results.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id": stringP(),
					"memory_id":    stringP(),
					"direction":    map[string]any{"type": "string", "enum": []string{"out", "in", "both"}, "default": "both"},
					"edge_types":   map[string]any{"type": "array", "items": stringP()},
					"k":            intP(1, 200, 50),
				},
				"required": []string{"workspace_id", "memory_id"},
			},
		},
		{
			Name:        "memora_traverse",
			Description: "BFS traversal from a seed Memory over the Context Graph. depth capped at 3 in OSS.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id":   stringP(),
					"seed_memory_id": stringP(),
					"depth":          intP(1, 3, 1),
					"direction":      map[string]any{"type": "string", "enum": []string{"out", "in", "both"}, "default": "out"},
					"edge_types":     map[string]any{"type": "array", "items": stringP()},
					"filter":         map[string]any{"type": "object"},
				},
				"required": []string{"workspace_id", "seed_memory_id", "depth"},
			},
		},
	}
}
