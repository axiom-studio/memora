package api

import (
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// RecallMode is the search strategy.
type RecallMode string

const (
	RecallModeLookup  RecallMode = "lookup"
	RecallModeKeyword RecallMode = "keyword"
	RecallModeVector  RecallMode = "vector"
	RecallModeHybrid  RecallMode = "hybrid"
)

// GraphDirection is the BFS direction over the Context Graph.
type GraphDirection string

const (
	GraphDirOut  GraphDirection = "out"
	GraphDirIn   GraphDirection = "in"
	GraphDirBoth GraphDirection = "both"
)

// RecallFilters is the WHERE-style filter pushed down to the adapters.
type RecallFilters struct {
	CollectionID string              `json:"collection_id,omitempty"`
	Tags         map[string][]string `json:"tags,omitempty"`
	TsAfter      *time.Time          `json:"ts_after,omitempty"`
	TsBefore     *time.Time          `json:"ts_before,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	AgentIDIn    []string            `json:"agent_id_in,omitempty"`
	AgentIDNotIn []string            `json:"agent_id_not_in,omitempty"`
}

// GraphExpansion extends Recall with a BFS walk over the Context Graph.
type GraphExpansion struct {
	Depth                      int            `json:"depth"`
	EdgeTypes                  []string       `json:"edge_types,omitempty"`
	Direction                  GraphDirection `json:"direction,omitempty"`
	Weight                     float64        `json:"weight,omitempty"`
	IncludeNeighborsInResponse bool           `json:"include_neighbors_in_response,omitempty"`
	MaxNeighbors               int            `json:"max_neighbors,omitempty"`
}

// RecallWeights overrides the default hybrid-mode weights.
type RecallWeights struct {
	Vector  float64 `json:"vector,omitempty"`
	Keyword float64 `json:"keyword,omitempty"`
}

// RecallRequest is the search query body.
type RecallRequest struct {
	Query        string        `json:"query"`
	Mode         RecallMode    `json:"mode,omitempty"`
	K            int           `json:"k,omitempty"`
	Filters      RecallFilters `json:"filters,omitempty"`
	Weights      RecallWeights `json:"weights,omitempty"`
	IncludeCells bool          `json:"include_cells,omitempty"`
	// IncludeContent adds each hit's full memory content, cut at
	// MaxContentBytes (default 64 KiB, at most 1 MiB).
	IncludeContent  bool            `json:"include_content,omitempty"`
	MaxContentBytes int             `json:"max_content_bytes,omitempty"`
	GraphExpansion  *GraphExpansion `json:"graph_expansion,omitempty"`
	MemoryIDs       []string        `json:"memory_ids,omitempty"` // for lookup mode
}

// GraphProvenance carries the edge a graph result came in through.
type GraphProvenance struct {
	FromMemoryID string `json:"from_memory_id"`
	EdgeID       string `json:"edge_id"`
	EdgeType     string `json:"edge_type"`
	Layer        int    `json:"layer"`
}

// RecallHit is one search result.
type RecallHit struct {
	MemoryID     string  `json:"memory_id"`
	WorkspaceID  string  `json:"workspace_id,omitempty"`
	CellID       string  `json:"cell_id,omitempty"`
	Score        float64 `json:"score"`
	VectorScore  float64 `json:"vector_score,omitempty"`
	KeywordScore float64 `json:"keyword_score,omitempty"`
	Text         string  `json:"text"` // short display excerpt
	// MatchedText is the matched cell's full text; CellID and CellIndex
	// locate it in the memory.
	MatchedText string     `json:"matched_text,omitempty"`
	CellIndex   *int       `json:"cell_index,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	// Content is the memory's full content (include_content), cut at
	// max_content_bytes when ContentTruncated.
	Content          string            `json:"content,omitempty"`
	ContentTruncated bool              `json:"content_truncated,omitempty"`
	Cells            []types.Cell      `json:"cells,omitempty"` // include_cells
	Tags             map[string]string `json:"tags,omitempty"`
	Watermark        string            `json:"watermark"`
	WrittenByAgentID string            `json:"written_by_agent_id,omitempty"`
	Via              string            `json:"via"` // "seed" | "graph" | "federation:<peer>"
	PeerID           string            `json:"peer_id,omitempty"`
	// NodeID and NodeName identify the node that holds the memory. A
	// node answering a peer's federation search sets them.
	NodeID          string           `json:"node_id,omitempty"`
	NodeName        string           `json:"node_name,omitempty"`
	GraphProvenance *GraphProvenance `json:"graph_provenance,omitempty"`
}

// RecallResponse is the full search result envelope.
type RecallResponse struct {
	Results []RecallHit `json:"results"`
	// Degraded lists the hybrid sources that failed (DegradedVector,
	// DegradedKeyword); results came from the other source alone.
	Degraded               []string       `json:"degraded,omitempty"`
	TotalCandidatesScanned int            `json:"total_candidates_scanned"`
	GraphNodesExpanded     int            `json:"graph_nodes_expanded,omitempty"`
	LatencyMS              int            `json:"latency_ms"`
	EmbeddingPending       bool           `json:"embedding_pending,omitempty"`
	FederationID           string         `json:"federation_id,omitempty"`
	PartialSuccess         bool           `json:"partial_success,omitempty"`
	FailedPeers            []string       `json:"failed_peers,omitempty"`
	PeerLatencyMS          map[string]int `json:"peer_latency_ms,omitempty"`
}

// PinRequest creates a saved recall query bound to a watermark.
type PinRequest struct {
	Query     string `json:"query"`
	Mode      string `json:"mode,omitempty"`
	K         int    `json:"k,omitempty"`
	Watermark string `json:"watermark"`
	Label     string `json:"label,omitempty"`
}

// PinResponse acknowledges a pin creation.
type PinResponse struct {
	PinID     string `json:"pin_id"`
	Watermark string `json:"watermark"`
}

// PinListResponse wraps a list of pins.
type PinListResponse struct {
	Pins []PinEntry `json:"pins"`
}

// PinEntry is a single pin in a list response.
type PinEntry struct {
	PinID     string `json:"pin_id"`
	Query     string `json:"query"`
	Mode      string `json:"mode,omitempty"`
	K         int    `json:"k,omitempty"`
	Watermark string `json:"watermark"`
	Label     string `json:"label,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at"`
}

// SearchRequest is POST /v1/search: a recall query across every
// workspace on the node, or the subset in WorkspaceIDs. Lookup mode does
// not apply; Mode is keyword, vector or hybrid (the default).
type SearchRequest struct {
	Query        string        `json:"query"`
	Mode         RecallMode    `json:"mode,omitempty"`
	K            int           `json:"k,omitempty"`
	WorkspaceIDs []string      `json:"workspace_ids,omitempty"`
	Filters      RecallFilters `json:"filters,omitempty"`
	Weights      RecallWeights `json:"weights,omitempty"`
	// IncludeContent, MaxContentBytes and IncludeCells work as in
	// RecallRequest.
	IncludeContent  bool `json:"include_content,omitempty"`
	MaxContentBytes int  `json:"max_content_bytes,omitempty"`
	IncludeCells    bool `json:"include_cells,omitempty"`
	// Federated also searches this node's peers. Peers restricts it to
	// those node IDs (default: every peer that is not revoked and has an
	// api_key); PerPeerK is each peer's k (default K); TimeoutMS bounds
	// every peer (default: each peer's timeout_ms; at most 60000).
	Federated bool     `json:"federated,omitempty"`
	Peers     []string `json:"peers,omitempty"`
	PerPeerK  int      `json:"per_peer_k,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
}

// FederationSearchRequest is POST /v1/federation/search, sent by a peer.
// The query is text: the receiving node embeds it with its own embedder,
// so peers need not share a model or dimension. It has no workspace
// fields; a peer searches the whole node.
type FederationSearchRequest struct {
	Query           string     `json:"query"`
	Mode            RecallMode `json:"mode,omitempty"`
	K               int        `json:"k,omitempty"`
	IncludeContent  bool       `json:"include_content,omitempty"`
	MaxContentBytes int        `json:"max_content_bytes,omitempty"`
}

// Degraded-mode markers reported in RecallResponse.Degraded.
const (
	DegradedVector  = "vector_unavailable"
	DegradedKeyword = "keyword_unavailable"
)
