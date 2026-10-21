// Package sqlite is the OSS-default Memora LedgerStore — append-only
// audit log against SQLite, colocated with the MetadataStore.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"

	_ "modernc.org/sqlite"
)

func init() {
	adapter.RegisterLedger("sqlite", func() adapter.LedgerStore { return &Store{} })
}

// Store implements adapter.LedgerStore against SQLite.
type Store struct {
	db *sql.DB
}

// Open implements adapter.LedgerStore.
func (s *Store) Open(ctx context.Context, cfg adapter.LedgerConfig) error {
	if cfg.DSN == "" {
		return errors.New("sqlite ledger: DSN required")
	}
	dsn := cfg.DSN
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return err
	}
	db.SetMaxOpenConns(1)
	s.db = db
	_, err = db.ExecContext(ctx, `
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
    ts               TEXT NOT NULL,
    request_id       TEXT,
    latency_ms       INTEGER,
    metadata_json    TEXT,
    redacted         INTEGER NOT NULL DEFAULT 0,
    redacted_fields  TEXT
);
DROP INDEX IF EXISTS idx_ledger_ws_ts;
CREATE INDEX IF NOT EXISTS idx_ledger_ws_ts_id ON memora_ledger(workspace_id, ts, ledger_id);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_agent ON memora_ledger(workspace_id, agent_id, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_ws_op    ON memora_ledger(workspace_id, op, ts);
CREATE INDEX IF NOT EXISTS idx_ledger_target   ON memora_ledger(workspace_id, target, ts);`)
	if err != nil {
		return err
	}
	return s.normalizeTimestamps(ctx)
}

// tsLayout is the fixed-width UTC layout ts is stored in. Text comparison
// in ORDER BY, the since/until filters and the keyset cursor is then
// chronological. RFC3339Nano trims trailing zeros, and "…05.1Z" sorts
// after "…05.12Z" (#6155).
const tsLayout = "2006-01-02T15:04:05.000000000Z"

// formatTS renders t for the ts column.
func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

// normalizeTimestamps rewrites ts values written before tsLayout. Every
// tsLayout value is 30 bytes, so only other rows are touched; idempotent.
// O(old rows), once.
func (s *Store) normalizeTimestamps(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT ledger_id, ts FROM memora_ledger WHERE length(ts) != 30`)
	if err != nil {
		return fmt.Errorf("scan ledger timestamps: %w", err)
	}
	fixed := map[string]string{}
	for rows.Next() {
		var id, ts string
		if err := rows.Scan(&id, &ts); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan ledger timestamps: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			fixed[id] = formatTS(t)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for id, ts := range fixed {
		if _, err := s.db.ExecContext(ctx, `UPDATE memora_ledger SET ts = ? WHERE ledger_id = ?`, ts, id); err != nil {
			return fmt.Errorf("normalize ledger timestamp %s: %w", id, err)
		}
	}
	return nil
}

// Close implements adapter.LedgerStore.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping implements adapter.LedgerStore.
func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return errors.New("sqlite ledger: not opened")
	}
	return s.db.PingContext(ctx)
}

// Capabilities implements adapter.LedgerStore.
func (s *Store) Capabilities() adapter.LedgerCapabilities {
	return adapter.LedgerCapabilities{
		SupportsAppend:      true,
		SupportsBatchAppend: true,
		SupportsQuery:       true,
		SupportsRedaction:   true,
		DurableOnAppend:     true,
		EstimatedAppendQPS:  5000,
	}
}

// Append implements adapter.LedgerStore.
func (s *Store) Append(ctx context.Context, e api.LedgerEntry) error {
	if e.LedgerID == "" {
		e.LedgerID = types.NewID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	metaJSON, _ := json.Marshal(e.Metadata)
	redactedFieldsJSON, _ := json.Marshal(e.RedactedFields)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memora_ledger (ledger_id, workspace_id, op, target, agent_id, api_key_id, user_id,
    watermark_before, watermark_after, ip, user_agent, ts, request_id, latency_ms, metadata_json,
    redacted, redacted_fields)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.LedgerID, e.WorkspaceID, e.Op, nullableStr(e.Target), e.AgentID,
		nullableStr(e.APIKeyID), nullableStr(e.UserID),
		nullableStr(e.WatermarkBefore), nullableStr(e.WatermarkAfter),
		nullableStr(e.IP), nullableStr(e.UserAgent), formatTS(e.Timestamp),
		nullableStr(e.RequestID), e.LatencyMS, nullableStr(string(metaJSON)),
		boolToInt(e.Redacted), nullableStr(string(redactedFieldsJSON)))
	return err
}

// AppendBatch implements adapter.LedgerStore.
func (s *Store) AppendBatch(ctx context.Context, entries []api.LedgerEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range entries {
		if err := s.appendIn(ctx, tx, &entries[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) appendIn(ctx context.Context, tx *sql.Tx, e *api.LedgerEntry) error {
	if e.LedgerID == "" {
		e.LedgerID = types.NewID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	metaJSON, _ := json.Marshal(e.Metadata)
	redactedFieldsJSON, _ := json.Marshal(e.RedactedFields)
	_, err := tx.ExecContext(ctx, `
INSERT INTO memora_ledger (ledger_id, workspace_id, op, target, agent_id, api_key_id, user_id,
    watermark_before, watermark_after, ip, user_agent, ts, request_id, latency_ms, metadata_json,
    redacted, redacted_fields)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.LedgerID, e.WorkspaceID, e.Op, nullableStr(e.Target), e.AgentID,
		nullableStr(e.APIKeyID), nullableStr(e.UserID),
		nullableStr(e.WatermarkBefore), nullableStr(e.WatermarkAfter),
		nullableStr(e.IP), nullableStr(e.UserAgent), formatTS(e.Timestamp),
		nullableStr(e.RequestID), e.LatencyMS, nullableStr(string(metaJSON)),
		boolToInt(e.Redacted), nullableStr(string(redactedFieldsJSON)))
	return err
}

