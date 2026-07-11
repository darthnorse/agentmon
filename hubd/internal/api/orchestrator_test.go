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
	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}
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
}
