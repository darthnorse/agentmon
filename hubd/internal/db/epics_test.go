package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func seedProject(t *testing.T, d *DB) {
	t.Helper()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(context.Background(), testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertEpicIssue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, err := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 15, Title: "GDPR framework",
		Labels: []string{"agentmon:epic"}, BlockedBy: []int{13},
		IssueState: "open", QueuedAt: "2026-07-10T10:00:00Z", StageUpdatedAt: "2026-07-10T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.Stage != "queued" || e.BlockedBy[0] != 13 {
		t.Fatalf("insert: %+v", e)
	}
	// Second upsert refreshes mirror fields but never resets stage/runtime.
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "2026-07-10T10:01:00Z"); err != nil || !ok {
		t.Fatalf("transition: ok=%v err=%v", ok, err)
	}
	e2, err := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 15, Title: "GDPR consent & retention",
		Labels: []string{"agentmon:epic", "pr-gate"}, BlockedBy: []int{13},
		IssueState: "open", QueuedAt: "x", StageUpdatedAt: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e2.ID != e.ID || e2.Stage != "starting" || e2.Title != "GDPR consent & retention" || len(e2.Labels) != 2 {
		t.Fatalf("upsert: %+v", e2)
	}
}

func TestTransitionEpicGuarded(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, _ := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 16, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if ok, _ := d.TransitionEpic(ctx, e.ID, "planning", "implementing", "report", "", "t1"); ok {
		t.Fatal("stale-from transition must report false")
	}
	if ok, _ := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "spawn", "t1"); !ok {
		t.Fatal("valid transition must succeed")
	}
	got, _ := d.GetEpic(ctx, e.ID)
	if got.Stage != "starting" || got.StartedAt != "t1" || got.StageUpdatedAt != "t1" {
		t.Fatalf("got %+v", got)
	}
	evs, err := d.ListEpicEvents(ctx, e.ID, 10)
	if err != nil || len(evs) != 1 || evs[0].ToStage != "starting" || evs[0].Source != "hub" {
		t.Fatalf("events = %+v err=%v", evs, err)
	}
	// needs set on escalation, cleared on leaving it
	d.SetEpicNeeds(ctx, e.ID, "2 unresolved findings")
	d.TransitionEpic(ctx, e.ID, "starting", "escalated", "hub", "gate", "t2")
	d.TransitionEpic(ctx, e.ID, "escalated", "queued", "user", "retry", "t3")
	got, _ = d.GetEpic(ctx, e.ID)
	if got.Needs != "" {
		t.Fatalf("needs should clear on leaving escalated, got %q", got.Needs)
	}
}

func TestEpicSettersAndLists(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, _ := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 17, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if ok, _ := d.SetEpicAssignment(ctx, e.ID, "epic-17", 1); !ok {
		t.Fatal("assignment")
	}
	if ok, _ := d.SetEpicPR(ctx, e.ID, 61, "epic/17-timetabling"); !ok {
		t.Fatal("pr")
	}
	if ok, _ := d.SetEpicVerdict(ctx, e.ID, `{"uncertain":false}`); !ok {
		t.Fatal("verdict")
	}
	got, _ := d.GetEpicByIssue(ctx, "p1", 17)
	if got.SessionName != "epic-17" || got.Attempt != 1 || got.PRNumber != 61 || got.Branch != "epic/17-timetabling" {
		t.Fatalf("got %+v", got)
	}
	if _, err := d.GetEpicByIssue(ctx, "p1", 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
	all, _ := d.ListEpicsByProject(ctx, "p1")
	nt, _ := d.ListNonTerminalEpics(ctx)
	if len(all) != 1 || len(nt) != 1 {
		t.Fatalf("all=%d nt=%d", len(all), len(nt))
	}
}

func TestEpicEventsSameSecondOrderByInsertion(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, _ := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 30, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	// Same-second ts; explicit IDs chosen so the OLD (id-based) tiebreak
	// would return them in the wrong order. rowid must win instead.
	ts := "2026-07-10T15:00:00Z"
	first := EpicEvent{ID: "a-first", EpicID: e.ID, FromStage: "queued", ToStage: "starting", Source: "hub", Ts: ts}
	second := EpicEvent{ID: "0-second", EpicID: e.ID, FromStage: "starting", ToStage: "planning", Source: "report", Ts: ts}
	if err := d.AppendEpicEvent(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendEpicEvent(ctx, second); err != nil {
		t.Fatal(err)
	}
	evs, err := d.ListEpicEvents(ctx, e.ID, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("evs=%v err=%v", evs, err)
	}
	if evs[0].ID != "0-second" || evs[1].ID != "a-first" {
		t.Fatalf("newest-first insertion order expected, got %q then %q", evs[0].ID, evs[1].ID)
	}
}
