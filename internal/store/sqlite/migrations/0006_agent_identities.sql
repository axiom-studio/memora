-- Agent IDs are UUIDv4s. Providers that identify an agent from its proof
-- (anthropic_session, oauth_agent, oidc_agent) map that identity key to
-- the agent_id minted the first time the key was seen.
CREATE TABLE IF NOT EXISTS memora_agent_identities (
    identity_key TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL
);
