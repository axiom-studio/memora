package types

import "time"

// Workspace is the top-level Memora container — one customer, many
// workspaces (e.g. Prod / Staging / Per-Customer-Tenant).
type Workspace struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Region         string         `json:"region,omitempty"`
	ChunkerID      string         `json:"chunker_id,omitempty"`
	EmbeddingModel string         `json:"embedding_model,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`

	AutoLinkEnabled           bool    `json:"auto_link_enabled"`
	AutoLinkThreshold         float64 `json:"auto_link_threshold,omitempty"`
	AutoLinkMaxEdges          int     `json:"auto_link_max_edges,omitempty"`
	AutoLinkMaxIncomingPerDay int     `json:"auto_link_max_incoming_per_day,omitempty"`
}

const (
	AutoLinkDefaultThreshold         = 0.7
	AutoLinkDefaultMaxEdges          = 10
	AutoLinkDefaultMaxIncomingPerDay = 100
	AutoLinkHardCapMaxEdges          = 50
)

// AutoLinkDefaults fills zero-valued auto-link fields with defaults and
// clamps values to valid ranges.
func (w *Workspace) AutoLinkDefaults() {
	if w.AutoLinkThreshold <= 0 {
		w.AutoLinkThreshold = AutoLinkDefaultThreshold
	}
	if w.AutoLinkThreshold > 1.0 {
		w.AutoLinkThreshold = 1.0
	}
	if w.AutoLinkMaxEdges <= 0 {
		w.AutoLinkMaxEdges = AutoLinkDefaultMaxEdges
	}
	if w.AutoLinkMaxEdges > AutoLinkHardCapMaxEdges {
		w.AutoLinkMaxEdges = AutoLinkHardCapMaxEdges
	}
	if w.AutoLinkMaxIncomingPerDay <= 0 {
		w.AutoLinkMaxIncomingPerDay = AutoLinkDefaultMaxIncomingPerDay
	}
}

// Validate returns an error if the workspace is malformed.
func (w *Workspace) Validate() error {
	if w.ID != "" {
		if err := MustHavePrefix(w.ID, WorkspaceIDPrefix); err != nil {
			return err
		}
	}
	if w.Name == "" {
		return errEmpty("workspace.name")
	}
	return nil
}

// Collection is a logical grouping inside a Workspace (e.g. "Customer
// Conversations", "Knowledge Base"). Maps to vibeflow's `feature`.
type Collection struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
}

// Validate returns an error if the collection is malformed.
func (c *Collection) Validate() error {
	if c.ID != "" {
		if err := MustHavePrefix(c.ID, CollectionIDPrefix); err != nil {
			return err
		}
	}
	if err := MustHavePrefix(c.WorkspaceID, WorkspaceIDPrefix); err != nil {
		return err
	}
	if c.Name == "" {
		return errEmpty("collection.name")
	}
	return nil
}
