package sqlite

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

func (s *Store) CreatePin(ctx context.Context, p *types.Pin) error {
	if p.PinID == "" {
		p.PinID = types.NewID("pin")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_pins (pin_id, workspace_id, query, mode, k, watermark, label, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.PinID, p.WorkspaceID, p.Query, p.Mode, p.K, p.Watermark, p.Label, p.CreatedBy, p.CreatedAt)
	return err
}

func (s *Store) ListPins(ctx context.Context, workspaceID string) ([]types.Pin, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pin_id, workspace_id, query, mode, k, watermark, label, created_by, created_at
FROM memora_pins WHERE workspace_id = ? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Pin
	for rows.Next() {
		var p types.Pin
		var createdAt sqliteTime
		if err := rows.Scan(&p.PinID, &p.WorkspaceID, &p.Query, &p.Mode, &p.K,
			&p.Watermark, &p.Label, &p.CreatedBy, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt = createdAt.Time
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePin(ctx context.Context, pinID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM memora_pins WHERE pin_id = ?", pinID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	return nil
}
