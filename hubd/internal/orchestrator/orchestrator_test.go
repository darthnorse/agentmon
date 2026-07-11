package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// ---- fakes ----

type fakeGH struct {
	issues     map[int]github.Issue
	prs        map[int]github.PullRequest
	checks     map[string][]github.CheckRun
	merged     []int
	mergedSHAs []string
	labels     [][2]string // [issue number, label]
	comments   []string
}

func (f *fakeGH) GetIssue(_ context.Context, _ string, n int) (github.Issue, error) {
	return f.issues[n], nil
}
func (f *fakeGH) ListIssuesLabeledSince(_ context.Context, _, label, _ string) ([]github.Issue, error) {
	var out []github.Issue
	for _, is := range f.issues {
		for _, l := range is.Labels {
			if l == label {
				out = append(out, is)
				break
			}
		}
	}
	return out, nil
}
func (f *fakeGH) GetPullRequest(_ context.Context, _ string, n int) (github.PullRequest, error) {
	return f.prs[n], nil
}
func (f *fakeGH) ListCheckRuns(_ context.Context, _, ref string) ([]github.CheckRun, error) {
	return f.checks[ref], nil
}
func (f *fakeGH) MergePR(_ context.Context, _ string, n int, sha string) error {
	f.merged = append(f.merged, n)
	f.mergedSHAs = append(f.mergedSHAs, sha)
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
		f.labels = append(f.labels, [2]string{strconv.Itoa(n), l})
	}
	return nil
}

type fakeAgents struct {
	created   []shared.CreateSessionRequest
	reports   []shared.OrchestratorReport
	drainAcks [][2]any
	killed    []string
	killErr   error
	spawnErr  error
}

func (f *fakeAgents) CreateSession(_ context.Context, _ db.Server, _ string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error) {
	if f.spawnErr != nil {
		return shared.CreateSessionResponse{}, f.spawnErr
	}
	f.created = append(f.created, req)
	return shared.CreateSessionResponse{Name: req.Name}, nil
}
func (f *fakeAgents) DrainReports(_ context.Context, _ db.Server, _, instance string, ack uint64) (shared.OrchestratorReportBatch, error) {
	f.drainAcks = append(f.drainAcks, [2]any{instance, ack})
	out := f.reports
	f.reports = nil
	return shared.OrchestratorReportBatch{Instance: "test-instance", Cursor: uint64(len(out)), Reports: out}, nil
}

func (f *fakeAgents) KillSession(_ context.Context, _ db.Server, _, name string) error {
	f.killed = append(f.killed, name)
	return f.killErr
}

type fakeReg struct{}

func (fakeReg) Get(_ context.Context, id string) (db.Server, bool, error) {
	return db.Server{ID: id, URL: "http://a", Bearer: "b", Status: "active"}, true, nil
}

// fakeLive mimics the projection's real behavior: views carry the AGENT-
// resolved target label ("default"), never the project's raw Target config.
type fakeLive struct{ alive map[string]bool }

func (f fakeLive) Server(_ string) []state.SessionView {
	var out []state.SessionView
	for name, ok := range f.alive {
		if ok {
			out = append(out, state.SessionView{Target: "default", Session: name})
		}
	}
	return out
}

// sessionName is the expected spawned name for the default test project.
func sessionName(issue int) string { return SessionNameFor("proj", issue, 1) }

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

func TestDrainAcksPreviousBatchOnNextPoll(t *testing.T) {
	ag := &fakeAgents{reports: []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 999, Stage: shared.EpicPlanning, Session: "s", Ts: "t"}}}
	o, d := newTestOrch(t, &fakeGH{}, ag, fakeLive{})
	ctx := context.Background()
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	o.drainReports(ctx, p) // batch of 1 (epic unknown → dropped) — cursor 1 remembered
	o.drainReports(ctx, p) // must echo instance+cursor as the ack
	if len(ag.drainAcks) != 2 {
		t.Fatalf("drains = %d", len(ag.drainAcks))
	}
	if ag.drainAcks[0] != [2]any{"", uint64(0)} {
		t.Fatalf("first drain must ack nothing: %+v", ag.drainAcks[0])
	}
	if ag.drainAcks[1] != [2]any{"test-instance", uint64(1)} {
		t.Fatalf("second drain must ack the first batch: %+v", ag.drainAcks[1])
	}
}

