package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// applyMigration must be atomic: if the SQL fails partway, NOTHING it created
// survives and schema_migrations is not stamped.
func TestApplyMigrationRollsBackOnError(t *testing.T) {
	sqldb, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.sqlite")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if _, err := sqldb.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// First statement is valid, second is a syntax error → the whole file must roll back.
	body := []byte(`CREATE TABLE good (id TEXT); CREATE TABLE bad (id TEXT) NOPE;`)
	if err := applyMigration(context.Background(), sqldb, "9999_broken.sql", body); err == nil {
		t.Fatal("broken migration must error")
	}
	var n string
	if err := sqldb.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='good'`).Scan(&n); err != sql.ErrNoRows {
		t.Fatalf("table 'good' must not survive a rolled-back migration (err=%v)", err)
	}
	if err := sqldb.QueryRowContext(context.Background(),
		`SELECT name FROM schema_migrations WHERE name='9999_broken.sql'`).Scan(&n); err != sql.ErrNoRows {
		t.Fatalf("rolled-back migration must not be recorded (err=%v)", err)
	}
}

func TestServersTableHasEnrollmentColumns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cols := map[string]bool{}
	rows, err := d.sql.QueryContext(context.Background(), `PRAGMA table_info(servers)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	for _, want := range []string{"id", "name", "hostname", "url", "status", "bearer", "signing_key", "labels", "os", "arch", "agent_version", "last_seen_at", "created_at", "updated_at"} {
		if !cols[want] {
			t.Fatalf("servers table missing column %q (have %v)", want, cols)
		}
	}
}

// TestStateEventsReceivedIndexExists verifies that migration 0003 creates the
// idx_state_events_received index used to order LatestSessionEvent queries by
// received_at DESC without a full sort.
func TestStateEventsReceivedIndexExists(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "idx.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var name string
	err = d.sql.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_state_events_received'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("idx_state_events_received index not found after migration: %v", err)
	}
	if name != "idx_state_events_received" {
		t.Fatalf("unexpected index name: %q", name)
	}
}
