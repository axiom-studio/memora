-- memora_tags is the source of truth for tags (#6182). Tag edits once
-- wrote only memora_tags, leaving tags_json stale; rebuild it from there.
UPDATE memora_memories SET tags_json = (
    SELECT jsonb_object_agg(key, value) FROM memora_tags WHERE memora_tags.memory_id = memora_memories.id);
