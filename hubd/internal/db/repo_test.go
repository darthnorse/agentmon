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
