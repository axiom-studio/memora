-- Agents are registered per workspace (#6154): the key becomes
-- (workspace_id, agent_id), so one agent ID, a sentinel or a shared CLI
-- agent, can be registered in every workspace. Sentinels are back-filled
-- into every workspace at startup (ensureLegacyAgent).
DROP INDEX IF EXISTS idx_agents_workspace;
ALTER TABLE memora_agents RENAME TO memora_agents_old;
CREATE TABLE memora_agents (
    agent_id           TEXT NOT NULL,
    workspace_id       TEXT NOT NULL,
    display_name       TEXT,
    identity_provider  TEXT NOT NULL,
    identity_proof     TEXT,
    agent_type         TEXT,
    model              TEXT,
    capabilities_json  TEXT,
    registered_at      TEXT NOT NULL,
    last_seen_at       TEXT,
    active             INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (workspace_id, agent_id)
);
INSERT INTO memora_agents (agent_id, workspace_id, display_name, identity_provider, identity_proof,
    agent_type, model, capabilities_json, registered_at, last_seen_at, active)
SELECT agent_id, workspace_id, display_name, identity_provider, identity_proof,
    agent_type, model, capabilities_json, registered_at, last_seen_at, active
FROM memora_agents_old;
DROP TABLE memora_agents_old;
CREATE INDEX IF NOT EXISTS idx_agents_workspace ON memora_agents(workspace_id);
