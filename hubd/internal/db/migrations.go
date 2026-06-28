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

// migrate applies every embedded *.sql file in lexical order, tracking applied
// files in schema_migrations. Each file's SQL and its schema_migrations insert
// run together in one transaction (applyMigration), so a failing migration leaves
// no partial schema and is not recorded — safe for non-idempotent migrations.
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
		if err := applyMigration(ctx, sqldb, name, body); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration file's SQL and records it in schema_migrations
// atomically. Any error rolls the whole thing back, so a non-idempotent migration
// never leaves a half-applied schema.
func applyMigration(ctx context.Context, sqldb *sql.DB, name string, body []byte) error {
	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(name, applied_at) VALUES(?, datetime('now'))`, name); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record %s: %w", name, err)
	}
	return tx.Commit()
}
