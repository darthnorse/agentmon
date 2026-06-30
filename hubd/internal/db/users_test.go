package db

import (
	"context"
	"testing"
)

func TestSeedDefaultUserOnlyWhenEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	seeded, err := d.SeedDefaultUser(ctx, "id1", "admin", "admin", "hash1")
	if err != nil || !seeded {
		t.Fatalf("first seed: seeded=%v err=%v", seeded, err)
	}
	u, err := d.GetUserByUsername(ctx, "admin")
	if err != nil || u.ID != "id1" || u.PasswordHash != "hash1" || u.Status != "active" {
		t.Fatalf("seeded user: %+v err=%v", u, err)
	}

	// A second seed must be a no-op (a user already exists) — and must not overwrite.
	seeded2, err := d.SeedDefaultUser(ctx, "id2", "other", "other", "hash2")
	if err != nil || seeded2 {
		t.Fatalf("second seed must not run: seeded=%v err=%v", seeded2, err)
	}
	if _, err := d.GetUserByUsername(ctx, "other"); err == nil {
		t.Fatal("a second user must NOT have been created once the table is non-empty")
	}
	if u, _ := d.GetUserByUsername(ctx, "admin"); u.PasswordHash != "hash1" {
		t.Fatal("existing user must be left untouched by a re-seed")
	}
}
