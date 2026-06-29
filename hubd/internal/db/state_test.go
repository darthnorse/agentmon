package db

import (
	"context"
	"testing"
)

// enrollTestServer inserts a minimal server row to satisfy the FK on session_state_events.
func enrollTestServer(t *testing.T, d *DB, id string) {
	t.Helper()
	ctx := context.Background()
	err := d.EnrollServer(ctx, Server{
		ID: id, Name: id, Hostname: id,
		URL: "http://localhost:8377", Status: "active",
		Bearer: "b", SigningKey: "k",
	})
	if err != nil {
		t.Fatalf("enrollTestServer(%q): %v", id, err)
	}
}

func TestAppendAndLatestStateEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "srvA")

	e1 := StateEvent{
		ID: "e1", ServerID: "srvA", TargetID: "", Session: "api", Pane: "%0",
		Source: "hook", RawEvent: "{}", DerivedState: "working",
		EventTs:    "2026-06-29 10:00:00.000",
		ReceivedAt: "2026-06-29 10:00:01.000",
	}
	e2 := e1
	e2.ID, e2.DerivedState, e2.ReceivedAt = "e2", "done", "2026-06-29 10:00:05.000"

	if err := d.AppendStateEvent(ctx, e1); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendStateEvent(ctx, e2); err != nil {
		t.Fatal(err)
	}

	got, ok, err := d.LatestSessionEvent(ctx, "srvA", "", "api")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.ID != "e2" || got.DerivedState != "done" {
		t.Fatalf("latest = %+v", got)
	}
}

func TestAppendStateEventRejectsBogusServer(t *testing.T) {
	d := openTestDB(t)
	err := d.AppendStateEvent(context.Background(), StateEvent{
		ID: "x", ServerID: "ghost", Session: "s",
		Source: "hook", RawEvent: "{}", DerivedState: "done",
		EventTs: "t", ReceivedAt: "t",
	})
	if err == nil {
		t.Fatal("expected FK violation for unknown server_id")
	}
}

func TestLatestSessionEventNoRows(t *testing.T) {
	d := openTestDB(t)
	_, ok, err := d.LatestSessionEvent(context.Background(), "srvA", "", "nope")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
