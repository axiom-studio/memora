-- Memora Core — context graph edges + edges_view stub.

CREATE TABLE IF NOT EXISTS memora_edges (
    edge_id              TEXT PRIMARY KEY,
    workspace_id         TEXT NOT NULL,
    source_memory_id     TEXT NOT NULL REFERENCES memora_memories(id) ON DELETE CASCADE,
    target_memory_id     TEXT NOT NULL REFERENCES memora_memories(id) ON DELETE CASCADE,
    edge_type            TEXT NOT NULL CHECK (edge_type IN
        ('parent_of','derived_from','supersedes','references','session_of','mentions')),
    properties_json      TEXT,
    created_by_agent_id  TEXT NOT NULL,
    created_at           TEXT NOT NULL,
    deleted_at           TEXT,
    watermark            TEXT NOT NULL,
    CHECK (source_memory_id <> target_memory_id)
);
CREATE INDEX IF NOT EXISTS idx_edges_src ON memora_edges(workspace_id, source_memory_id, edge_type, deleted_at);
CREATE INDEX IF NOT EXISTS idx_edges_tgt ON memora_edges(workspace_id, target_memory_id, edge_type, deleted_at);
CREATE INDEX IF NOT EXISTS idx_edges_type_ts ON memora_edges(workspace_id, edge_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_edges_agent_ts ON memora_edges(workspace_id, created_by_agent_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_edges_live_triple
    ON memora_edges(workspace_id, source_memory_id, target_memory_id, edge_type)
    WHERE deleted_at IS NULL;

-- Empty stub — replaced by F11 (parent_context_id migration shim) with a
-- view that surfaces synthetic parent_of edges over memora_edges.
CREATE VIEW IF NOT EXISTS memora_edges_view AS
    SELECT * FROM memora_edges WHERE 0;
