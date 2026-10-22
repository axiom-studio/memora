-- Runtime federation peer registry (F-7). Keys are stored only as the
-- inbound key's SHA-256 and the sealed outbound key. deleted_at is a
-- tombstone: the config seed never re-imports a removed peer.
CREATE TABLE IF NOT EXISTS memora_federation_peers (
    node_id            TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    url                TEXT NOT NULL,
    inbound_key_sha256 TEXT NOT NULL DEFAULT '',
    sealed_api_key     BYTEA,
    tls_cert           TEXT NOT NULL DEFAULT '',
    client_cert_file   TEXT NOT NULL DEFAULT '',
    client_key_file    TEXT NOT NULL DEFAULT '',
    client_cert_sha256 TEXT NOT NULL DEFAULT '',
    timeout_ms         INTEGER NOT NULL DEFAULT 0,
    rate_limit_qps     DOUBLE PRECISION NOT NULL DEFAULT 0,
    revoked            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    deleted_at         TIMESTAMPTZ
);
