package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
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
	// run_issue rejects a non-positive issue number before touching GitHub
	r, w = orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"run_issue","issue":0}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("run_issue issue=0 must 400, got %d", w.Code)
	}
}

func TestSetPinnedAction(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(&captureSink{})}

	r, w := orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"set_pinned","on":true}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("set_pinned = %d %s", w.Code, w.Body.String())
	}
	if p, _ := database.GetProject(ctx, "p1"); !p.Pinned {
		t.Fatalf("pinned must be set: %+v", p)
	}

	// The board payload must expose the flag so the home screen can render chips.
	r, w = orchReq("GET", "/api/v1/orchestrator/board", "")
	d.OrchestratorAllBoardHandler()(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pinned":true`) {
		t.Fatalf("board must expose pinned: %d %s", w.Code, w.Body.String())
	}

	// Unknown project id → 404.
	r, w = orchReq("POST", "/api/v1/orchestrator/projects/nope/actions", `{"action":"set_pinned","on":true}`)
	r.SetPathValue("id", "nope")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("set_pinned unknown project = %d", w.Code)
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

func TestProjectPatchTargetGuardedWhileActive(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Target: "old", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	// A non-terminal epic is live → changing target must be refused (it would
	// strand the runner on the old socket: reports lost, control actions misrouted).
	r, w := orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"target":"new"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "target while epics are running") {
		t.Fatalf("active target change: want 400, got %d %s", w.Code, w.Body.String())
	}
	// A no-op target (unchanged value) is not a change → allowed even while active.
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"target":"old"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("no-op target while active: want 200, got %d %s", w.Code, w.Body.String())
	}
	// Once the epic is terminal, the target may change and is persisted.
	database.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "", "t2")
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"target":"new"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("target change after terminal: want 200, got %d %s", w.Code, w.Body.String())
	}
	if p, _ := database.GetProject(ctx, "p1"); p.Target != "new" {
		t.Fatalf("target not persisted, got %q", p.Target)
	}
}

