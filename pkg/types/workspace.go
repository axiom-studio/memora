package types

import "time"

// Workspace is the top-level Memora container — one customer, many
// workspaces (e.g. Prod / Staging / Per-Customer-Tenant).
type Workspace struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Region        string         `json:"region,omitempty"`
	ChunkerID     string         `json:"chunker_id,omitempty"`
	EmbeddingModel string        `json:"embedding_model,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
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
