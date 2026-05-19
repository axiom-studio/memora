package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

// openSQLiteDB opens a SQLite database with the standard safety
// pragmas (WAL, foreign keys, synchronous=NORMAL, busy_timeout).
// Shared between PrimaryStore and ContentStore.
func openSQLiteDB(dsn string) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