func TestProjectPatchDuplicateName(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "one", Repo: "o/r1", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	database.CreateProject(ctx, db.Project{ID: "p2", Name: "two", Repo: "o/r2", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	d := Deps{DB: database, Orch: &fakeOrch{}}

	// Renaming p1 onto p2's taken name is a UNIQUE(name) collision → 400,
	// distinct from a genuine backend failure (which is now a 500).
	r, w := orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"name":"two"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("duplicate name: want 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestBoardEventsDormantServesProjects(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	// A project registered while the hub had a token, now running dormant
	// (BoardBcast==nil): the SSE snapshot must still carry it — it must not
	// drift from GET /board, which serves the real rows.
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "school-platform", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	d := Deps{DB: database, SSEHeartbeat: time.Hour} // BoardBcast nil → dormant
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{pw: pw, header: make(http.Header)}
	ctx2, cancel := context.WithCancel(ctx)
	req := withPrincipal(httptest.NewRequest("GET", "/api/v1/orchestrator/events", nil).WithContext(ctx2), authz.Principal{ID: "u1"})
	done := make(chan struct{})
	go func() { defer close(done); defer pw.Close(); d.OrchestratorEventsHandler()(rw, req) }()

	sc := bufio.NewScanner(pr)
	var all strings.Builder
	for sc.Scan() {
		all.WriteString(sc.Text())
		all.WriteByte('\n')
		if strings.Contains(all.String(), `"projects":[`) { // snapshot arrived
			break
		}
	}
	cancel()
	<-done
	pr.Close()
	if !strings.Contains(all.String(), `"projects":[{`) || strings.Contains(all.String(), `"projects":[]`) {
		t.Fatalf("dormant snapshot must include the registered project, got %s", all.String())
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

func TestAllBoard(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, Title: "T", IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	d := Deps{DB: database, Orch: &fakeOrch{}}
	r, w := orchReq("GET", "/api/v1/orchestrator/board", "")
	d.OrchestratorAllBoardHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("board = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Enabled  bool              `json:"orchestrator_enabled"`
		Projects []json.RawMessage `json:"projects"`
		Epics    []json.RawMessage `json:"epics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || len(out.Projects) != 1 || len(out.Epics) != 1 {
		t.Fatalf("got %+v", out)
	}

	// Dormant hub: enabled=false, and empty slices must be [] not null.
	empty, err := db.Open(filepath.Join(t.TempDir(), "e.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { empty.Close() })
	d = Deps{DB: empty}
	r, w = orchReq("GET", "/api/v1/orchestrator/board", "")
	d.OrchestratorAllBoardHandler()(w, r)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, `"orchestrator_enabled":true`) {
		t.Fatalf("dormant = %d %s", w.Code, body)
	}
	if !strings.Contains(body, `"projects":[]`) || !strings.Contains(body, `"epics":[]`) {
		t.Fatalf("empty slices must marshal as [], got %s", body)
	}
}

func TestBoardEventsDormant(t *testing.T) {
	database := orchDB(t)
	d := Deps{DB: database, SSEHeartbeat: time.Hour}
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{pw: pw, header: make(http.Header)}
	ctx, cancel := context.WithCancel(context.Background())
	req := withPrincipal(httptest.NewRequest("GET", "/api/v1/orchestrator/events", nil).WithContext(ctx), authz.Principal{ID: "u1"})
	done := make(chan struct{})
	go func() { defer close(done); defer pw.Close(); d.OrchestratorEventsHandler()(rw, req) }()

	sc := bufio.NewScanner(pr)
	var all strings.Builder
	for sc.Scan() {
		all.WriteString(sc.Text())
		all.WriteByte('\n')
		if strings.Contains(all.String(), `"projects":[]`) && strings.Contains(all.String(), `"epics":[]`) {
			break
		}
	}
	cancel()
	<-done
	pr.Close()
	if rw.code == http.StatusServiceUnavailable || !strings.Contains(all.String(), "event: board-snapshot") {
		t.Fatalf("dormant stream code=%d body=%s", rw.code, all.String())
	}
}

type fakeContents struct {
	body            []byte
	err             error
	repo, path, ref string
}

func (f *fakeContents) GetContents(_ context.Context, repo, path, ref string) ([]byte, error) {
	f.repo, f.path, f.ref = repo, path, ref
	return f.body, f.err
}

func TestPlanDocPath(t *testing.T) {
	for needs, want := range map[string]string{
		"": "docs/plans/epic-7.md",
		"plan-gate: plan ready at docs/plans/epic-7.md":  "docs/plans/epic-7.md",
		"plan-gate: plan ready at docs/plans/sub/p.md":   "docs/plans/sub/p.md", // nested under docs/plans/ is fine
		"plan-gate: plan ready at docs/x/plan.md":        "docs/plans/epic-7.md", // outside docs/plans/ → fall back
		"plan-gate: plan ready at README.md":             "docs/plans/epic-7.md", // arbitrary repo file → fall back
		"plan-gate: plan ready at ../../etc/passwd":      "docs/plans/epic-7.md",
		"plan-gate: plan ready at /abs/path.md":          "docs/plans/epic-7.md",
		"something else entirely":                        "docs/plans/epic-7.md",
	} {
		if got := planDocPath(needs, 7); got != want {
			t.Fatalf("planDocPath(%q) = %q, want %q", needs, got, want)
		}
	}
}

func TestEpicPlanHandler(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	fc := &fakeContents{body: []byte("# Plan")}
	d := Deps{DB: database, Orch: &fakeOrch{}, Contents: fc}

	// No branch yet → 409.
	r, w := orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 409 {
		t.Fatalf("branchless = %d %s", w.Code, w.Body.String())
	}

	if ok, err := database.SetEpicPR(ctx, e.ID, 0, "epic/7-x"); err != nil || !ok {
		t.Fatalf("SetEpicPR: ok=%v err=%v", ok, err)
	}

	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"markdown":"# Plan"`) {
		t.Fatalf("plan = %d %s", w.Code, w.Body.String())
	}
	if fc.repo != "o/r" || fc.path != "docs/plans/epic-7.md" || fc.ref != "epic/7-x" {
		t.Fatalf("fetch args %+v", fc)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}

	// Wrong project → 404 (cross-project guard).
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p2/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p2")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("cross-project = %d", w.Code)
	}

	// GitHub 404 → friendly 404; too large → 413; other errors → 502.
	for _, tc := range []struct {
		err  error
		want int
	}{{github.ErrNotFound, 404}, {github.ErrTooLarge, 413}, {errors.New("boom"), 502}} {
		fc.err = tc.err
		r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
		r.SetPathValue("id", "p1")
		r.SetPathValue("epicID", e.ID)
		d.OrchestratorEpicPlanHandler()(w, r)
		if w.Code != tc.want {
			t.Fatalf("err %v = %d, want %d", tc.err, w.Code, tc.want)
		}
	}

	// Contents unset (dormant) → 503.
	d.Contents = nil
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 503 {
		t.Fatalf("dormant = %d", w.Code)
	}
}
