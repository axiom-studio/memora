package types

// SeedFile is one entry in a bulk-import manifest.
type SeedFile struct {
	Path         string            `json:"path"`
	Content      []byte            `json:"-"` // populated by the loader; not serialized
	Tags         map[string]string `json:"tags,omitempty"`
	ChunkerID    string            `json:"chunker_id,omitempty"`
	CollectionID string            `json:"collection_id,omitempty"`
}

// SeedEdgeManifest is a row in the optional `_edges.jsonl` sidecar
// accepted by `memora-cli seed`. After files imprint, the manifest is
// resolved into a LinkBatch call.
type SeedEdgeManifest struct {
	Source     string         `json:"source"` // file:// path or mem_... id
	Target     string         `json:"target"`
	Type       string         `json:"type"` // one of StoredEdgeTypes
	Properties map[string]any `json:"properties,omitempty"`
}
