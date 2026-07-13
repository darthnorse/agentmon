package db

import (
	"context"
	"errors"
	"testing"
)

func testProject(server string) Project {
	return Project{
		ID: "p1", Name: "school-platform", Repo: "darthnorse/school-platform",
		ServerID: server, Target: "", Workdir: "/srv/school-platform",
		BaseBranch: "main", Provider: "claude",
		RequiredReviews: []string{"specialist", "codex"}, MaxParallel: 1,
	}
}

func TestProjectRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "darthnorse/school-platform" || got.MaxParallel != 1 || got.Paused {
		t.Fatalf("got %+v", got)
	}
	if len(got.RequiredReviews) != 2 || got.RequiredReviews[1] != "codex" {
		t.Fatalf("required reviews = %v", got.RequiredReviews)
	}
	byRepo, err := d.GetProjectByRepo(ctx, "darthnorse/school-platform")
	if err != nil || byRepo.ID != "p1" {
		t.Fatalf("byRepo = %+v err=%v", byRepo, err)
	}
	list, err := d.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
}

func TestProjectSetters(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.SetProjectPaused(ctx, "p1", true); err != nil || !ok {
		t.Fatalf("pause: ok=%v err=%v", ok, err)
	}
	if ok, err := d.SetProjectMaxParallel(ctx, "p1", 3); err != nil || !ok {
		t.Fatalf("maxpar: ok=%v err=%v", ok, err)
	}
	got, _ := d.GetProject(ctx, "p1")
	if !got.Paused || got.MaxParallel != 3 {
		t.Fatalf("got %+v", got)
	}
	if ok, _ := d.SetProjectPaused(ctx, "nope", true); ok {
		t.Fatal("pause on missing id should report false")
	}
}

func TestProjectRequireCIRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	p := testProject("aigallery")
	p.RequireCI = true
	if err := d.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetProject(ctx, "p1")
	if err != nil || !got.RequireCI {
		t.Fatalf("RequireCI must round-trip: %+v err=%v", got, err)
	}
}

func TestProjectSetPinned(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	// New projects default to unpinned.
	got, _ := d.GetProject(ctx, "p1")
	if got.Pinned {
		t.Fatalf("new project must default unpinned: %+v", got)
	}
	// Pin it; the new value must survive GetProject and ListProjects.
	if ok, err := d.SetProjectPinned(ctx, "p1", true); err != nil || !ok {
		t.Fatalf("pin: ok=%v err=%v", ok, err)
	}
	got, _ = d.GetProject(ctx, "p1")
	if !got.Pinned {
		t.Fatalf("pinned must round-trip via GetProject: %+v", got)
	}
	list, _ := d.ListProjects(ctx)
	if len(list) != 1 || !list[0].Pinned {
		t.Fatalf("pinned must round-trip via ListProjects: %+v", list)
	}
	// Unknown id reports found=false, no error.
	if ok, _ := d.SetProjectPinned(ctx, "nope", true); ok {
		t.Fatal("pin on missing id should report false")
	}
}

func TestGetProjectByRepoIsCaseInsensitive(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	// GitHub slugs are case-insensitive but case-preserving: webhooks carry
	// canonical casing that may differ from what was typed at registration.
	got, err := d.GetProjectByRepo(ctx, "DarthNorse/School-Platform")
	if err != nil || got.ID != "p1" {
		t.Fatalf("case-insensitive lookup failed: %+v err=%v", got, err)
	}
	// And the UNIQUE constraint must reject a differently-cased duplicate.
	dup := testProject("aigallery")
	dup.ID, dup.Name, dup.Repo = "p2", "dupe", "DARTHNORSE/school-platform"
	if err := d.CreateProject(ctx, dup); err == nil {
		t.Fatal("differently-cased duplicate repo must violate UNIQUE")
	}
}

func projDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	d := openTestDB(t)
	enrollTestServer(t, d, "h1")
	ctx := context.Background()
	if err := d.CreateProject(ctx, Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	return d, ctx
}

