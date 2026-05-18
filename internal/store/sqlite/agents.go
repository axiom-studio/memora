package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// RegisterAgent upserts an Agent by agent_id.
func (s *Store) RegisterAgent(ctx context.Context, a *types.Agent) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.RegisteredAt.IsZero() {
		a.RegisteredAt = time.Now().UTC()
	}
	a.Active = true
	proofJSON, _ := json.Marshal(a.IdentityProof)
	capsJSON, _ := json.Marshal(a.Capabilities)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_agents (agent_id, workspace_id, display_name, identity_provider, identity_proof,
    agent_type, model, capabilities_json, registered_at, last_seen_at, active)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(agent_id) DO UPDATE SET
    display_name=excluded.display_name,
    identity_provider=excluded.identity_provider,
    identity_proof=excluded.identity_proof,
    agent_type=excluded.agent_type,
    model=excluded.model,
    capabilities_json=excluded.capabilities_json,
    last_seen_at=excluded.last_seen_at,
    active=1`,
		a.AgentID, a.WorkspaceID, nullableStr(a.DisplayName), a.IdentityProvider,
		nullableStr(string(proofJSON)), nullableStr(a.AgentType), nullableStr(a.Model),
		nullableStr(string(capsJSON)), a.RegisteredAt, time.Now().UTC())
	return err
}

// GetAgent fetches a single Agent.
func (s *Store) GetAgent(ctx context.Context, id string) (*types.Agent, error) {
	a := &types.Agent{}
	var dn, proof, at, model, caps, lastSeen sql.NullString
	var registeredAt sqliteTime
	var active int
	err := s.db.QueryRowContext(ctx, `
SELECT agent_id, workspace_id, display_name, identity_provider, identity_proof, agent_type, model,
    capabilities_json, registered_at, last_seen_at, active
FROM memora_agents WHERE agent_id = ?`, id).Scan(
		&a.AgentID, &a.WorkspaceID, &dn, &a.IdentityProvider, &proof, &at, &model, &caps,
		&registeredAt, &lastSeen, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, types.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.DisplayName = dn.String
	a.AgentType = at.String
	a.Model = model.String
	a.RegisteredAt = registeredAt.Time
	a.LastSeenAt = nullableTime(lastSeen)
	a.Active = active != 0
	if proof.Valid && proof.String != "" {
		_ = json.Unmarshal([]byte(proof.String), &a.IdentityProof)
	}
	if caps.Valid && caps.String != "" {
		_ = json.Unmarshal([]byte(caps.String), &a.Capabilities)
	}
	return a, nil
}

// ListAgents returns the workspace's agents.
func (s *Store) ListAgents(ctx context.Context, workspaceID string) ([]types.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT agent_id, workspace_id, display_name, identity_provider, identity_proof, agent_type, model,
    capabilities_json, registered_at, last_seen_at, active
FROM memora_agents WHERE workspace_id = ? ORDER BY registered_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Agent
	for rows.Next() {
		var a types.Agent
		var dn, proof, at, model, caps, lastSeen sql.NullString
		var registeredAt sqliteTime
		var active int
		if err := rows.Scan(&a.AgentID, &a.WorkspaceID, &dn, &a.IdentityProvider, &proof, &at, &model, &caps,
			&registeredAt, &lastSeen, &active); err != nil {
			return nil, err
		}
		a.DisplayName = dn.String
		a.AgentType = at.String
		a.Model = model.String
		a.RegisteredAt = registeredAt.Time
		a.LastSeenAt = nullableTime(lastSeen)
		a.Active = active != 0
		if proof.Valid && proof.String != "" {
			_ = json.Unmarshal([]byte(proof.String), &a.IdentityProof)
		}
		if caps.Valid && caps.String != "" {
			_ = json.Unmarshal([]byte(caps.String), &a.Capabilities)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeactivateAgent flips active=false; preserves all attribution history.
func (s *Store) DeactivateAgent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE memora_agents SET active = 0 WHERE agent_id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return types.ErrNotFound
	}
	return nil
}

// GetWatermarkHistory returns history rows for a Memory or Edge.
func (s *Store) GetWatermarkHistory(ctx context.Context, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after
FROM memora_watermark_history WHERE target_id = ? AND created_at >= ? ORDER BY created_at DESC`,
		targetID, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WatermarkHistoryEntry
	for rows.Next() {
		var e types.WatermarkHistoryEntry
		var b, a sql.NullString
		var createdAt sqliteTime
		if err := rows.Scan(&e.TargetID, &e.Watermark, &e.Op, &e.AgentID, &createdAt, &b, &a); err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt.Time
		e.ContentMD5Before = b.String
		e.ContentMD5After = a.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendWatermarkHistory records a single history row.
func (s *Store) AppendWatermarkHistory(ctx context.Context, e types.WatermarkHistoryEntry) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_watermark_history (target_id, watermark, op, agent_id, created_at, content_md5_before, content_md5_after)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.TargetID, e.Watermark, e.Op, e.AgentID, e.CreatedAt, nullableStr(e.ContentMD5Before), nullableStr(e.ContentMD5After))
	return err
}

// UpsertTag adds or replaces a tag.
func (s *Store) UpsertTag(ctx context.Context, memoryID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_tags(memory_id, key, value) VALUES (?, ?, ?)
ON CONFLICT(memory_id, key) DO UPDATE SET value = excluded.value`, memoryID, key, value)
	return err
}

// DeleteTag removes a tag.
func (s *Store) DeleteTag(ctx context.Context, memoryID, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM memora_tags WHERE memory_id = ? AND key = ?", memoryID, key)
	return err
}
