package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

func TestServerActionApproveRevokeRemove(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.EnrollServer(ctx, db.Server{ID: "web-01", Name: "web-01", Hostname: "web-01.lan",
		URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(d)

	if _, err := serverAction(ctx, d, rec, "approve", "web-01.lan"); err != nil { // resolve by hostname
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "web-01"); got.Status != "active" {
		t.Fatalf("approve did not activate: %+v", got)
	}
	if _, err := serverAction(ctx, d, rec, "revoke", "web-01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "web-01"); got.Status != "revoked" {
		t.Fatalf("revoke did not set revoked: %+v", got)
	}
	if _, err := serverAction(ctx, d, rec, "rm", "web-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetServer(ctx, "web-01"); err == nil {
		t.Fatal("rm did not delete the row")
	}
	// audited
	rows, _ := d.Recent(ctx, 50)
	var approve, revoke, remove bool
	for _, e := range rows {
		switch e.Action {
		case "server.approve":
			approve = true
		case "server.revoke":
			revoke = true
		case "server.remove":
			remove = true
		}
	}
	if !approve || !revoke || !remove {
		t.Fatalf("lifecycle not audited: approve=%v revoke=%v remove=%v", approve, revoke, remove)
	}
}

func TestServerRmRefusesWhenProjectsBound(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.EnrollServer(ctx, db.Server{ID: "prism", Name: "prism", Hostname: "prism.lan",
		URL: "u", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProject(ctx, db.Project{ID: "p1", Name: "proj", Repo: "o/r",
		ServerID: "prism", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(d)

	if _, err := serverAction(ctx, d, rec, "rm", "prism.lan"); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("want a projects-bound refusal mentioning projects, got %v", err)
	}
	if _, err := d.GetServer(ctx, "prism"); err != nil {
		t.Fatalf("server must survive a refused rm, got %v", err)
	}
	rows, _ := d.Recent(ctx, 50)
	for _, e := range rows {
		if e.Action == "server.remove" {
			t.Fatal("a refused rm must not audit server.remove")
		}
	}
}

func TestServerActionUnknownTarget(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	rec := audit.NewRecorder(d)
	_, err := serverAction(context.Background(), d, rec, "approve", "ghost")
	if err == nil || !strings.Contains(err.Error(), "no server") {
		t.Fatalf("want no-server error, got %v", err)
	}
}

func TestServerListRenders(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()
	d.EnrollServer(ctx, db.Server{ID: "web-01", Name: "web-01", Hostname: "web-01.lan", URL: "http://10.0.0.99:8377", Status: "pending", Bearer: "BEARER_SEKRET_zzz", SigningKey: "SIGNKEY_SEKRET_yyy", OS: "linux", Arch: "amd64", AgentVersion: "dev"})
	out, err := serverList(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web-01", "pending", "amd64"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "BEARER_SEKRET_zzz") || strings.Contains(out, "SIGNKEY_SEKRET_yyy") {
		t.Fatalf("server list must never print secrets:\n%s", out)
	}
	if !strings.Contains(out, "http://10.0.0.99:8377") {
		t.Fatalf("server list must show the dial URL so the operator sees the dial target before approving:\n%s", out)
	}
}
