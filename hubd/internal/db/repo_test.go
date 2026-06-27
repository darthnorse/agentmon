package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUserRepoRoundTrip(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()

	in := User{ID: "u1", Username: "patrik", DisplayName: "Patrik",
		PasswordHash: "$argon2id$...", Status: "active"}
	if err := d.CreateUser(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetUserByUsername(ctx, "patrik")
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("got %+v want %+v", got, in)
	}
}

func TestRecentToleratesNullColumns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	// Insert omitting the nullable columns → they become SQL NULL.
	_, err = d.sql.ExecContext(ctx,
		`INSERT INTO audit_log(id, action, resource, result, ts) VALUES(?,?,?,?, datetime('now'))`,
		"a-null", "login.success", "user:u1", "allow")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed on NULL columns: %v", err)
	}
	if len(rows) != 1 || rows[0].PrincipalID != "" || rows[0].RequestID != "" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestAuditAppendAndRecent(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()

	if err := d.Append(ctx, AuditEntry{ID: "a1", PrincipalID: "u1",
		Action: "terminal.open", Resource: "pane:server-a/default/%3", Result: "allow"}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "terminal.open" {
		t.Fatalf("recent: %+v", rows)
	}
}
