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
	"agentmon/hubd/internal/orchestrator"
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
func TestProjectRequirementsAPI(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	d := Deps{DB: database, Orch: &fakeOrch{}, Reg: registry.New(database), Audit: audit.NewRecorder(&captureSink{})}

	// CREATE: supplied id preserved; missing id derived from text; blank row dropped.
	body := `{"name":"proj","repo":"o/r","server_id":"h1","workdir":"/w",` +
		`"requirements":[{"text":"Always use RLS"},{"id":"wcag","text":"WCAG 2.2 AA"},{"text":"  "}]}`
	r, w := orchReq("POST", "/api/v1/orchestrator/projects", body)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created projectDTO
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Requirements) != 2 ||
		created.Requirements[0] != (db.Requirement{ID: "always-use-rls", Text: "Always use RLS"}) ||
		created.Requirements[1] != (db.Requirement{ID: "wcag", Text: "WCAG 2.2 AA"}) {
		t.Fatalf("create response requirements = %+v", created.Requirements)
	}
	got, _ := database.GetProject(ctx, created.ID)
	if len(got.Requirements) != 2 || got.Requirements[0].ID != "always-use-rls" {
		t.Fatalf("persisted requirements = %+v", got.Requirements)
	}

	// PATCH must accept AND RETURN requirements, keeping the id stable across a
	// text edit.
	patch := `{"requirements":[{"id":"always-use-rls","text":"Always use row-level security"}]}`
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/"+created.ID, patch)
	r.SetPathValue("id", created.ID)
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	var patched projectDTO
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if len(patched.Requirements) != 1 ||
		patched.Requirements[0] != (db.Requirement{ID: "always-use-rls", Text: "Always use row-level security"}) {
		t.Fatalf("patch response requirements = %+v", patched.Requirements)
	}
	if got, _ = database.GetProject(ctx, created.ID); got.Requirements[0].Text != "Always use row-level security" {
		t.Fatalf("patch must persist the edited text: %+v", got.Requirements)
	}

	// Duplicate resolved ids are rejected at create time (fail closed).
	dup := `{"name":"dup","repo":"o/dup","server_id":"h1","workdir":"/w",` +
		`"requirements":[{"text":"Always use RLS"},{"id":"always-use-rls","text":"Other"}]}`
	r, w = orchReq("POST", "/api/v1/orchestrator/projects", dup)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("duplicate requirement ids must 400, got %d %s", w.Code, w.Body.String())
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

