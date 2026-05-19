// Package postgres is the scale-up Memora LedgerStore — append-only
// audit log against Postgres, using pgx/v5.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterLedger("postgres", func() adapter.LedgerStore { return &Store{} })
}

type Store struct {
	pool *pgxpool.Pool
}

func (s *Store) Open(ctx context.Context, cfg adapter.LedgerConfig) error {
	if cfg.DSN == "" {
		return errors.New("postgres ledger: DSN required")
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return fmt.Errorf("postgres ledger: parse DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("postgres ledger: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("postgres ledger: ping: %w", err)
	}
	s.pool = pool

	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS memora_ledger (
    ledger_id        TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    op               TEXT NOT NULL,
    target           TEXT,
    agent_id         TEXT NOT NULL,
    api_key_id       TEXT,
    user_id          TEXT,
    watermark_before TEXT,
    watermark_after  TEXT,
    ip               TEXT,
    user_agent       TEXT,
    ts               TIMESTAMPTZ NOT NULL,
    request_id       TEXT,
    latency_ms       INTEGER,
    metadata_json    JSONB,
    redacted         BOOLEAN NOT NULL DEFAULT FALSE,
    redacted_fields  JSONB
);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_ts    ON memora_ledger(workspace_id, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_agent ON memora_ledger(workspace_id, agent_id, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_op    ON memora_ledger(workspace_id, op, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_target   ON memora_ledger(workspace_id, target, ts)`); err != nil {
		pool.Close()
		return fmt.Errorf("postgres ledger: migrate: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("postgres ledger: not opened")
	}
	return s.pool.Ping(ctx)
}

func (s *Store) Capabilities() adapter.LedgerCapabilities {
	return adapter.LedgerCapabilities{
		SupportsAppend:      true,
		SupportsBatchAppend: true,
		SupportsQuery:       true,
		SupportsRedaction:   true,
		DurableOnAppend:     true,
		EstimatedAppendQPS:  10000,
	}
}

func (s *Store) Append(ctx context.Context, e api.LedgerEntry) error {
	return s.appendEntry(ctx, s.pool, &e)
}

func (s *Store) AppendBatch(ctx context.Context, entries []api.LedgerEntry) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i := range entries {
		if err := s.appendEntry(ctx, tx, &entries[i]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type execAdapter struct {
	pool *pgxpool.Pool
}

func (a *execAdapter) Exec(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.pool.Query(ctx, sql, args...)
}

func (s *Store) appendEntry(ctx context.Context, ex any, e *api.LedgerEntry) error {
	if e.LedgerID == "" {
		e.LedgerID = types.NewID(types.LedgerIDPrefix)
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	metaJSON, _ := json.Marshal(e.Metadata)
	redactedFieldsJSON, _ := json.Marshal(e.RedactedFields)

	query := `
INSERT INTO memora_ledger (ledger_id, workspace_id, op, target, agent_id, api_key_id, user_id,
    watermark_before, watermark_after, ip, user_agent, ts, request_id, latency_ms, metadata_json,
    redacted, redacted_fields)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`

	args := []any{
		e.LedgerID, e.WorkspaceID, e.Op, nullableStr(e.Target), e.AgentID,
		nullableStr(e.APIKeyID), nullableStr(e.UserID),
		nullableStr(e.WatermarkBefore), nullableStr(e.WatermarkAfter),
		nullableStr(e.IP), nullableStr(e.UserAgent), e.Timestamp.UTC(),
		nullableStr(e.RequestID), e.LatencyMS, nullableJSON(metaJSON),
		e.Redacted, nullableJSON(redactedFieldsJSON),
	}

	switch v := ex.(type) {
	case *pgxpool.Pool:
		_, err := v.Exec(ctx, query, args...)
		return err
	case pgx.Tx:
		_, err := v.Exec(ctx, query, args...)
		return err
	default:
		return fmt.Errorf("postgres ledger: unsupported executor type %T", ex)
	}
}

func (s *Store) Query(ctx context.Context, q adapter.LedgerQuery) ([]api.LedgerEntry, string, error) {
	where := []string{}
	args := []any{}
	argN := 1

	addFilter := func(clause string, val any) {
		where = append(where, fmt.Sprintf(clause, argN))
		args = append(args, val)
		argN++
	}

	if q.WorkspaceID != "" {
		addFilter("workspace_id = $%d", q.WorkspaceID)
	}
	if q.Since != nil {
		addFilter("ts >= $%d", q.Since.UTC())
	}
	if q.Until != nil {
		addFilter("ts <= $%d", q.Until.UTC())
	}
	if q.Actor != "" {
		addFilter("api_key_id = $%d", q.Actor)
	}
	if q.AgentID != "" {
		addFilter("agent_id = $%d", q.AgentID)
	}
	if q.MemoryID != "" {
		addFilter("target = $%d", q.MemoryID)
	}
	if q.EdgeID != "" {
		addFilter("target = $%d", q.EdgeID)
	}
	if len(q.Op) > 0 {
		where = append(where, fmt.Sprintf("op = ANY($%d)", argN))
		args = append(args, q.Op)
		argN++
	}
	if q.SinceLedgerID != "" {
		addFilter("ledger_id < $%d", q.SinceLedgerID)
	}

	sqlStr := `SELECT ledger_id, workspace_id, op, target, agent_id, api_key_id, user_id,
		watermark_before, watermark_after, ip, user_agent, ts, request_id, latency_ms,
		metadata_json, redacted, redacted_fields
	FROM memora_ledger`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	sqlStr += " ORDER BY ts DESC, ledger_id DESC"

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	sqlStr += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []api.LedgerEntry
	for rows.Next() {
		var e api.LedgerEntry
		var target, apiKey, userID, wmkB, wmkA, ip, ua, reqID *string
		var latency *int
		var metaJSON, redFieldsJSON []byte
		if err := rows.Scan(&e.LedgerID, &e.WorkspaceID, &e.Op, &target, &e.AgentID,
			&apiKey, &userID, &wmkB, &wmkA, &ip, &ua, &e.Timestamp,
			&reqID, &latency, &metaJSON, &e.Redacted, &redFieldsJSON); err != nil {
			return nil, "", err
		}
		if target != nil {
			e.Target = *target
		}
		if apiKey != nil {
			e.APIKeyID = *apiKey
		}
		if userID != nil {
			e.UserID = *userID
		}
		if wmkB != nil {
			e.WatermarkBefore = *wmkB
		}
		if wmkA != nil {
			e.WatermarkAfter = *wmkA
		}
		if ip != nil {
			e.IP = *ip
		}
		if ua != nil {
			e.UserAgent = *ua
		}
		if reqID != nil {
			e.RequestID = *reqID
		}
		if latency != nil {
			e.LatencyMS = *latency
		}
		if len(redFieldsJSON) > 0 {
			_ = json.Unmarshal(redFieldsJSON, &e.RedactedFields)
		}
		if len(metaJSON) > 0 {
			if !e.Redacted {
				_ = json.Unmarshal(metaJSON, &e.Metadata)
			} else {
				e.Metadata = map[string]any{"_redacted": "[REDACTED]"}
			}
		}
		if e.Redacted {
			applyRedaction(&e)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].LedgerID
	}
	return out, nextCursor, nil
}

func (s *Store) Redact(ctx context.Context, ledgerID string, fields []string) error {
	if len(fields) == 0 {
		fields = []string{"metadata"}
	}
	fieldsJSON, _ := json.Marshal(fields)
	tag, err := s.pool.Exec(ctx, `
UPDATE memora_ledger SET redacted = TRUE, redacted_fields = $1 WHERE ledger_id = $2`,
		fieldsJSON, ledgerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}

const redacted = "[REDACTED]"

func applyRedaction(e *api.LedgerEntry) {
	set := make(map[string]bool, len(e.RedactedFields))
	for _, f := range e.RedactedFields {
		set[f] = true
	}
	if set["ip"] {
		e.IP = redacted
	}
	if set["user_agent"] {
		e.UserAgent = redacted
	}
	if set["api_key_id"] {
		e.APIKeyID = redacted
	}
	if set["user_id"] {
		e.UserID = redacted
	}
	if set["target"] {
		e.Target = redacted
	}
	if set["watermark_before"] {
		e.WatermarkBefore = redacted
	}
	if set["watermark_after"] {
		e.WatermarkAfter = redacted
	}
	if set["request_id"] {
		e.RequestID = redacted
	}
	if set["metadata"] {
		e.Metadata = map[string]any{"_redacted": redacted}
	}
}
