package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

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
