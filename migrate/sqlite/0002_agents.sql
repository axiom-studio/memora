-- Memora Core — agent registry.

CREATE TABLE IF NOT EXISTS memora_agents (
    agent_id           TEXT PRIMARY KEY,
    workspace_id       TEXT NOT NULL,
    display_name       TEXT,
    identity_provider  TEXT NOT NULL,
    identity_proof     TEXT,
    agent_type         TEXT,
    model              TEXT,
    capabilities_json  TEXT,
    registered_at      TEXT NOT NULL,
    last_seen_at       TEXT,
    active             INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_agents_workspace ON memora_agents(workspace_id);
