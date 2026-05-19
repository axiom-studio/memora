package api

import "time"

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
	CollectionID  string              `json:"collection_id,omitempty"`
	Tags          map[string][]string `json:"tags,omitempty"`
	TsAfter       *time.Time          `json:"ts_after,omitempty"`
	TsBefore      *time.Time          `json:"ts_before,omitempty"`
	AgentID       string              `json:"agent_id,omitempty"`
	AgentIDIn     []string            `json:"agent_id_in,omitempty"`
	AgentIDNotIn  []string            `json:"agent_id_not_in,omitempty"`
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
	Query          string         `json:"query"`
	Mode           RecallMode     `json:"mode,omitempty"`
	K              int            `json:"k,omitempty"`
	Filters        RecallFilters  `json:"filters,omitempty"`
	Weights        RecallWeights  `json:"weights,omitempty"`
	IncludeCells   bool           `json:"include_cells,omitempty"`
	GraphExpansion *GraphExpansion `json:"graph_expansion,omitempty"`
	MemoryIDs      []string       `json:"memory_ids,omitempty"` // for lookup mode
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
	MemoryID         string            `json:"memory_id"`
	CellID           string            `json:"cell_id,omitempty"`
	Score            float64           `json:"score"`
	VectorScore      float64           `json:"vector_score,omitempty"`
	KeywordScore     float64           `json:"keyword_score,omitempty"`
	Text             string            `json:"text"`
	Tags             map[string]string `json:"tags,omitempty"`
	Watermark        string            `json:"watermark"`
	WrittenByAgentID string            `json:"written_by_agent_id,omitempty"`
	Via              string            `json:"via"` // "seed" | "graph" | "federation:<peer>"
	PeerID           string            `json:"peer_id,omitempty"`
	GraphProvenance  *GraphProvenance  `json:"graph_provenance,omitempty"`
}

// RecallResponse is the full search result envelope.
type RecallResponse struct {
	Results                 []RecallHit    `json:"results"`
	TotalCandidatesScanned  int            `json:"total_candidates_scanned"`
	GraphNodesExpanded      int            `json:"graph_nodes_expanded,omitempty"`
	LatencyMS               int            `json:"latency_ms"`
	EmbeddingPending        bool           `json:"embedding_pending,omitempty"`
	FederationID            string         `json:"federation_id,omitempty"`
	PartialSuccess          bool           `json:"partial_success,omitempty"`
	FailedPeers             []string       `json:"failed_peers,omitempty"`
	PeerLatencyMS           map[string]int `json:"peer_latency_ms,omitempty"`
}

// PinRequest binds a recall query to a watermark.
type PinRequest struct {
	Query     RecallRequest `json:"query"`
	Watermark string        `json:"watermark"`
}

// PinResponse acknowledges a pin.
type PinResponse struct {
	PinID     string `json:"pin_id"`
	Watermark string `json:"watermark"`
}
