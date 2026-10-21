-- Full-text keyword search (S-2): a generated tsvector over memory
-- content with english stemming, GIN-indexed for @@ queries.
ALTER TABLE memora_memories
    ADD COLUMN IF NOT EXISTS content_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('english', content)) STORED;
CREATE INDEX IF NOT EXISTS idx_memories_content_tsv ON memora_memories USING GIN (content_tsv);
