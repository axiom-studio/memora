-- Memora Core — SQLite primary-store schema (data-plane).
-- Idempotent (CREATE TABLE/INDEX IF NOT EXISTS). Run by the
-- modernc.org/sqlite adapter at server startup via the embedded
-- migration runner.

CREATE TABLE IF NOT EXISTS memora_schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS memora_workspaces (
    id                          TEXT PRIMARY KEY,
    name                        TEXT NOT NULL,
    region                      TEXT,
    chunker_id                  TEXT,
    embedding_model             TEXT,
    meta_json                   TEXT,
    auto_link_enabled           INTEGER NOT NULL DEFAULT 0,
    auto_link_threshold         REAL    NOT NULL DEFAULT 0.7,
    auto_link_max_edges         INTEGER NOT NULL DEFAULT 10,
    auto_link_max_incoming_per_day INTEGER NOT NULL DEFAULT 100,
    created_at                  TEXT NOT NULL,
    updated_at                  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memora_workspace_meta (
    workspace_id      TEXT PRIMARY KEY REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    hierarchy_labels  TEXT,
    extra_json        TEXT,
    updated_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memora_collections (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
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
    tags_json                  TEXT,
    recall_ready               INTEGER NOT NULL DEFAULT 0,
    created_at                 TEXT NOT NULL,
    updated_at                 TEXT NOT NULL,
    deleted_at                 TEXT
);
CREATE INDEX IF NOT EXISTS idx_memories_ws_coll ON memora_memories(workspace_id, collection_id);
CREATE INDEX IF NOT EXISTS idx_memories_ws_agent ON memora_memories(workspace_id, written_by_agent_id);
CREATE INDEX IF NOT EXISTS idx_memories_ws_created ON memora_memories(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memories_recall_ready ON memora_memories(workspace_id, recall_ready);

CREATE VIRTUAL TABLE IF NOT EXISTS memora_memories_fts USING fts5(
    memory_id UNINDEXED,
    workspace_id UNINDEXED,
    content,
    tokenize = 'porter unicode61'
);

CREATE TABLE IF NOT EXISTS memora_cells (
    cell_id              TEXT PRIMARY KEY,
    memory_id            TEXT NOT NULL REFERENCES memora_memories(id) ON DELETE CASCADE,
    seq                  INTEGER NOT NULL,
    text                 TEXT NOT NULL,
    text_md5             TEXT NOT NULL,
    written_by_agent_id  TEXT NOT NULL,
    embedding_model      TEXT,
    vector_key           TEXT,
    metadata_json        TEXT,
    created_at           TEXT NOT NULL,
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
    created_at         TEXT NOT NULL,
    content_md5_before TEXT,
    content_md5_after  TEXT,
    PRIMARY KEY(target_id, watermark)
);
CREATE INDEX IF NOT EXISTS idx_wmk_history_target_ts ON memora_watermark_history(target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memora_ledger (
    ledger_id        TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    op               TEXT NOT NULL,
    target           TEXT,
    agent_id         TEXT NOT NULL,
    api_key_id       TEXT,
    user_id          TEXT,
    watermark_before TEXT,
    watermark_after  TEXT,
    ip               TEXT,
    user_agent       TEXT,
    ts               TEXT NOT NULL,
    request_id       TEXT,
    latency_ms       INTEGER,
    metadata_json    TEXT,
    redacted         INTEGER NOT NULL DEFAULT 0,
    redacted_fields  TEXT
);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_ts    ON memora_ledger(workspace_id, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_agent ON memora_ledger(workspace_id, agent_id, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_op    ON memora_ledger(workspace_id, op, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_target   ON memora_ledger(workspace_id, target, ts);

CREATE TABLE IF NOT EXISTS memora_pins (
    pin_id       TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    query_spec   TEXT NOT NULL,
    watermark    TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