// TestBoardEpicUsageRollup: an epic with usage-ledger rows carries an inline
// usage rollup (tokens = the summed final cumulative, cost priced from the
// rate card); an epic with none omits the field entirely.
func TestBoardEpicUsageRollup(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	eWithUsage, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	eNoUsage, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 8, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "claude-opus-4-8", Input: 100, Output: 50,
	}); err != nil {
		t.Fatal(err)
	}

	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}
	r, w := orchReq("GET", "/api/v1/orchestrator/projects/p1/board", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorBoardHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("board = %d %s", w.Code, w.Body.String())
	}

	var out struct {
		Project projectDTO `json:"project"`
		Epics   []epicDTO  `json:"epics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	var withUsage, noUsage *epicDTO
	for i := range out.Epics {
		switch out.Epics[i].ID {
		case eWithUsage.ID:
			withUsage = &out.Epics[i]
		case eNoUsage.ID:
			noUsage = &out.Epics[i]
		}
	}
	if withUsage == nil {
		t.Fatal("epic with usage rows missing from board")
	}
	if withUsage.Usage == nil {
		t.Fatalf("epic with usage rows must carry a rollup, got %+v", withUsage)
	}
	if withUsage.Usage.Tokens != 150 { // summed final cumulative: Input 100 + Output 50
		t.Fatalf("tokens = 150, got %d", withUsage.Usage.Tokens)
	}
	wantCost := (100*5.0 + 50*25.0) / 1e6 // claude-opus-4-8 rate card
	if withUsage.Usage.Cost == nil || *withUsage.Usage.Cost != wantCost {
		t.Fatalf("cost = %v, got %v", wantCost, withUsage.Usage.Cost)
	}

	if noUsage == nil {
		t.Fatal("epic without usage rows missing from board")
	}
	if noUsage.Usage != nil {
		t.Fatalf("epic with no usage rows must omit usage, got %+v", noUsage.Usage)
	}

	// omitempty must actually drop the key from the wire payload, not just
	// round-trip as a Go nil pointer.
	var raw struct {
		Epics []map[string]json.RawMessage `json:"epics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw.Epics {
		var id string
		json.Unmarshal(e["id"], &id)
		_, hasUsage := e["usage"]
		switch id {
		case eWithUsage.ID:
			if !hasUsage {
				t.Fatalf("epic with usage rows must include the usage key: %s", w.Body.String())
			}
		case eNoUsage.ID:
			if hasUsage {
				t.Fatalf("epic with no usage rows must omit the usage key entirely: %s", w.Body.String())
			}
		}
	}

	// The project-level rollup sums its epics' usage.
	if out.Project.Usage == nil || out.Project.Usage.Tokens != 150 {
		t.Fatalf("project rollup = sum of epic rollups (150), got %+v", out.Project.Usage)
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

// C1: flipping a pin must fan a project-level board change so OTHER connected
// clients refresh their home-header chips (the initiator refreshes locally).
func TestSetPinnedBroadcasts(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	bcast := orchestrator.NewBoardBroadcaster()
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(&captureSink{}), BoardBcast: bcast}

	// Publish is synchronous, so the change is queued by the time the handler
	// returns — a non-blocking read must find it.
	_, ch, cancel := bcast.Subscribe()
	defer cancel()

	r, w := orchReq("POST", "/api/v1/orchestrator/projects/p1/actions", `{"action":"set_pinned","on":true}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorActionsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("set_pinned = %d %s", w.Code, w.Body.String())
	}
	select {
	case c := <-ch:
		if c.ProjectID != "p1" {
			t.Fatalf("board change project = %q, want p1", c.ProjectID)
		}
		// An empty stage is what keeps the web-push dispatcher from firing on a pin.
		if c.Stage != "" {
			t.Fatalf("pin change must carry no stage, got %q", c.Stage)
		}
	default:
		t.Fatal("set_pinned did not publish a board change")
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

func TestProjectPatchWorkdirGuardedWhileActive(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Target: "old", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	// A non-terminal epic is live → changing workdir must be refused: the merge
	// reap resolves the worktree relative to workdir, so a change would target the
	// wrong clone.
	r, w := orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"workdir":"/w2"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "workdir while epics are running") {
		t.Fatalf("active workdir change: want 400, got %d %s", w.Code, w.Body.String())
	}
	// A no-op workdir (unchanged value) is not a change → allowed even while active.
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"workdir":"/w"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("no-op workdir while active: want 200, got %d %s", w.Code, w.Body.String())
	}
	// Once the epic is terminal, workdir may change and is persisted.
	database.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "", "t2")
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"workdir":"/w2"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("workdir change after terminal: want 200, got %d %s", w.Code, w.Body.String())
	}
	if p, _ := database.GetProject(ctx, "p1"); p.Workdir != "/w2" {
		t.Fatalf("workdir not persisted, got %q", p.Workdir)
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
		"plan-gate: plan ready at docs/plans/epic-7.md": "docs/plans/epic-7.md",
		"plan-gate: plan ready at docs/plans/sub/p.md":  "docs/plans/sub/p.md",  // nested under docs/plans/ is fine
		"plan-gate: plan ready at docs/x/plan.md":       "docs/plans/epic-7.md", // outside docs/plans/ → fall back
		"plan-gate: plan ready at README.md":            "docs/plans/epic-7.md", // arbitrary repo file → fall back
		"plan-gate: plan ready at ../../etc/passwd":     "docs/plans/epic-7.md",
		"plan-gate: plan ready at /abs/path.md":         "docs/plans/epic-7.md",
		"something else entirely":                       "docs/plans/epic-7.md",
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

// TestEpicUsageHandler: the epic usage-detail endpoint resolves the epic
// exactly as the plan handler does (same cross-project 404 guard) and
// returns orchestrator.DeriveEpicUsage's full breakdown verbatim, not a
// collapsed rollup.
func TestEpicUsageHandler(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "claude-opus-4-8", Input: 100, Output: 50,
	}); err != nil {
		t.Fatal(err)
	}

	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}

	// Unauthenticated (no principal in context) → 403, not 200.
	r := httptest.NewRequest("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/usage", nil)
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	w := httptest.NewRecorder()
	d.OrchestratorEpicUsageHandler()(w, r)
	if w.Code == 200 {
		t.Fatalf("unauthenticated must not be 200, got %d %s", w.Code, w.Body.String())
	}
	if w.Code != 403 {
		t.Fatalf("unauthenticated = %d, want 403", w.Code)
	}

	// Unknown epic → 404.
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/does-not-exist/usage", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", "does-not-exist")
	d.OrchestratorEpicUsageHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown epic = %d, want 404", w.Code)
	}

	// Known epic with usage rows → full derived breakdown.
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/usage", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicUsageHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("epic usage = %d %s", w.Code, w.Body.String())
	}
	var got orchestrator.EpicUsage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tokens.Total != 150 {
		t.Fatalf("tokens.total = 150, got %d", got.Tokens.Total)
	}
	wantCost := (100*5.0 + 50*25.0) / 1e6 // claude-opus-4-8 rate card
	if got.Cost == nil || *got.Cost != wantCost {
		t.Fatalf("cost = %v, got %v", wantCost, got.Cost)
	}
	if len(got.Attempts) != 1 || got.Attempts[0].Attempt != 1 {
		t.Fatalf("attempts = %+v", got.Attempts)
	}
	if len(got.Attempts[0].Stages) != 1 || got.Attempts[0].Stages[0].Stage != "planning" {
		t.Fatalf("stages = %+v", got.Attempts[0].Stages)
	}
}

// TestProjectUsageHandler: the project usage-detail endpoint aggregates
// across every epic's ledger — by_stage folds every attempt's stages by
// stage name across epics, by_model folds by (provider,model) across every
// stage, and costs sum whatever's priced (nil only when nothing in that
// scope is priced) rather than failing closed on a single unpriced model.
func TestProjectUsageHandler(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 8, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "claude-opus-4-8", Input: 100, Output: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 8, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "claude-haiku-4-5-20251001", Input: 200, Output: 100,
	}); err != nil {
		t.Fatal(err)
	}

	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}

	// Unauthenticated → not 200.
	r := httptest.NewRequest("GET", "/api/v1/orchestrator/projects/p1/usage", nil)
	r.SetPathValue("id", "p1")
	w := httptest.NewRecorder()
	d.OrchestratorProjectUsageHandler()(w, r)
	if w.Code == 200 {
		t.Fatalf("unauthenticated must not be 200, got %d %s", w.Code, w.Body.String())
	}

	// Empty project (no rows) → zeroed DTO, still 200.
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p2/usage", "")
	r.SetPathValue("id", "p2")
	d.OrchestratorProjectUsageHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("empty project = %d, want 200", w.Code)
	}
	var empty projectUsageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Tokens.Total != 0 || empty.Cost != nil || len(empty.ByStage) != 0 || len(empty.ByModel) != 0 {
		t.Fatalf("empty project dto = %+v", empty)
	}

	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/usage", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectUsageHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("project usage = %d %s", w.Code, w.Body.String())
	}
	var got projectUsageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	opusCost := (100*5.0)/1e6 + (50*25.0)/1e6
	haikuCost := (200*1.0)/1e6 + (100*5.0)/1e6
	wantTotal := opusCost + haikuCost

	if got.Tokens.Total != 450 { // 150 (epic 7) + 300 (epic 8)
		t.Fatalf("project tokens.total = 450, got %d", got.Tokens.Total)
	}
	if got.Cost == nil || *got.Cost != wantTotal {
		t.Fatalf("project cost = %v, got %v", wantTotal, got.Cost)
	}

	if len(got.ByStage) != 1 || got.ByStage[0].Stage != "planning" {
		t.Fatalf("by_stage = %+v", got.ByStage)
	}
	if got.ByStage[0].Tokens.Total != 450 {
		t.Fatalf("by_stage[0].tokens.total = 450, got %d", got.ByStage[0].Tokens.Total)
	}
	if got.ByStage[0].Cost == nil || *got.ByStage[0].Cost != wantTotal {
		t.Fatalf("by_stage[0].cost = %v, got %v", wantTotal, got.ByStage[0].Cost)
	}

	if len(got.ByModel) != 2 {
		t.Fatalf("by_model = %+v", got.ByModel)
	}
	var gotOpus, gotHaiku *orchestrator.ModelUsage
	for i := range got.ByModel {
		switch got.ByModel[i].Model {
		case "claude-opus-4-8":
			gotOpus = &got.ByModel[i]
		case "claude-haiku-4-5-20251001":
			gotHaiku = &got.ByModel[i]
		}
	}
	if gotOpus == nil || gotOpus.Tokens.Total != 150 || gotOpus.Cost == nil || *gotOpus.Cost != opusCost {
		t.Fatalf("by_model opus = %+v", gotOpus)
	}
	if gotHaiku == nil || gotHaiku.Tokens.Total != 300 || gotHaiku.Cost == nil || *gotHaiku.Cost != haikuCost {
		t.Fatalf("by_model haiku = %+v", gotHaiku)
	}
}

// TestProjectUsageMixedModelCostFailsClosed guards the Should-Fix finding:
// projectUsageRollup (board) summed non-nil PER-EPIC costs, skipping any nil
// one, while aggregateProjectUsage/sumKnownCosts (project endpoint) summed
// non-nil PER-MODEL costs across the whole project. When a single epic mixes
// a priced model with an unpriced one in the same stage, DeriveEpicUsage
// already fails that epic's own Cost closed to nil — so the OLD board rollup
// (its only epic contributes a nil cost) skipped it and landed on nil, while
// the OLD project endpoint still summed the priced model's known share and
// reported a real number, silently dropping the unpriced model's unknown
// share. The two surfaces disagreed on the same project's total dollars.
// Both must now report nil uniformly (fail-closed, matching DeriveEpicUsage),
// while by_model rows stay independently priced per model.
func TestProjectUsageMixedModelCostFailsClosed(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	// Both rows share one captured_at (one runner report = one boundary), so
	// both models land in the SAME interval and SAME stage of the SAME
	// epic — the mixed scope the finding is about, not two separately-priced
	// stages/epics that each happen to be fully known or fully unknown.
	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "claude-opus-4-8", Input: 100, Output: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertUsage(ctx, db.UsageRow{
		ProjectID: "p1", ProjectName: "p", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z",
		Provider: "claude", Model: "future-model-x", Input: 40, Output: 20,
	}); err != nil {
		t.Fatal(err)
	}

	d := Deps{DB: database, Audit: audit.NewRecorder(&captureSink{})}

	// GET /projects (board rollup) — project usage.cost must be null.
	r, w := orchReq("GET", "/api/v1/orchestrator/projects", "")
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("list projects = %d %s", w.Code, w.Body.String())
	}
	var list []projectDTO
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var p1 *projectDTO
	for i := range list {
		if list[i].ID == "p1" {
			p1 = &list[i]
		}
	}
	if p1 == nil {
		t.Fatal("p1 missing from project list")
	}
	if p1.Usage == nil {
		t.Fatalf("p1 usage must be present (it has ledger rows), got nil")
	}
	if p1.Usage.Cost != nil {
		t.Fatalf("board rollup cost must be nil when the project mixes an unpriced model, got %v", *p1.Usage.Cost)
	}

	// GET /projects/{id}/usage — project total AND the mixed stage must be
	// null; by_model stays independently priced per model.
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/usage", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectUsageHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("project usage = %d %s", w.Code, w.Body.String())
	}
	var got projectUsageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Cost != nil {
		t.Fatalf("project-total cost must be nil when any contributing model is unpriced, got %v", *got.Cost)
	}
	if len(got.ByStage) != 1 || got.ByStage[0].Stage != "planning" {
		t.Fatalf("by_stage = %+v", got.ByStage)
	}
	if got.ByStage[0].Cost != nil {
		t.Fatalf("by_stage[planning].cost must be nil (mixes an unpriced model), got %v", *got.ByStage[0].Cost)
	}

	if len(got.ByModel) != 2 {
		t.Fatalf("by_model = %+v", got.ByModel)
	}
	var gotOpus, gotUnpriced *orchestrator.ModelUsage
	for i := range got.ByModel {
		switch got.ByModel[i].Model {
		case "claude-opus-4-8":
			gotOpus = &got.ByModel[i]
		case "future-model-x":
			gotUnpriced = &got.ByModel[i]
		}
	}
	wantOpusCost := (100*5.0)/1e6 + (50*25.0)/1e6
	if gotOpus == nil || gotOpus.Cost == nil || *gotOpus.Cost != wantOpusCost {
		t.Fatalf("by_model opus must stay independently priced even though the aggregate is nil, got %+v", gotOpus)
	}
	if gotUnpriced == nil || gotUnpriced.Cost != nil {
		t.Fatalf("by_model future-model-x must stay nil (unknown to the rate card), got %+v", gotUnpriced)
	}
}
