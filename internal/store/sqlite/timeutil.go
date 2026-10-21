package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// tsLayout is the fixed-width UTC layout created_at is written in for
// memories and workspaces, so text order in ORDER BY and the listing
// cursor is chronological. The driver's default rendering trims trailing
// fractional zeros, and "…05.1 +0000" sorts after "…05.12 +0000".
const tsLayout = "2006-01-02T15:04:05.000000000Z"

// formatTS renders t for a created_at column.
func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

// sqliteTime is a time.Time wrapper that scans correctly out of
// modernc.org/sqlite which round-trips timestamps as ISO-ish strings.
// Both the RFC3339Nano shape used by Go inserts and the
// "2006-01-02 15:04:05" shape used by SQLite's datetime() default are
// parsed.
type sqliteTime struct {
	time.Time
}

// Scan implements sql.Scanner.
func (t *sqliteTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case time.Time:
		t.Time = v
		return nil
	case []byte:
		return t.parseString(string(v))
	case string:
		return t.parseString(v)
	default:
		return fmt.Errorf("sqliteTime: cannot scan %T", src)
	}
}

// Value implements driver.Valuer so the same wrapper can be passed
// into Exec args as well.
func (t sqliteTime) Value() (driver.Value, error) {
	if t.IsZero() {
		return nil, nil
	}
	return t.Time.UTC().Format(time.RFC3339Nano), nil
}

func (t *sqliteTime) parseString(s string) error {
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if parsed, err := time.Parse(f, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("sqliteTime: unrecognized format %q", s)
}

// nullableTime turns a nullable text column into *time.Time. Used by
// scanners that target optional fields like deleted_at, last_seen_at.
func nullableTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var t sqliteTime
	if err := t.parseString(ns.String); err != nil {
		return nil
	}
	tt := t.Time
	return &tt
}

// normalizeCreatedAt rewrites memory and workspace created_at values
// written before tsLayout. Every tsLayout value is 30 bytes, so only
// older rows are touched; idempotent, O(old rows), once.
func (s *Store) normalizeCreatedAt(ctx context.Context) error {
	for _, table := range []string{"memora_memories", "memora_workspaces"} {
		rows, err := s.db.QueryContext(ctx, `SELECT id, created_at FROM `+table+` WHERE length(created_at) != 30`)
		if err != nil {
			return err
		}
		fixed := map[string]string{}
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				_ = rows.Close()
				return err
			}
			var t sqliteTime
			if err := t.parseString(raw); err == nil { // unparseable: leave as is
				fixed[id] = formatTS(t.Time)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for id, ts := range fixed {
			if _, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET created_at = ? WHERE id = ?`, ts, id); err != nil {
				return fmt.Errorf("%s %s: %w", table, id, err)
			}
		}
	}
	return nil
}
