package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, enables WAL, and
// runs migrations. The driver is pure-Go modernc.org/sqlite (CGO_ENABLED=0).
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // SQLite single-writer; keeps WAL well-behaved on the volume
	if err := migrate(context.Background(), sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{sql: sqldb}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// nullIfEmpty converts an empty string to nil (SQL NULL); non-empty strings pass through.
// Used for nullable TEXT columns. Reuse this wherever a string column is nullable.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
