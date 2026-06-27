package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// NOTE: migrate applies each file's SQL outside an explicit transaction and
// records schema_migrations only after the whole file succeeds. This is safe
// ONLY because every migration uses IF NOT EXISTS, so re-running a partially
// applied file is idempotent. A future migration with non-idempotent
// statements (e.g. ALTER) MUST wrap its file in a transaction.
//
// migrate applies every embedded *.sql file in lexical order, tracking applied
// files in a schema_migrations table so re-runs are idempotent.
func migrate(ctx context.Context, sqldb *sql.DB) error {
	if _, err := sqldb.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var seen string
		err := sqldb.QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE name=?`, name).Scan(&seen)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := sqldb.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := sqldb.ExecContext(ctx,
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?, datetime('now'))`, name); err != nil {
			return err
		}
	}
	return nil
}
