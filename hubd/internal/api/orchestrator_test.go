package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

func orchDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.EnrollServer(context.Background(), db.Server{ID: "h1", Name: "h1", Hostname: "h1", URL: "http://a", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	return d
}
func orchReq(method, url, body string) (*http.Request, *httptest.ResponseRecorder) {
	r := withPrincipal(httptest.NewRequest(method, url, strings.NewReader(body)), authz.Principal{ID: "u1"})
	return r, httptest.NewRecorder()
}
func TestRegisterAndListProjects(t *testing.T) {
	database := orchDB(t)
	d := Deps{DB: database, Orch: &fakeOrch{}, Reg: registry.New(database), Audit: audit.NewRecorder(&captureSink{})}
	r, w := orchReq("POST", "/api/v1/orchestrator/projects", `{"name":"proj","repo":"o/r","server_id":"h1","workdir":"/w"}`)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 201 {
		t.Fatalf("post %d %s", w.Code, w.Body.String())
	}
	r, w = orchReq("GET", "/api/v1/orchestrator/projects", "")
	d.OrchestratorProjectsHandler()(w, r)
	var got []map[string]any
	json.NewDecoder(w.Body).Decode(&got)
	if w.Code != 200 || len(got) != 1 || got[0]["base_branch"] != "main" || got[0]["provider"] != "claude" {
		t.Fatalf("got %d %+v", w.Code, got)
	}
	r, w = orchReq("POST", "/api/v1/orchestrator/projects", `{"name":"bad","server_id":"h1","workdir":"/w"}`)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("missing repo code=%d", w.Code)
	}
}
func TestBoardEndpoint(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	database.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "t1")
	database.TransitionEpic(ctx, e.ID, "starting", "escalated", "hub", "needs", "t2")
	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}
	r, w := orchReq("GET", "/api/v1/orchestrator/projects/p1/board", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorBoardHandler()(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"stage":"escalated"`) || !strings.Contains(w.Body.String(), `"events"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestActionsDispatch(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	fo := &fakeOrch{}
	d := Deps{DB: database, Orch: fo, Audit: audit.NewRecorder(&captureSink{})}
	r, w := orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"pause"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	p, _ := database.GetProject(ctx, "p1")
	if w.Code != 200 || !p.Paused || fo.woke == 0 {
		t.Fatalf("pause %d %+v woke=%d", w.Code, p, fo.woke)
	}
	r, w = orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"nope"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("nope=%d", w.Code)
	}
	// epic-scoped actions bind the epic to the URL project
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 9, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	r, w = orchReq("POST", "/api/v1/orchestrator/projects/p2/actions", `{"action":"approve","epic_id":"`+e.ID+`"}`)
	r.SetPathValue("id", "p2")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("cross-project epic action must 404, got %d", w.Code)
	}
	r, w = orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"approve","epic_id":"`+e.ID+`"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("in-project approve = %d %s", w.Code, w.Body.String())
	}
}

func TestActionsDisabledOrchestrator(t *testing.T) {
	database := orchDB(t)
	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})} // Orch nil = disabled
	r, w := orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"pause"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 503 {
		t.Fatalf("disabled orchestrator must 503, got %d", w.Code)
	}
}

func TestProjectPatch(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	r, w := orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"name":"p2","workdir":"/w2","provider":"codex","required_reviews":["cross-model"]}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	p, _ := database.GetProject(ctx, "p1")
	if p.Name != "p2" || p.Workdir != "/w2" || p.Provider != "codex" || len(p.RequiredReviews) != 1 {
		t.Fatalf("got %+v", p)
	}
	if p.BaseBranch != "main" {
		t.Fatalf("absent field must be unchanged, got %q", p.BaseBranch)
	}
	if _, ok := sink.find("project.update"); !ok {
		t.Fatal("missing audit entry")
	}

	for body, why := range map[string]string{
		`{"repo":"o/x"}`:     "repo immutable",
		`{"server_id":"h2"}`: "server_id immutable",
		`{"provider":"gpt"}`: "bad provider",
		`{"name":""}`:        "empty name",
		`{"workdir":""}`:     "empty workdir",
		`{"base_branch":""}`: "empty base_branch",
	} {
		r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", body)
		r.SetPathValue("id", "p1")
		d.OrchestratorProjectPatchHandler()(w, r)
		if w.Code != 400 {
			t.Fatalf("%s: want 400, got %d", why, w.Code)
		}
	}

	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/nope", `{"name":"x"}`)
	r.SetPathValue("id", "nope")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("missing project: want 404, got %d", w.Code)
	}

	d.Orch = nil
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"name":"x"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 503 {
		t.Fatalf("dormant: want 503, got %d", w.Code)
	}
}

func TestProjectDelete(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	r, w := orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "1 active epic") {
		t.Fatalf("active refuse = %d %s", w.Code, w.Body.String())
	}

	database.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "", "t2")
	r, w = orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if _, ok := sink.find("project.delete"); !ok {
		t.Fatal("missing audit entry")
	}

	r, w = orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("gone: want 404, got %d", w.Code)
	}
}
