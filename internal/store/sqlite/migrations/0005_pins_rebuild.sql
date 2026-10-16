-- 0001 created memora_pins with a stub schema (query_spec), so 0004's
-- CREATE TABLE IF NOT EXISTS never applied and pins.go's columns were
-- missing on every SQLite database. Rebuild the table into the 0004 shape,
-- carrying over rows whose workspace still exists (the FK requires it).
ALTER TABLE memora_pins RENAME TO memora_pins_stale;

CREATE TABLE memora_pins (
    pin_id       TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    query        TEXT NOT NULL,
    mode         TEXT NOT NULL DEFAULT 'hybrid',
    k            INTEGER NOT NULL DEFAULT 10,
    watermark    TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO memora_pins (pin_id, workspace_id, query, watermark, created_at)
SELECT pin_id, workspace_id, query_spec, watermark, created_at
FROM memora_pins_stale
WHERE workspace_id IN (SELECT id FROM memora_workspaces);

DROP TABLE memora_pins_stale;

CREATE INDEX IF NOT EXISTS idx_pins_workspace ON memora_pins(workspace_id);
