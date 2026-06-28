package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestEnrollAndGetServer(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	in := Server{ID: "web-01", Name: "web-01", Hostname: "web-01",
		URL: "http://10.0.0.9:8377", Status: "pending", Bearer: "b", SigningKey: "k",
		OS: "linux", Arch: "amd64", AgentVersion: "dev"}
	if err := d.EnrollServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetServer(ctx, "web-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "web-01" || got.Status != "pending" || got.Bearer != "b" || got.URL != "http://10.0.0.9:8377" || got.Arch != "amd64" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if _, err := d.GetServer(ctx, "nope"); err != sql.ErrNoRows {
		t.Fatalf("missing server: want ErrNoRows, got %v", err)
	}
}

func TestFindServerByHostname(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "abc123", Name: "n", Hostname: "db-02", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.FindServer(ctx, "db-02") // by hostname, not id
	if err != nil || got.ID != "abc123" {
		t.Fatalf("find by hostname: %+v err=%v", got, err)
	}
	got, err = d.FindServer(ctx, "abc123") // by id
	if err != nil || got.Hostname != "db-02" {
		t.Fatalf("find by id: %+v err=%v", got, err)
	}
}

func TestListServersStatusFilter(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	for _, s := range []Server{
		{ID: "a", Name: "a", Hostname: "a", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"},
		{ID: "b", Name: "b", Hostname: "b", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"},
		{ID: "c", Name: "c", Hostname: "c", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"},
	} {
		if err := d.EnrollServer(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	active, err := d.ListServers(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "b" || active[1].ID != "c" {
		t.Fatalf("active filter: %+v", active)
	}
	all, err := d.ListServers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered: %+v", all)
	}
}

func TestSetStatusDeleteAndTouch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "a", Name: "a", Hostname: "a", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	ok, err := d.SetServerStatus(ctx, "a", "active")
	if err != nil || !ok {
		t.Fatalf("set status: ok=%v err=%v", ok, err)
	}
	if got, _ := d.GetServer(ctx, "a"); got.Status != "active" {
		t.Fatalf("status not updated: %+v", got)
	}
	if ok, _ := d.SetServerStatus(ctx, "ghost", "active"); ok {
		t.Fatal("setting status on a missing id must report not-found")
	}
	if err := d.TouchServerLastSeen(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "a"); got.LastSeenAt == "" {
		t.Fatal("last_seen_at must be set after touch")
	}
	ok, err = d.DeleteServer(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if _, err := d.GetServer(ctx, "a"); err != sql.ErrNoRows {
		t.Fatalf("deleted server still present: %v", err)
	}
}