func TestUpdateProject(t *testing.T) {
	d, ctx := projDB(t)
	ok, err := d.UpdateProject(ctx, Project{ID: "p1", Name: "p2", Workdir: "/w2", Target: "tgt", BaseBranch: "dev", Provider: "codex", RequiredReviews: []string{"cross-model"}})
	if err != nil || !ok {
		t.Fatalf("update: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "p2" || p.Workdir != "/w2" || p.Target != "tgt" || p.BaseBranch != "dev" || p.Provider != "codex" || len(p.RequiredReviews) != 1 || p.RequiredReviews[0] != "cross-model" {
		t.Fatalf("got %+v", p)
	}
	if p.Repo != "o/r" || p.ServerID != "h1" {
		t.Fatalf("immutable fields changed: %+v", p)
	}
	if ok, err := d.UpdateProject(ctx, Project{ID: "nope", Name: "x", Workdir: "/w", BaseBranch: "main", Provider: "claude"}); err != nil || ok {
		t.Fatalf("missing project: ok=%v err=%v", ok, err)
	}
}

func TestUpdateProjectDuplicateNameIsSentinel(t *testing.T) {
	d, ctx := projDB(t)
	// A second project whose name p1's rename will collide with.
	if err := d.CreateProject(ctx, Project{ID: "p2", Name: "taken", Repo: "o/r2", ServerID: "h1", Workdir: "/w2", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	// UNIQUE(name) violation must surface as the ErrDuplicateName sentinel so
	// the API can map it to 400 while a genuine failure still becomes a 500.
	if _, err := d.UpdateProject(ctx, Project{ID: "p1", Name: "taken", Workdir: "/w", BaseBranch: "main", Provider: "claude"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate rename: got %v, want ErrDuplicateName", err)
	}
}

func TestUpdateProjectNonConstraintErrorPassesThrough(t *testing.T) {
	d, ctx := projDB(t)
	d.Close() // any subsequent statement fails with a non-constraint error
	// A closed-DB (or lock/IO) failure must NOT be masquerade as ErrDuplicateName —
	// otherwise the handler would 400 a real outage as "name already in use".
	if _, err := d.UpdateProject(ctx, Project{ID: "p1", Name: "x", Workdir: "/w", BaseBranch: "main", Provider: "claude"}); err == nil || errors.Is(err, ErrDuplicateName) {
		t.Fatalf("non-constraint failure must pass through unchanged, got %v", err)
	}
}

func TestDeleteProjectRefusesActiveEpics(t *testing.T) {
	d, ctx := projDB(t)
	e, err := d.UpsertEpicIssue(ctx, Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	if err != nil {
		t.Fatal(err)
	}
	found, active, err := d.DeleteProject(ctx, "p1")
	if err != nil || !found || active != 1 {
		t.Fatalf("active refuse: found=%v active=%d err=%v", found, active, err)
	}
	if _, err := d.GetProject(ctx, "p1"); err != nil {
		t.Fatalf("project must survive a refused delete: %v", err)
	}
	// Terminal epic (with an event row) → delete succeeds and cascades.
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "n", "t2"); err != nil || !ok {
		t.Fatalf("transition: %v", err)
	}
	if err := d.AppendEpicEvent(ctx, EpicEvent{ID: "ev1", EpicID: e.ID, FromStage: "queued", ToStage: "canceled", Source: "user:u1", Ts: "t2"}); err != nil {
		t.Fatal(err)
	}
	found, active, err = d.DeleteProject(ctx, "p1")
	if err != nil || !found || active != 0 {
		t.Fatalf("delete: found=%v active=%d err=%v", found, active, err)
	}
	if _, err := d.GetProject(ctx, "p1"); err == nil {
		t.Fatal("project must be gone")
	}
	if _, _, err := d.DeleteProject(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if found, _, _ := d.DeleteProject(ctx, "p1"); found {
		t.Fatal("second delete must report not-found")
	}
}
