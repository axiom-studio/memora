-- Synthetic parent_of edges (the vibeflow_contexts shim, F11) get a
-- persisted UUIDv4 each, keyed by the archive row they come from, so
-- their IDs are stable across reads and carry no meaning of their own.
-- Rows are added when the shim view is built (legacy_shim.go).
CREATE TABLE IF NOT EXISTS memora_synthetic_edges (
    archive_id TEXT PRIMARY KEY,
    edge_id    TEXT NOT NULL UNIQUE
);
