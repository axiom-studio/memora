package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

const peerColumns = `node_id, name, url, inbound_key_sha256, sealed_api_key, tls_cert, client_cert_file,
    client_key_file, client_cert_sha256, timeout_ms, rate_limit_qps, inbound_qps, revoked, created_at, updated_at`

// ListPeers implements adapter.PeerStore.
func (s *Store) ListPeers(ctx context.Context) ([]adapter.PeerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+peerColumns+` FROM memora_federation_peers
WHERE deleted_at IS NULL ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}
	defer rows.Close()
	var out []adapter.PeerRecord
	for rows.Next() {
		var p adapter.PeerRecord
		var revoked int
		var created, updated sqliteTime
		if err := rows.Scan(&p.NodeID, &p.Name, &p.URL, &p.InboundKeySHA256, &p.SealedAPIKey, &p.TLSCert,
			&p.ClientCertFile, &p.ClientKeyFile, &p.ClientCertSHA256, &p.TimeoutMS, &p.RateLimitQPS, &p.InboundQPS,
			&revoked, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan peer: %w", err)
		}
		p.Revoked, p.CreatedAt, p.UpdatedAt = revoked != 0, created.Time, updated.Time
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) writePeer(ctx context.Context, p adapter.PeerRecord, conflict string) (sql.Result, error) {
	now := time.Now().UTC()
	return s.db.ExecContext(ctx, `INSERT INTO memora_federation_peers (`+peerColumns+`, deleted_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL) `+conflict,
		p.NodeID, p.Name, p.URL, p.InboundKeySHA256, p.SealedAPIKey, p.TLSCert, p.ClientCertFile,
		p.ClientKeyFile, p.ClientCertSHA256, p.TimeoutMS, p.RateLimitQPS, p.InboundQPS, boolToInt(p.Revoked),
		formatTS(now), formatTS(now))
}

// UpsertPeer implements adapter.PeerStore.
func (s *Store) UpsertPeer(ctx context.Context, p adapter.PeerRecord) error {
	if _, err := types.ParseID("node_id", p.NodeID); err != nil {
		return err
	}
	_, err := s.writePeer(ctx, p, `ON CONFLICT(node_id) DO UPDATE SET
    name = excluded.name, url = excluded.url, inbound_key_sha256 = excluded.inbound_key_sha256,
    sealed_api_key = excluded.sealed_api_key, tls_cert = excluded.tls_cert,
    client_cert_file = excluded.client_cert_file, client_key_file = excluded.client_key_file,
    client_cert_sha256 = excluded.client_cert_sha256, timeout_ms = excluded.timeout_ms,
    rate_limit_qps = excluded.rate_limit_qps, inbound_qps = excluded.inbound_qps, revoked = excluded.revoked,
    updated_at = excluded.updated_at, deleted_at = NULL`)
	if err != nil {
		return fmt.Errorf("upsert peer %s: %w", p.NodeID, err)
	}
	return nil
}

// SeedPeer implements adapter.PeerStore.
func (s *Store) SeedPeer(ctx context.Context, p adapter.PeerRecord) (bool, error) {
	if _, err := types.ParseID("node_id", p.NodeID); err != nil {
		return false, err
	}
	res, err := s.writePeer(ctx, p, `ON CONFLICT(node_id) DO NOTHING`)
	if err != nil {
		return false, fmt.Errorf("seed peer %s: %w", p.NodeID, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// AllPeerIDs implements adapter.PeerStore.
func (s *Store) AllPeerIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id FROM memora_federation_peers`)
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
func (s *Store) DeletePeer(ctx context.Context, nodeID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE memora_federation_peers SET deleted_at = ?, updated_at = ?
WHERE node_id = ? AND deleted_at IS NULL`, formatTS(time.Now()), formatTS(time.Now()), nodeID)
	if err != nil {
		return fmt.Errorf("delete peer %s: %w", nodeID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: peer %s", types.ErrNotFound, nodeID)
	}
	return nil
}