// spawnEpic16 boots one epic through Tick so it holds a session assignment
// (mirrors TestTickSyncsAndSpawns' setup).
func spawnEpic16(t *testing.T, ag *fakeAgents) (*Orchestrator, *db.DB, db.Epic) {
	t.Helper()
	gh := &fakeGH{issues: map[int]github.Issue{
		16: {Number: 16, Title: "Epic", State: "open", Labels: []string{"agentmon:epic"}},
	}}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	o.Tick(context.Background())
	e, err := d.GetEpicByIssue(context.Background(), "p1", 16)
	if err != nil || e.SessionName == "" {
		t.Fatalf("epic not spawned: %+v err=%v", e, err)
	}
	return o, d, e
}

func TestCancelKillsRunnerSession(t *testing.T) {
	ag := &fakeAgents{}
	o, _, e := spawnEpic16(t, ag)
	if err := o.Cancel(context.Background(), e.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if len(ag.killed) != 1 || ag.killed[0] != e.SessionName {
		t.Fatalf("killed = %v, want [%s]", ag.killed, e.SessionName)
	}
}

func TestRetryKillsPredecessorSession(t *testing.T) {
	ag := &fakeAgents{}
	o, d, e := spawnEpic16(t, ag)
	ctx := context.Background()
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "stalled", "hub", "test", "2026-07-10T14:01:00Z"); err != nil || !ok {
		t.Fatalf("force stall: ok=%v err=%v", ok, err)
	}
	if err := o.Retry(ctx, e.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if len(ag.killed) != 1 || ag.killed[0] != e.SessionName {
		t.Fatalf("killed = %v, want [%s]", ag.killed, e.SessionName)
	}
}

func TestKillFailureDoesNotBlockRetry(t *testing.T) {
	ag := &fakeAgents{killErr: errors.New("agent unreachable")}
	o, d, e := spawnEpic16(t, ag)
	ctx := context.Background()
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "stalled", "hub", "test", "2026-07-10T14:01:00Z"); err != nil || !ok {
		t.Fatalf("force stall: ok=%v err=%v", ok, err)
	}
	if err := o.Retry(ctx, e.ID, "user"); err != nil {
		t.Fatalf("retry must be best-effort about the kill: %v", err)
	}
	got, _ := d.GetEpic(ctx, e.ID)
	if got.Stage != "queued" {
		t.Fatalf("stage = %s, want queued", got.Stage)
	}
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
	if e.Stage != "starting" || e.SessionName != sessionName(16) || e.Attempt != 1 {
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
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	o.Tick(ctx) // sync + spawn → starting
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: sessionName(16), Ts: "t"},
		{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"},
	}
	o.Tick(ctx) // drain → pr_open, then gate → merged
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" || e.PRNumber != 61 || e.Branch != "epic/16-x" {
		t.Fatalf("epic = %+v", e)
	}
	if len(gh.merged) != 1 || gh.merged[0] != 61 {
		t.Fatalf("merged = %v", gh.merged)
	}
	// merged write-back label lands on the ISSUE number
	if len(gh.labels) != 1 || gh.labels[0] != [2]string{"16", "agentmon:merged"} {
		t.Fatalf("labels = %v", gh.labels)
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
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"}}
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
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: sessionName(16), Ts: "t"}}
	o.Tick(ctx)
	o.Tick(ctx) // grace tick passed; session still gone → stalled
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "stalled" {
		t.Fatalf("epic = %+v", e)
	}
}

func TestAttemptsExhaustedReachesFailed(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	ctx := context.Background()
	o.Tick(ctx) // attempt 1 spawns
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	// simulate two stall+retry cycles exhausting MaxAttempts=2
	for i := 0; i < 2; i++ {
		e, _ = d.GetEpicByIssue(ctx, "p1", 16)
		if e.Stage == "starting" {
			o.Tick(ctx)
			o.Tick(ctx) // dead session → stalled
		}
		e, _ = d.GetEpicByIssue(ctx, "p1", 16)
		if e.Stage != "stalled" {
			t.Fatalf("cycle %d: expected stalled, got %+v", i, e)
		}
		if err := o.Retry(ctx, e.ID, "user:admin"); err != nil {
			t.Fatal(err)
		}
		o.Tick(ctx) // respawn or terminalize
	}
	e, _ = d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "failed" {
		t.Fatalf("attempts exhausted must reach failed (not wedge in queued), got %+v", e)
	}
}

func TestHumanMergeOfEscalatedEpicObservedAtRuntime(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 3, resolved: 1, unresolved: 2}\ntests: {passed: 5, failed: 0}\n" +
		"uncertain: false\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"}}
	o.Tick(ctx) // gate escalates on unresolved findings
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "escalated" {
		t.Fatalf("epic = %+v", e)
	}
	// human merges the PR directly in GitHub — NO Approve click
	pr := gh.prs[61]
	pr.Merged = true
	gh.prs[61] = pr
	o.Tick(ctx) // runtime observation must close the epic (not just boot reconcile)
	e, _ = d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" {
		t.Fatalf("human-merged escalated epic must reach merged at runtime, got %+v", e)
	}
}

