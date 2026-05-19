package types

import "time"

// EdgeType is the closed enum of stored edge types.
// Derived edge types (embedding_neighbor, tag_co_occurrence) are
// computed at query time and never stored.
type EdgeType string

const (
	// EdgeTypeParentOf — hierarchical containment.
	EdgeTypeParentOf EdgeType = "parent_of"
	// EdgeTypeDerivedFrom — Memory was created from another via
	// summarization, split, refinement, or import.
	EdgeTypeDerivedFrom EdgeType = "derived_from"
	// EdgeTypeSupersedes — new Memory replaces an older one.
	EdgeTypeSupersedes EdgeType = "supersedes"
	// EdgeTypeReferences — soft curated link, the default
	// "these two memories are related" type.
	EdgeTypeReferences EdgeType = "references"
	// EdgeTypeSessionOf — Memory was written within a named session
	// anchor (the anchor itself is a Memory).
	EdgeTypeSessionOf EdgeType = "session_of"
	// EdgeTypeMentions — A's content references B's id.
	EdgeTypeMentions EdgeType = "mentions"
)

// StoredEdgeTypes is the canonical list of edge types persisted in
// memora_edges. New types require a Memora release.
var StoredEdgeTypes = []EdgeType{
	EdgeTypeParentOf,
	EdgeTypeDerivedFrom,
	EdgeTypeSupersedes,
	EdgeTypeReferences,
	EdgeTypeSessionOf,
	EdgeTypeMentions,
}

// ValidEdgeType returns true if the given string is a known stored edge type.
func ValidEdgeType(s string) bool {
	switch EdgeType(s) {
	case EdgeTypeParentOf, EdgeTypeDerivedFrom, EdgeTypeSupersedes,
		EdgeTypeReferences, EdgeTypeSessionOf, EdgeTypeMentions:
		return true
	default:
		return false
	}
}

// Edge is a typed, directed link between two Memories within a single
// Workspace. Caller-declared, not auto-extracted.
type Edge struct {
	EdgeID           string         `json:"edge_id"`
	WorkspaceID      string         `json:"workspace_id"`
	SourceMemoryID   string         `json:"source_memory_id"`
	TargetMemoryID   string         `json:"target_memory_id"`
	EdgeType         EdgeType       `json:"edge_type"`
	PropertiesJSON   map[string]any `json:"properties_json,omitempty"`
	CreatedByAgentID string         `json:"created_by_agent_id"`
	CreatedAt        time.Time      `json:"created_at"`
	DeletedAt        *time.Time     `json:"deleted_at,omitempty"`
	Watermark        string         `json:"watermark"`
}

// Validate returns an error if the edge is malformed.
func (e *Edge) Validate() error {
	if e.EdgeID != "" {
		if err := MustHavePrefix(e.EdgeID, EdgeIDPrefix); err != nil {
			return err
		}
	}
	if err := MustHavePrefix(e.WorkspaceID, WorkspaceIDPrefix); err != nil {
		return err
	}
	if err := MustHavePrefix(e.SourceMemoryID, MemoryIDPrefix); err != nil {
		return err
	}
	if err := MustHavePrefix(e.TargetMemoryID, MemoryIDPrefix); err != nil {
		return err
	}
	if e.SourceMemoryID == e.TargetMemoryID {
		return errf("edge source == target (%s); self-loops disallowed in v1", e.SourceMemoryID)
	}
	if !ValidEdgeType(string(e.EdgeType)) {
		return errf("edge_type %q is not in the closed enum; valid: %v", e.EdgeType, StoredEdgeTypes)
	}
	if e.CreatedByAgentID == "" {
		return errEmpty("edge.created_by_agent_id")
	}
	return nil
}

// MemoryHeader is a lightweight projection of a Memory used in
// Neighbors and Traverse responses — full content is omitted to keep
// graph results compact.
type MemoryHeader struct {
	MemoryID      string            `json:"memory_id"`
	WorkspaceID   string            `json:"workspace_id"`
	CollectionID  string            `json:"collection_id,omitempty"`
	ContentMD5    string            `json:"content_md5"`
	HeadWatermark string            `json:"head_watermark"`
	Tags          map[string]string `json:"tags,omitempty"`
}
