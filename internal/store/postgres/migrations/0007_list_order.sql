-- Listings page newest first by (created_at, id) (ID-6): index the full
-- sort key.
DROP INDEX IF EXISTS idx_memories_ws_created;
CREATE INDEX IF NOT EXISTS idx_memories_ws_created_id ON memora_memories(workspace_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_workspaces_created_id ON memora_workspaces(created_at, id);
