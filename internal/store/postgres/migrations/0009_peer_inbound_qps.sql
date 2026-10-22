-- Per-peer inbound rate limit (F-8); 0 = federation.inbound_qps.
ALTER TABLE memora_federation_peers ADD COLUMN IF NOT EXISTS inbound_qps DOUBLE PRECISION NOT NULL DEFAULT 0;
