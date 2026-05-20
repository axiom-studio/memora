CREATE TABLE IF NOT EXISTS memora_pins (
    pin_id       TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES memora_workspaces(id) ON DELETE CASCADE,
    query        TEXT NOT NULL,
    mode         TEXT NOT NULL DEFAULT 'hybrid',
    k            INTEGER NOT NULL DEFAULT 10,
    watermark    TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pins_workspace ON memora_pins(workspace_id);
