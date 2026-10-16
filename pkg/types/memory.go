package types

import (
	"crypto/md5"
	"encoding/hex"
	"time"
)

// Memory is the primary unit of stored content — a typed, versioned
// blob of text + structured metadata. It is also a node in the
// Context Graph.
type Memory struct {
	ID                    string            `json:"id"`
	WorkspaceID           string            `json:"workspace_id"`
	CollectionID          string            `json:"collection_id,omitempty"`
	Content               string            `json:"content"`
	ContentMD5            string            `json:"content_md5"`
	HeadWatermark         string            `json:"head_watermark"`
	CreatedWatermark      string            `json:"created_watermark"`
	WrittenByAgentID      string            `json:"written_by_agent_id"`
	LastModifiedByAgentID string            `json:"last_modified_by_agent_id"`
	Tags                  map[string]string `json:"tags,omitempty"`
	RecallReady           bool              `json:"recall_ready"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	DeletedAt             *time.Time        `json:"deleted_at,omitempty"`
}

// Validate returns an error if the memory is malformed.
func (m *Memory) Validate() error {
	if m.ID != "" {
		if err := MustHavePrefix(m.ID, MemoryIDPrefix); err != nil {
			return err
		}
	}
	if err := MustHavePrefix(m.WorkspaceID, WorkspaceIDPrefix); err != nil {
		return err
	}
	if m.CollectionID != "" {
		if err := MustHavePrefix(m.CollectionID, CollectionIDPrefix); err != nil {
			return err
		}
	}
	if m.WrittenByAgentID == "" {
		return errEmpty("memory.written_by_agent_id")
	}
	return nil
}

// MD5Hex returns the canonical content_md5 value for a piece of content.
func MD5Hex(content string) string {
	sum := md5.Sum([]byte(content)) //nolint:gosec // MD5 is used here as a content checksum, not for security.
	return hex.EncodeToString(sum[:])
}

// Cell is a single addressable chunk within a Memory. Cells are what
// get embedded — embeddings are at the Cell level so semantic search
// returns the smallest relevant unit. A 50 KB Memory typically splits
// into ~25 Cells.
type Cell struct {
	CellID           string         `json:"cell_id"`
	MemoryID         string         `json:"memory_id"`
	Seq              int            `json:"seq"`
	Text             string         `json:"text"`
	TextMD5          string         `json:"text_md5"`
	WrittenByAgentID string         `json:"written_by_agent_id"`
	EmbeddingModel   string         `json:"embedding_model,omitempty"`
	VectorKey        string         `json:"vector_key,omitempty"`
	MetadataJSON     map[string]any `json:"metadata_json,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

// Validate returns an error if the cell is malformed.
func (c *Cell) Validate() error {
	if c.CellID != "" {
		if err := MustHavePrefix(c.CellID, CellIDPrefix); err != nil {
			return err
		}
	}
	if err := MustHavePrefix(c.MemoryID, MemoryIDPrefix); err != nil {
		return err
	}
	if c.Seq < 0 {
		return errf("cell.seq must be >= 0, got %d", c.Seq)
	}
	return nil
}

// Tag is a key/value attribute on a Memory. Filterable in Recall.
type Tag struct {
	MemoryID string `json:"memory_id"`
	Key      string `json:"key"`
	Value    string `json:"value"`
}

// CellDelta describes the effect of a write on a single Cell — used
// by Patch to track which Cells got re-embedded (the moat).
type CellDelta struct {
	CellID       string `json:"cell_id"`
	Seq          int    `json:"seq"`
	Op           string `json:"op"` // "added" | "modified" | "removed" | "unchanged"
	OldTextMD5   string `json:"old_text_md5,omitempty"`
	NewTextMD5   string `json:"new_text_md5,omitempty"`
	NeedsReembed bool   `json:"needs_reembed"`
}
