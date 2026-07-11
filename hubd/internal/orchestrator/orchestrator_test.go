package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// ---- fakes ----

type fakeGH struct {
	issues   map[int]github.Issue
	prs      map[int]github.PullRequest
	checks   map[string][]github.CheckRun
	merged   []int
	labels   [][2]string // [issue-or-pr, label]
	comments []string
}

func (f *fakeGH) GetIssue(_ context.Context, _ string, n int) (github.Issue, error) {
	return f.issues[n], nil
}
func (f *fakeGH) ListIssuesSince(_ context.Context, _, _ string) ([]github.Issue, error) {
	var out []github.Issue
	for _, is := range f.issues {
		out = append(out, is)
	}
	return out, nil
}
func (f *fakeGH) GetPullRequest(_ context.Context, _ string, n int) (github.PullRequest, error) {
	return f.prs[n], nil
}
func (f *fakeGH) ListCheckRuns(_ context.Context, _, ref string) ([]github.CheckRun, error) {
	return f.checks[ref], nil
}
func (f *fakeGH) MergePR(_ context.Context, _ string, n int, _ string) error {
	f.merged = append(f.merged, n)
	pr := f.prs[n]
	pr.Merged = true
	f.prs[n] = pr
	return nil
}
func (f *fakeGH) CreateIssueComment(_ context.Context, _ string, _ int, body string) error {
	f.comments = append(f.comments, body)
	return nil
}
func (f *fakeGH) AddLabels(_ context.Context, _ string, n int, ls []string) error {
	for _, l := range ls {
		f.labels = append(f.labels, [2]string{SessionNameFor(n), l})
	}
	return nil
}
func (f *fakeGH) RemoveLabel(_ context.Context, _ string, _ int, _ string) error { return nil }

type fakeAgents struct {
	created  []shared.CreateSessionRequest
	reports  []shared.OrchestratorReport
	spawnErr error
}

func (f *fakeAgents) CreateSession(_ context.Context, _ db.Server, _ string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error) {
	if f.spawnErr != nil {
		return shared.CreateSessionResponse{}, f.spawnErr
	}
	f.created = append(f.created, req)
	return shared.CreateSessionResponse{Name: req.Name}, nil
}
func (f *fakeAgents) DrainReports(_ context.Context, _ db.Server, _ string) ([]shared.OrchestratorReport, error) {
	out := f.reports
	f.reports = nil
	return out, nil
}

type fakeReg struct{}

func (fakeReg) Get(_ context.Context, id string) (db.Server, bool, error) {
	return db.Server{ID: id, URL: "http://a", Bearer: "b", Status: "active"}, true, nil
}

type fakeLive struct{ alive map[string]bool }

func (f fakeLive) Session(_, _, name string) (state.SessionView, bool) {
	return state.SessionView{Session: name}, f.alive[name]
}

func newTestOrch(t *testing.T, gh *fakeGH, ag *fakeAgents, live fakeLive) (*Orchestrator, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := d.EnrollServer(ctx, db.Server{ID: "h1", Name: "h1", Hostname: "h1",
		URL: "http://a", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProject(ctx, db.Project{ID: "p1", Name: "proj", Repo: "o/r",
		ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude",
		RequiredReviews: []string{"codex"}, MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	clock := "2026-07-10T14:00:00Z"
	o := New(Deps{DB: d, GH: gh, Agents: ag, Reg: fakeReg{}, Live: live,
		Bcast: NewBoardBroadcaster(),
		Cfg:   config.OrchestratorCfg{MaxAttempts: 2},
		Now:   func() string { return clock }})
	return o, d
}

func TestTickSyncsAndSpawns(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{
		16: {Number: 16, Title: "Curriculum", State: "open", Labels: []string{"agentmon:epic"}},
		99: {Number: 99, Title: "unlabeled", State: "open"},
	}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	ctx := context.Background()
	o.Tick(ctx)
	e, err := d.GetEpicByIssue(ctx, "p1", 16)
	if err != nil {
		t.Fatal(err)
	}
	if e.Stage != "starting" || e.SessionName != "epic-16" || e.Attempt != 1 {
		t.Fatalf("epic = %+v", e)
	}
	if len(ag.created) != 1 || ag.created[0].Command != `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline 16"` || ag.created[0].Cwd != "/w" {
		t.Fatalf("created = %+v", ag.created)
	}
	if _, err := d.GetEpicByIssue(ctx, "p1", 99); err == nil {
		t.Fatal("unlabeled issue must not be mirrored")
	}
}

func TestReportsAdvanceAndGateMerges(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 1, resolved: 1, unresolved: 0}\ntests: {passed: 5, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s", HeadRef: "epic/16-x"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{"epic-16": true}})
	ctx := context.Background()
	o.Tick(ctx) // sync + spawn → starting
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: "epic-16", Ts: "t"},
		{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: "epic-16", Ts: "t"},
	}
	o.Tick(ctx) // drain → pr_open, then gate → merged
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" || e.PRNumber != 61 || e.Branch != "epic/16-x" {
		t.Fatalf("epic = %+v", e)
	}
	if len(gh.merged) != 1 || gh.merged[0] != 61 {
		t.Fatalf("merged = %v", gh.merged)
	}
}

func TestGateEscalatesOnUnresolvedAndApproveRecovers(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 3, resolved: 1, unresolved: 2}\ntests: {passed: 5, failed: 0}\n" +
		"uncertain: false\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{"epic-16": true}})
	ctx := context.Background()
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: "epic-16", Ts: "t"}}
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "escalated" || e.Needs == "" {
		t.Fatalf("epic = %+v", e)
	}
	if err := o.Approve(ctx, e.ID, "user:admin"); err != nil {
		t.Fatal(err)
	}
	e, _ = d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" || len(gh.merged) != 1 {
		t.Fatalf("after approve: %+v merged=%v", e, gh.merged)
	}
}

func TestStallOnDeadSession(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}}) // session never alive
	ctx := context.Background()
	o.Tick(ctx) // spawn → starting
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: "epic-16", Ts: "t"}}
	o.Tick(ctx)
	o.Tick(ctx) // grace tick passed; session still gone → stalled
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "stalled" {
		t.Fatalf("epic = %+v", e)
	}
}
