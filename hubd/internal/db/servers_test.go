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
	ok, _, err = d.DeleteServer(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if _, err := d.GetServer(ctx, "a"); err != sql.ErrNoRows {
		t.Fatalf("deleted server still present: %v", err)
	}
}

func mustExec(t *testing.T, d *DB, q string, args ...any) {
	t.Helper()
	if _, err := d.sql.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func countForServer(t *testing.T, d *DB, table, serverID string) int {
	t.Helper()
	var n int
	if err := d.sql.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM `+table+` WHERE server_id = ?`, serverID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// A real monitored host accumulates a tmux target, state history, and per-principal
// seen rows — all of which reference the server. A bare DELETE fails the FOREIGN KEY
// check (the original bug), so rm must cascade the monitoring children in one tx.
func TestDeleteServerCascadesMonitoringChildren(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "prism", Name: "prism", Hostname: "prism", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `INSERT INTO tmux_targets(id, server_id, os_user) VALUES('t1','prism','root')`)
	mustExec(t, d, `INSERT INTO session_state_events(id, server_id, tmux_session_name, source, raw_event, derived_state, event_ts, received_at)
		VALUES('e1','prism','main','agent','{}','idle',datetime('now'),datetime('now'))`)
	mustExec(t, d, `INSERT INTO principal_seen(principal_id, server_id, tmux_session_name, last_focused_at)
		VALUES('u1','prism','main',datetime('now'))`)

	found, blocked, err := d.DeleteServer(ctx, "prism")
	if err != nil {
		t.Fatalf("cascade delete must succeed, got err=%v", err)
	}
	if !found || blocked != 0 {
		t.Fatalf("want found=true blocked=0, got found=%v blocked=%d", found, blocked)
	}
	if _, err := d.GetServer(ctx, "prism"); err != sql.ErrNoRows {
		t.Fatalf("server must be gone, got %v", err)
	}
	for _, tbl := range []string{"tmux_targets", "session_state_events", "principal_seen"} {
		if n := countForServer(t, d, tbl, "prism"); n != 0 {
			t.Fatalf("%s must be cascaded, still %d rows", tbl, n)
		}
	}
}

// Orchestrator projects carry epic history of independent value, so rm must REFUSE
// (report the count, delete nothing) rather than silently destroy it — mirroring
// DeleteProject's active-epic guard. The operator removes/re-points them first.
func TestDeleteServerRefusesWhenProjectsBound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "prism", Name: "prism", Hostname: "prism", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProject(ctx, Project{ID: "p1", Name: "proj", Repo: "o/r", ServerID: "prism", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}

	found, blocked, err := d.DeleteServer(ctx, "prism")
	if err != nil {
		t.Fatalf("a refusal is not an error, got %v", err)
	}
	if !found || blocked != 1 {
		t.Fatalf("want found=true blocked=1, got found=%v blocked=%d", found, blocked)
	}
	if _, err := d.GetServer(ctx, "prism"); err != nil {
		t.Fatalf("server must survive a refused delete, got %v", err)
	}
	if _, err := d.GetProject(ctx, "p1"); err != nil {
		t.Fatalf("bound project must survive, got %v", err)
	}
}

// The admit UI's "pending-only" guarantee is enforced atomically by the SQL status
// predicate (not a read-then-write), so these never resurrect or delete a non-pending row.
func TestApproveRejectIfPendingAreAtomicAndPendingOnly(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	for _, s := range []Server{
		{ID: "pending-1", Name: "p", Hostname: "p", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"},
		{ID: "active-1", Name: "a", Hostname: "a", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"},
	} {
		if err := d.EnrollServer(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	if ok, err := d.ApproveIfPending(ctx, "pending-1"); err != nil || !ok {
		t.Fatalf("approve pending: ok=%v err=%v", ok, err)
	}
	if got, _ := d.GetServer(ctx, "pending-1"); got.Status != "active" {
		t.Fatalf("status not active after approve: %+v", got)
	}
	if ok, _ := d.ApproveIfPending(ctx, "pending-1"); ok {
		t.Fatal("re-approving an already-active row must be a no-op (atomic guard)")
	}
	if ok, _ := d.ApproveIfPending(ctx, "active-1"); ok {
		t.Fatal("an active server must NOT be approvable (no resurrect)")
	}
	if ok, _ := d.ApproveIfPending(ctx, "ghost"); ok {
		t.Fatal("approving a missing id must be false")
	}

	if ok, _ := d.RejectIfPending(ctx, "active-1"); ok {
		t.Fatal("RejectIfPending must NOT delete an active server")
	}
	if _, err := d.GetServer(ctx, "active-1"); err != nil {
		t.Fatalf("active server must remain after a refused reject: %v", err)
	}
	if err := d.EnrollServer(ctx, Server{ID: "pending-2", Name: "p2", Hostname: "p2", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.RejectIfPending(ctx, "pending-2"); err != nil || !ok {
		t.Fatalf("reject pending: ok=%v err=%v", ok, err)
	}
	if _, err := d.GetServer(ctx, "pending-2"); err != sql.ErrNoRows {
		t.Fatal("a rejected pending server must be gone")
	}
}
