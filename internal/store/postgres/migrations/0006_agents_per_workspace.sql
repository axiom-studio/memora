-- Agents are registered per workspace (#6154): the key becomes
-- (workspace_id, agent_id), so one agent ID, a sentinel or a shared CLI
-- agent, can be registered in every workspace. Sentinels are back-filled
-- into every workspace at startup (MetadataStore.seedReservedAgents).
ALTER TABLE memora_agents DROP CONSTRAINT IF EXISTS memora_agents_pkey;
ALTER TABLE memora_agents ADD PRIMARY KEY (workspace_id, agent_id);
