-- Memora Core — Postgres MetadataStore + ContentStore schema.
-- Mirrors the SQLite schema but uses native Postgres types.

CREATE TABLE IF NOT EXISTS memora_schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memora_workspaces (
    id                          TEXT PRIMARY KEY,
    name                        TEXT NOT NULL,
    region                      TEXT,
    chunker_id                  TEXT,
    embedding_model             TEXT,
    meta_json                   JSONB,
    auto_link_enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    auto_link_threshold         DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    auto_link_max_edges         INTEGER NOT NULL DEFAULT 10,
    auto_link_max_incoming_per_day INTEGER NOT NULL DEFAULT 100,
    created_at                  TIMESTAMPTZ NOT NULL,
    updated_at                  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS memora_collections (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    UNIQUE(workspace_id, name)
);

CREATE TABLE IF NOT EXISTS memora_memories (
    id                         TEXT PRIMARY KEY,
    workspace_id               TEXT NOT NULL REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    collection_id              TEXT,
    content                    TEXT NOT NULL,
    content_md5                TEXT NOT NULL,
    head_watermark             TEXT NOT NULL,
    created_watermark          TEXT NOT NULL,
    written_by_agent_id        TEXT NOT NULL,
    last_modified_by_agent_id  TEXT NOT NULL,
    tags_json                  JSONB,
    recall_ready               BOOLEAN NOT NULL DEFAULT FALSE,
    created_at                 TIMESTAMPTZ NOT NULL,
    updated_at                 TIMESTAMPTZ NOT NULL,
    deleted_at                 TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_memories_ws_coll ON memora_memories(workspace_id, collection_id);
CREATE INDEX IF NOT EXISTS idx_memories_ws_agent ON memora_memories(workspace_id, written_by_agent_id);
CREATE INDEX IF NOT EXISTS idx_memories_ws_created ON memora_memories(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memories_recall_ready ON memora_memories(workspace_id, recall_ready);

CREATE TABLE IF NOT EXISTS memora_cells (
    cell_id              TEXT PRIMARY KEY,
    memory_id            TEXT NOT NULL REFERENCES memora_memories(id) ON DELETE CASCADE,
    seq                  INTEGER NOT NULL,
    text                 TEXT NOT NULL,
    text_md5             TEXT NOT NULL,
    written_by_agent_id  TEXT NOT NULL,
    embedding_model      TEXT,
    vector_key           TEXT,
    metadata_json        JSONB,
    created_at           TIMESTAMPTZ NOT NULL,
    UNIQUE(memory_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_cells_memory ON memora_cells(memory_id);
CREATE INDEX IF NOT EXISTS idx_cells_text_md5 ON memora_cells(text_md5);

CREATE TABLE IF NOT EXISTS memora_tags (
    memory_id TEXT NOT NULL REFERENCES memora_memories(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY(memory_id, key)
);
CREATE INDEX IF NOT EXISTS idx_tags_key_value ON memora_tags(key, value);

CREATE TABLE IF NOT EXISTS memora_watermark_history (
    target_id          TEXT NOT NULL,
    watermark          TEXT NOT NULL,
    op                 TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    content_md5_before TEXT,
    content_md5_after  TEXT,
    PRIMARY KEY(target_id, watermark)
);
CREATE INDEX IF NOT EXISTS idx_wmk_history_target_ts ON memora_watermark_history(target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memora_agents (
    agent_id           TEXT PRIMARY KEY,
    workspace_id       TEXT NOT NULL,
    display_name       TEXT,
    identity_provider  TEXT NOT NULL,
    identity_proof     TEXT,
    agent_type         TEXT,
    model              TEXT,
    capabilities_json  JSONB,
    registered_at      TIMESTAMPTZ NOT NULL,
    last_seen_at       TIMESTAMPTZ,
    active             BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX IF NOT EXISTS idx_agents_workspace ON memora_agents(workspace_id);

CREATE TABLE IF NOT EXISTS memora_content (
    workspace_id TEXT NOT NULL,
    memory_id    TEXT NOT NULL,
    cell_id      TEXT NOT NULL DEFAULT '',
    content_md5  TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, memory_id, cell_id)
);
CREATE INDEX IF NOT EXISTS idx_content_memory ON memora_content(workspace_id, memory_id);
