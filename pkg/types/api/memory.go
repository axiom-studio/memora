// Package api defines the wire-format request and response types for
// every public Memora verb. These are the structs that get marshalled
// to/from JSON on the REST surface and embedded as MCP tool input
// schemas.
package api

import "github.com/axiom-studio/memora/pkg/types"

// ImprintRequest creates a new Memory.
type ImprintRequest struct {
	CollectionID  string            `json:"collection_id,omitempty"`
	Content       string            `json:"content"`
	Tags          map[string]string `json:"tags,omitempty"`
	ChunkerID     string            `json:"chunker_id,omitempty"`
	ChunkerConfig map[string]string `json:"chunker_config,omitempty"`
	AutoLink      *bool             `json:"auto_link,omitempty"`
}

// ImprintResponse is returned synchronously after Imprint commits.
type ImprintResponse struct {
	MemoryID         string `json:"memory_id"`
	Watermark        string `json:"watermark"`
	ContentMD5       string `json:"content_md5"`
	CellsCreated     int    `json:"cells_created"`
	RecallReady      bool   `json:"recall_ready"`
	AutoLinkedEdges  int    `json:"auto_linked_edges,omitempty"`
	WrittenByAgentID string `json:"written_by_agent_id"`
	LedgerID         string `json:"ledger_id"`
	LatencyMS        int    `json:"latency_ms"`
}

// UpdateRequest replaces a Memory's content. CAS via expected_watermark
// (alternatively via the If-Match HTTP header).
type UpdateRequest struct {
	Content           string            `json:"content"`
	Tags              map[string]string `json:"tags,omitempty"`
	ExpectedWatermark string            `json:"expected_watermark,omitempty"`
}

// UpdateResponse mirrors ImprintResponse plus delta info.
type UpdateResponse struct {
	MemoryID              string `json:"memory_id"`
	Watermark             string `json:"watermark"`
	ContentMD5            string `json:"content_md5"`
	CellsReembed          int    `json:"cells_re_embedded"`
	CellsSkipped          int    `json:"cells_skipped"`
	LastModifiedByAgentID string `json:"last_modified_by_agent_id"`
	LedgerID              string `json:"ledger_id"`
	LatencyMS             int    `json:"latency_ms"`
}

// PatchOp is one find-and-replace operation. Identical contract to
// vibeflow's AXIOMCLOUD-454 patch protocol.
type PatchOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// PatchRequest applies a sequence of ops to a Memory.
type PatchRequest struct {
	Patch             []PatchOp `json:"patch"`
	ExpectedWatermark string    `json:"expected_watermark,omitempty"`
}

// PatchResponse documents what the patch did and — crucially — how
// many cells got re-embedded vs skipped (the moat metric).
type PatchResponse struct {
	MemoryID              string `json:"memory_id"`
	Watermark             string `json:"watermark"`
	ContentMD5            string `json:"content_md5"`
	PatchesApplied        int    `json:"patches_applied"`
	CellsReembed          int    `json:"cells_re_embedded"`
	CellsSkipped          int    `json:"cells_skipped"`
	CellsAdded            int    `json:"cells_added,omitempty"`
	CellsRemoved          int    `json:"cells_removed,omitempty"`
	LastModifiedByAgentID string `json:"last_modified_by_agent_id"`
	LedgerID              string `json:"ledger_id"`
	LatencyMS             int    `json:"latency_ms"`
}

// AppendRequest tacks content onto an existing Memory.
type AppendRequest struct {
	Content           string `json:"content"`
	ExpectedWatermark string `json:"expected_watermark,omitempty"`
}

// AppendResponse mirrors PatchResponse but only cells_added is set.
type AppendResponse struct {
	MemoryID              string `json:"memory_id"`
	Watermark             string `json:"watermark"`
	ContentMD5            string `json:"content_md5"`
	CellsAdded            int    `json:"cells_added"`
	LastModifiedByAgentID string `json:"last_modified_by_agent_id"`
	LedgerID              string `json:"ledger_id"`
	LatencyMS             int    `json:"latency_ms"`
}

// ForgetResponse is returned by DELETE /memories/{id}.
type ForgetResponse struct {
	MemoryID      string `json:"memory_id"`
	Watermark     string `json:"watermark"`
	CascadedEdges int    `json:"cascaded_edges"`
	LedgerID      string `json:"ledger_id"`
}

// MemoryEnvelope is the response shape for GET /memories/{id} — the
// full Memory struct plus optional cells.
type MemoryEnvelope struct {
	*types.Memory
	Cells []types.Cell `json:"cells,omitempty"`
}
