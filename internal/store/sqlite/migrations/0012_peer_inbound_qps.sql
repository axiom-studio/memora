-- Per-peer inbound rate limit (F-8); 0 = federation.inbound_qps.
ALTER TABLE memora_federation_peers ADD COLUMN inbound_qps REAL NOT NULL DEFAULT 0;