// Query implements adapter.LedgerStore.
func (s *Store) Query(ctx context.Context, q adapter.LedgerQuery) ([]api.LedgerEntry, string, error) {
	where := []string{}
	args := []any{}
	if q.WorkspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, q.WorkspaceID)
	}
	if q.Since != nil {
		where = append(where, "ts >= ?")
		args = append(args, formatTS(*q.Since))
	}
	if q.Until != nil {
		where = append(where, "ts <= ?")
		args = append(args, formatTS(*q.Until))
	}
	if q.Actor != "" {
		where = append(where, "api_key_id = ?")
		args = append(args, q.Actor)
	}
	if q.AgentID != "" {
		where = append(where, "agent_id = ?")
		args = append(args, q.AgentID)
	}
	if q.MemoryID != "" {
		where = append(where, "target = ?")
		args = append(args, q.MemoryID)
	}
	if q.EdgeID != "" {
		where = append(where, "target = ?")
		args = append(args, q.EdgeID)
	}
	if len(q.Op) > 0 {
		ph := make([]string, len(q.Op))
		for i, op := range q.Op {
			ph[i] = "?"
			args = append(args, op)
		}
		where = append(where, "op IN ("+strings.Join(ph, ",")+")")
	}
	if q.SinceLedgerID != "" {
		// Keyset cursor matching ORDER BY ts DESC, ledger_id DESC: rows
		// strictly after the cursor entry. Ledger IDs are random UUIDs, so
		// the cursor's own ts must be part of the comparison.
		where = append(where, "(ts, ledger_id) < ((SELECT ts FROM memora_ledger WHERE ledger_id = ?), ?)")
		args = append(args, q.SinceLedgerID, q.SinceLedgerID)
	}

	sqlStr := "SELECT ledger_id, workspace_id, op, target, agent_id, api_key_id, user_id, watermark_before, watermark_after, ip, user_agent, ts, request_id, latency_ms, metadata_json, redacted, redacted_fields FROM memora_ledger"
	if len(where) > 0 {
		//nolint:gosec // G202: only fixed SQL fragments and "?" placeholders are concatenated; values stay parameterized
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	sqlStr += " ORDER BY ts DESC, ledger_id DESC"
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	sqlStr += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []api.LedgerEntry
	for rows.Next() {
		var e api.LedgerEntry
		var (
			target, apiKey, userID, wmkB, wmkA, ip, ua, tsStr, reqID, metaJSON, redFields sql.NullString
			latency, redacted                                                             sql.NullInt64
		)
		if err := rows.Scan(&e.LedgerID, &e.WorkspaceID, &e.Op, &target, &e.AgentID, &apiKey, &userID,
			&wmkB, &wmkA, &ip, &ua, &tsStr, &reqID, &latency, &metaJSON, &redacted, &redFields); err != nil {
			return nil, "", err
		}
		if tsStr.Valid {
			e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr.String)
		}
		e.Target = target.String
		e.APIKeyID = apiKey.String
		e.UserID = userID.String
		e.WatermarkBefore = wmkB.String
		e.WatermarkAfter = wmkA.String
		e.IP = ip.String
		e.UserAgent = ua.String
		e.RequestID = reqID.String
		if latency.Valid {
			e.LatencyMS = int(latency.Int64)
		}
		e.Redacted = redacted.Valid && redacted.Int64 != 0
		if redFields.Valid && redFields.String != "" {
			_ = json.Unmarshal([]byte(redFields.String), &e.RedactedFields)
		}
		if metaJSON.Valid && metaJSON.String != "" {
			if !e.Redacted {
				_ = json.Unmarshal([]byte(metaJSON.String), &e.Metadata)
			} else {
				e.Metadata = map[string]any{"_redacted": "[REDACTED]"}
			}
		}
		if e.Redacted {
			applyRedaction(&e)
		}
		out = append(out, e)
	}
	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].LedgerID
	}
	return out, nextCursor, rows.Err()
}

// Redact marks a ledger entry's fields as redacted. Row is preserved.
func (s *Store) Redact(ctx context.Context, ledgerID string, fields []string) error {
	if len(fields) == 0 {
		fields = []string{"metadata"}
	}
	fieldsJSON, _ := json.Marshal(fields)
	res, err := s.db.ExecContext(ctx, `
UPDATE memora_ledger SET redacted = 1, redacted_fields = ? WHERE ledger_id = ?`,
		string(fieldsJSON), ledgerID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
