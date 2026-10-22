-- This node's identity for federation: one row, written on first start.
CREATE TABLE IF NOT EXISTS memora_node (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    node_id   TEXT NOT NULL
);
