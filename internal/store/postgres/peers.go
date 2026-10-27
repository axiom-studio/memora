package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

const peerColumns = `node_id, name, url, inbound_key_sha256, sealed_api_key, tls_cert, client_cert_file,
    client_key_file, client_cert_sha256, timeout_ms, rate_limit_qps, inbound_qps, revoked, created_at, updated_at`

// ListPeers implements adapter.PeerStore.
func (s *MetadataStore) ListPeers(ctx context.Context) ([]adapter.PeerRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+peerColumns+` FROM memora_federation_peers
WHERE deleted_at IS NULL ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}
	defer rows.Close()
	var out []adapter.PeerRecord
	for rows.Next() {
		var p adapter.PeerRecord
		if err := rows.Scan(&p.NodeID, &p.Name, &p.URL, &p.InboundKeySHA256, &p.SealedAPIKey, &p.TLSCert,
			&p.ClientCertFile, &p.ClientKeyFile, &p.ClientCertSHA256, &p.TimeoutMS, &p.RateLimitQPS, &p.InboundQPS,
			&p.Revoked, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan peer: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *MetadataStore) writePeer(ctx context.Context, p adapter.PeerRecord, conflict string) (int64, error) {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx, `INSERT INTO memora_federation_peers (`+peerColumns+`, deleted_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14, NULL) `+conflict,
		p.NodeID, p.Name, p.URL, p.InboundKeySHA256, p.SealedAPIKey, p.TLSCert, p.ClientCertFile,
		p.ClientKeyFile, p.ClientCertSHA256, p.TimeoutMS, p.RateLimitQPS, p.InboundQPS, p.Revoked, now)
	return tag.RowsAffected(), err
}

// UpsertPeer implements adapter.PeerStore.
func (s *MetadataStore) UpsertPeer(ctx context.Context, p adapter.PeerRecord) error {
	if _, err := types.ParseID("node_id", p.NodeID); err != nil {
		return err
	}
	_, err := s.writePeer(ctx, p, `ON CONFLICT (node_id) DO UPDATE SET
    name = EXCLUDED.name, url = EXCLUDED.url, inbound_key_sha256 = EXCLUDED.inbound_key_sha256,
    sealed_api_key = EXCLUDED.sealed_api_key, tls_cert = EXCLUDED.tls_cert,
    client_cert_file = EXCLUDED.client_cert_file, client_key_file = EXCLUDED.client_key_file,
    client_cert_sha256 = EXCLUDED.client_cert_sha256, timeout_ms = EXCLUDED.timeout_ms,
    rate_limit_qps = EXCLUDED.rate_limit_qps, inbound_qps = EXCLUDED.inbound_qps, revoked = EXCLUDED.revoked,
    updated_at = EXCLUDED.updated_at, deleted_at = NULL`)
	if err != nil {
		return fmt.Errorf("upsert peer %s: %w", p.NodeID, err)
	}
	return nil
}

// SeedPeer implements adapter.PeerStore.
func (s *MetadataStore) SeedPeer(ctx context.Context, p adapter.PeerRecord) (bool, error) {
	if _, err := types.ParseID("node_id", p.NodeID); err != nil {
		return false, err
	}
	n, err := s.writePeer(ctx, p, `ON CONFLICT (node_id) DO NOTHING`)
	if err != nil {
		return false, fmt.Errorf("seed peer %s: %w", p.NodeID, err)
	}
	return n == 1, nil
}

// AllPeerIDs implements adapter.PeerStore.
func (s *MetadataStore) AllPeerIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT node_id FROM memora_federation_peers`)
	if err != nil {
		return nil, fmt.Errorf("list peer ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan peer id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeletePeer implements adapter.PeerStore.
func (s *MetadataStore) DeletePeer(ctx context.Context, nodeID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE memora_federation_peers SET deleted_at = now(), updated_at = now()
WHERE node_id = $1 AND deleted_at IS NULL`, nodeID)
	if err != nil {
		return fmt.Errorf("delete peer %s: %w", nodeID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: peer %s", types.ErrNotFound, nodeID)
	}
	return nil
}
