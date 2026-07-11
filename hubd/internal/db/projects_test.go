package db

import (
	"context"
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
