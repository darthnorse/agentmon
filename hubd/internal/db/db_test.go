package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenRunsMigrations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// All Phase 1+ tables must exist after Open.
	want := []string{"users", "servers", "tmux_targets",
		"session_state_events", "principal_seen", "audit_log"}
	for _, tbl := range want {
		var name string
		err := d.sql.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.sqlite")
	d1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d1.Close()
	d2, err := Open(p) // re-open must not fail on already-applied migrations
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	d2.Close()
}