func TestPROpenReportWithoutPRRejected(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 0, Session: sessionName(16), Ts: "t"}}
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage == "pr_open" {
		t.Fatalf("pr_open without a PR number must be rejected (would wedge), got %+v", e)
	}
}

func TestEmptySessionReportRejectedOnceAssigned(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	o.Tick(ctx) // spawn assigns the session name
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: "", Ts: "t"}}
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "starting" {
		t.Fatalf("empty-session report must not bypass provenance, got %+v", e)
	}
}

func TestReportsRoutedByRepoAcrossProjects(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true, "epic-other-16": true}})
	ctx := context.Background()
	// second project on the SAME host with the SAME issue number
	if err := d.CreateProject(ctx, db.Project{ID: "p2", Name: "other", Repo: "o/other",
		ServerID: "h1", Workdir: "/w2", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	e2, err := d.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p2", IssueNumber: 16, Title: "other-epic",
		Labels: []string{"agentmon:epic"}, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.TransitionEpic(ctx, e2.ID, "queued", "starting", "hub", "", "t1"); !ok {
		t.Fatal("seed transition")
	}
	if ok, _ := d.SetEpicAssignment(ctx, e2.ID, "epic-other-16", 1); !ok {
		t.Fatal("seed assignment")
	}
	o.Tick(ctx) // spawns p1's epic 16
	// a report for p2's epic arrives in the buffer p1 drains first
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/other", Epic: 16, Stage: shared.EpicImplementing, Session: "epic-other-16", Ts: "t"},
	}
	o.Tick(ctx)
	got, _ := d.GetEpicByIssue(ctx, "p2", 16)
	if got.Stage != "implementing" {
		t.Fatalf("cross-project report must route by repo, got %+v", got)
	}
	p1e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if p1e.Stage == "implementing" {
		t.Fatalf("report must NOT apply to the draining project's epic: %+v", p1e)
	}
}

func TestMergingRetryPinsApprovedSHA(t *testing.T) {
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", HeadSHA: "NEWSHA", HeadRef: "epic/16-x"}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	ctx := context.Background()
	// Seed an epic parked in merging with a previously-approved SHA — the
	// state after a gate/human decision followed by a transient merge failure.
	e, err := d.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 16,
		Labels: []string{"agentmon:epic"}, IssueState: "open", QueuedAt: "t0", StageUpdatedAt: "t0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range [][2]string{{"queued", "starting"}, {"starting", "pr_open"}, {"pr_open", "merging"}} {
		if ok, _ := d.TransitionEpic(ctx, e.ID, tr[0], tr[1], "hub", "", "t1"); !ok {
			t.Fatalf("seed transition %v", tr)
		}
	}
	if ok, _ := d.SetEpicPR(ctx, e.ID, 61, "epic/16-x"); !ok {
		t.Fatal("seed pr")
	}
	if ok, _ := d.SetEpicApprovedSHA(ctx, e.ID, "APPROVEDSHA"); !ok {
		t.Fatal("seed approved sha")
	}
	o.Tick(ctx) // merging retry: must pin to the APPROVED sha, not the fresh head
	if len(gh.mergedSHAs) != 1 || gh.mergedSHAs[0] != "APPROVEDSHA" {
		t.Fatalf("merge retry must use the approved SHA, got %v", gh.mergedSHAs)
	}
	got, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if got.Stage != "merged" {
		t.Fatalf("epic = %+v", got)
	}
}

func TestRequireCIHoldsMergeUntilChecksRegister(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" + "findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" + "uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}, prs: map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s"}}, checks: map[string][]github.CheckRun{}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{sessionName(16): true}})
	ctx := context.Background()
	if ok, err := d.SetProjectRequireCI(ctx, "p1", true); err != nil || !ok {
		t.Fatalf("SetProjectRequireCI: ok=%v err=%v", ok, err)
	}
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"}}
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "pr_open" || len(gh.merged) != 0 {
		t.Fatalf("must hold in pr_open with no merge, got stage=%s merged=%v", e.Stage, gh.merged)
	}
	gh.checks["s"] = []github.CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}}
	o.Tick(ctx)
	e, _ = d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" {
		t.Fatalf("must merge once checks exist and pass, got %+v", e)
	}
}
