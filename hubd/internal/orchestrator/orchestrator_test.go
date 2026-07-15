package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
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
	created         []shared.CreateSessionRequest
	reports         []shared.OrchestratorReport
	drainAcks       [][2]any
	killed          []string
	killErr         error
	killUsage       []shared.Usage
	killCapturedAt  string
	spawnErr        error
	sessions        []string // live tmux session names the agent would list
	sessionsErr     error
	sessionsTargets []string                         // target arg of every Sessions call, in order
	tornDown        []shared.WorktreeTeardownRequest // worktree teardowns requested, in order
	teardownErr     error
}

func (f *fakeAgents) Sessions(_ context.Context, _ db.Server, target string) ([]shared.Session, error) {
	f.sessionsTargets = append(f.sessionsTargets, target)
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	out := make([]shared.Session, 0, len(f.sessions))
	for _, n := range f.sessions {
		out = append(out, shared.Session{Name: n})
	}
	return out, nil
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

func (f *fakeAgents) KillSession(_ context.Context, _ db.Server, _, name string) ([]shared.Usage, string, error) {
	f.killed = append(f.killed, name)
	return f.killUsage, f.killCapturedAt, f.killErr
}

func (f *fakeAgents) TeardownWorktree(_ context.Context, _ db.Server, _, workdir, branch string) error {
	f.tornDown = append(f.tornDown, shared.WorktreeTeardownRequest{Workdir: workdir, Branch: branch})
	return f.teardownErr
}

type fakeReg struct{}

func (fakeReg) Get(_ context.Context, id string) (db.Server, bool, error) {
	return db.Server{ID: id, URL: "http://a", Bearer: "b", Status: "active"}, true, nil
}

// sessionName is the expected spawned name for the default test project.
func sessionName(issue int) string { return SessionNameFor("proj", issue, 1) }

func newTestOrch(t *testing.T, gh *fakeGH, ag *fakeAgents) (*Orchestrator, *db.DB) {
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
	o := New(Deps{DB: d, GH: gh, Agents: ag, Reg: fakeReg{},
		Bcast: NewBoardBroadcaster(),
		Cfg:   config.OrchestratorCfg{MaxAttempts: 2},
		Now:   func() string { return clock }})
	return o, d
}

func TestDrainAcksPreviousBatchOnNextPoll(t *testing.T) {
	ag := &fakeAgents{reports: []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 999, Stage: shared.EpicPlanning, Session: "s", Ts: "t"}}}
	o, d := newTestOrch(t, &fakeGH{}, ag)
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
	o, d := newTestOrch(t, gh, ag)
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

func TestFailedKillRetriedUntilRetired(t *testing.T) {
	ag := &fakeAgents{killErr: errors.New("agent unreachable")}
	o, _, e := spawnEpic16(t, ag)
	ctx := context.Background()
	if err := o.Cancel(ctx, e.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if len(ag.killed) != 1 {
		t.Fatalf("killed = %v", ag.killed)
	}
	// Agent recovers: the next tick must retry the failed kill and retire the
	// zombie (it shares the epic's attempt-agnostic branch/worktree).
	ag.killErr = nil
	o.Tick(ctx)
	if len(ag.killed) != 2 || ag.killed[1] != e.SessionName {
		t.Fatalf("failed kill was not retried: %v", ag.killed)
	}
	// Retired sessions are forgotten, not re-killed forever.
	o.Tick(ctx)
	if len(ag.killed) != 2 {
		t.Fatalf("retired session was re-killed: %v", ag.killed)
	}
}

func TestRetryPendingEnforcesCrossHostBoundary(t *testing.T) {
	ag := &fakeAgents{}
	o, d, e := spawnEpic16(t, ag)
	ctx := context.Background()
	rep := shared.OrchestratorReport{Repo: "o/r", Epic: 16, Stage: shared.EpicPlanning, Session: e.SessionName, Ts: "t"}
	// A report deferred from a drain of ANOTHER server must not drive p1's epic.
	o.pending = append(o.pending, pendingReport{ServerID: "evil-host", Target: "default", R: rep})
	o.retryPending(ctx)
	got, _ := d.GetEpic(ctx, e.ID)
	if got.Stage != "starting" {
		t.Fatalf("cross-host pending report must be dropped, stage = %s", got.Stage)
	}
	// The same report deferred from the epic's own server applies normally.
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	o.pending = append(o.pending, pendingReport{ServerID: p.ServerID, Target: p.Target, R: rep})
	o.retryPending(ctx)
	got, _ = d.GetEpic(ctx, e.ID)
	if got.Stage != "planning" {
		t.Fatalf("same-origin pending report must apply, stage = %s", got.Stage)
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
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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

func mergeVerdict16() string {
	return "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
}

func newMergeableGH() *fakeGH {
	return &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: mergeVerdict16(), HeadSHA: "s", HeadRef: "epic/16-x"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
}

// mergeTo16 spawns epic 16, reports pr_open, and ticks so the gate merges it and
// finishMerged runs its inline best-effort reap — shared setup for the reap tests.
func mergeTo16(t *testing.T, ag *fakeAgents) *db.DB {
	t.Helper()
	o, d := newTestOrch(t, newMergeableGH(), ag)
	ctx := context.Background()
	o.Tick(ctx) // spawn → starting (sets SessionName)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"}}
	o.Tick(ctx) // drain → pr_open → gate merges → inline reap
	return d
}

func TestFinishMergedReapsSessionAndWorktree(t *testing.T) {
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	d := mergeTo16(t, ag)
	e, _ := d.GetEpicByIssue(context.Background(), "p1", 16)
	if e.Stage != "merged" {
		t.Fatalf("epic stage = %q", e.Stage)
	}
	if len(ag.killed) != 1 || ag.killed[0] != sessionName(16) {
		t.Fatalf("session not reaped on merge: killed=%v", ag.killed)
	}
	if len(ag.tornDown) != 1 || ag.tornDown[0].Branch != "epic/16-x" || ag.tornDown[0].Workdir == "" {
		t.Fatalf("worktree not torn down on merge: %+v", ag.tornDown)
	}
}

func TestFinishMergedProceedsWhenTeardownFails(t *testing.T) {
	ag := &fakeAgents{sessions: []string{sessionName(16)}, teardownErr: errors.New("boom")}
	d := mergeTo16(t, ag)
	e, _ := d.GetEpicByIssue(context.Background(), "p1", 16)
	if e.Stage != "merged" {
		t.Fatalf("a teardown error must not block the merge; stage = %q", e.Stage)
	}
}

// TestKillEpicSessionStoresTerminalUsage covers the reap snapshot (task 10):
// the agent's kill response can carry the session's final cumulative usage,
// captured immediately before the kill. On a CONFIRMED kill, killEpicSession
// must upsert that usage into the epic's ledger — closing the wasted-cost
// tail of a merge/cancel/retry reap that would otherwise report nothing past
// the last stage report.
func TestKillEpicSessionStoresTerminalUsage(t *testing.T) {
	ag := &fakeAgents{sessions: []string{sessionName(16)},
		killUsage: []shared.Usage{{Provider: "codex", Model: "m", Output: 7}}}
	d := mergeTo16(t, ag)
	ctx := context.Background()
	e, err := d.GetEpicByIssue(ctx, "p1", 16)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListEpicUsage(ctx, e.ProjectID, e.IssueNumber)
	if err != nil {
		t.Fatal(err)
	}
	// killEpicSession runs from finishMerged BEFORE finishMerged's own
	// merged transition lands on the local epic struct it was passed, so the
	// terminal row's stage is the epic's stage as of the kill call ("merging",
	// the gate's pre-merged transition) — not "merged". That is the documented
	// behavior (Stage: shared.EpicStage(e.Stage) uses the struct as received).
	if !hasUsageRow(rows, "codex", 7, string(shared.EpicMerging)) {
		t.Fatalf("terminal usage not stored: %+v", rows)
	}
	// The fake agent set no CapturedAt (mixed-fleet / pre-Fix-6 agent) — the
	// terminal row must fall back to the hub's own clock (newTestOrch's fixed
	// "2026-07-10T14:00:00Z").
	for _, r := range rows {
		if r.Provider == "codex" && r.Output == 7 && r.CapturedAt != "2026-07-10T14:00:00Z" {
			t.Fatalf("captured_at = %q, want hub-clock fallback %q", r.CapturedAt, "2026-07-10T14:00:00Z")
		}
	}
}

// TestKillEpicSessionUsesAgentCapturedAtForReapBoundary covers Fix 6: the
// terminal reap boundary must carry the AGENT's own clock (CapturedAt), not
// the hub's — in a multi-host fleet, clock skew could otherwise sort the
// hub-clock reap BEFORE the last real stage report (stamped on the agent),
// silently dropping the terminal tail the reap exists to capture.
func TestKillEpicSessionUsesAgentCapturedAtForReapBoundary(t *testing.T) {
	const agentClock = "2026-07-10T13:55:00Z" // deliberately BEFORE the hub's fixed Now()
	ag := &fakeAgents{sessions: []string{sessionName(16)},
		killUsage:      []shared.Usage{{Provider: "codex", Model: "m", Output: 7}},
		killCapturedAt: agentClock,
	}
	d := mergeTo16(t, ag)
	ctx := context.Background()
	e, err := d.GetEpicByIssue(ctx, "p1", 16)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListEpicUsage(ctx, e.ProjectID, e.IssueNumber)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Provider == "codex" && r.Output == 7 {
			if r.CapturedAt != agentClock {
				t.Fatalf("captured_at = %q, want agent clock %q (not hub Now)", r.CapturedAt, agentClock)
			}
			return
		}
	}
	t.Fatalf("terminal usage row not found: %+v", rows)
}

func hasUsageRow(rows []db.UsageRow, provider string, output int64, stage string) bool {
	for _, r := range rows {
		if r.Provider == provider && r.Output == output && r.Stage == stage {
			return true
		}
	}
	return false
}

func TestFinishMergedSkipsTeardownWhenKillFails(t *testing.T) {
	// Ordering: a merged epic whose session kill FAILS must NOT have its worktree
	// torn down — never pull a worktree out from under a runner whose kill has not
	// landed. killEpicSession queues the failed kill for per-tick retry.
	ag := &fakeAgents{sessions: []string{sessionName(16)}, killErr: errors.New("kill boom")}
	d := mergeTo16(t, ag)
	if len(ag.tornDown) != 0 {
		t.Fatalf("worktree torn down despite a failed kill: %+v", ag.tornDown)
	}
	e, _ := d.GetEpicByIssue(context.Background(), "p1", 16)
	if e.Stage != "merged" {
		t.Fatalf("stage = %q (want merged)", e.Stage)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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

func TestPlanGateReportPersistsBranchWithoutClobber(t *testing.T) {
	o, d := newTestOrch(t, &fakeGH{}, &fakeAgents{})
	ctx := context.Background()
	e, err := d.UpsertEpicIssue(ctx, db.Epic{
		ProjectID: "p1", IssueNumber: 7, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "t1"); err != nil || !ok {
		t.Fatalf("starting: ok=%v err=%v", ok, err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "planning", "report", "", "t2"); err != nil || !ok {
		t.Fatalf("planning: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if deferred := o.applyReport(ctx, p, shared.OrchestratorReport{
		Repo: "o/r", Epic: 7, Stage: shared.EpicEscalated, Branch: "epic/7-x", Note: "plan-gate: x", Ts: "t3",
	}); deferred {
		t.Fatal("escalated report unexpectedly deferred")
	}
	got, err := d.GetEpic(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "epic/7-x" || got.Stage != "escalated" {
		t.Fatalf("after plan gate: %+v", got)
	}

	// The approved plan resumes implementation before the runner opens a PR.
	if deferred := o.applyReport(ctx, p, shared.OrchestratorReport{
		Repo: "o/r", Epic: 7, Stage: shared.EpicImplementing, Ts: "t4",
	}); deferred {
		t.Fatal("implementing report unexpectedly deferred")
	}
	if deferred := o.applyReport(ctx, p, shared.OrchestratorReport{
		Repo: "o/r", Epic: 7, Stage: shared.EpicPROpen, PR: 61, Ts: "t5",
	}); deferred {
		t.Fatal("pr_open report unexpectedly deferred")
	}
	got, err = d.GetEpic(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PRNumber != 61 || got.Branch != "epic/7-x" {
		t.Fatalf("pr report must preserve plan-gate branch: %+v", got)
	}
}

func TestPlanGateReportIgnoresNonconformingBranch(t *testing.T) {
	o, d := newTestOrch(t, &fakeGH{}, &fakeAgents{})
	ctx := context.Background()
	e, err := d.UpsertEpicIssue(ctx, db.Epic{
		ProjectID: "p1", IssueNumber: 7, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "t1"); err != nil || !ok {
		t.Fatalf("starting: ok=%v err=%v", ok, err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "planning", "report", "", "t2"); err != nil || !ok {
		t.Fatalf("planning: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if deferred := o.applyReport(ctx, p, shared.OrchestratorReport{
		Repo: "o/r", Epic: 7, Stage: shared.EpicEscalated, Branch: "main", Note: "plan-gate: x", Ts: "t3",
	}); deferred {
		t.Fatal("escalated report unexpectedly deferred")
	}
	got, err := d.GetEpic(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "escalated" || got.Branch != "" {
		t.Fatalf("nonconforming branch must be ignored: %+v", got)
	}
}

func TestBranchClaimRequiresEscalatedStage(t *testing.T) {
	o, d := newTestOrch(t, &fakeGH{}, &fakeAgents{})
	ctx := context.Background()
	e, err := d.UpsertEpicIssue(ctx, db.Epic{
		ProjectID: "p1", IssueNumber: 7, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "t1"); err != nil || !ok {
		t.Fatalf("starting: ok=%v err=%v", ok, err)
	}
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "planning", "report", "", "t2"); err != nil || !ok {
		t.Fatalf("planning: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	// A non-plan-gate report (implementing) carrying a well-formed epic branch is
	// a legal transition, but branch persistence is escalation-only — it must NOT
	// claim the branch slot, or a genuine plan-gate branch could never be recorded.
	if deferred := o.applyReport(ctx, p, shared.OrchestratorReport{
		Repo: "o/r", Epic: 7, Stage: shared.EpicImplementing, Branch: "epic/7-x", Ts: "t3",
	}); deferred {
		t.Fatal("implementing report unexpectedly deferred")
	}
	got, err := d.GetEpic(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "implementing" || got.Branch != "" {
		t.Fatalf("non-escalated report must not claim the branch: %+v", got)
	}
}

func TestStallOnDeadSession(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag) // session never alive
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

// Liveness must come from the agent's REAL tmux session list, not the
// hook-fed state projection: a runner whose provider hooks are missing or
// mispointed produces no state events at all, yet is alive and working —
// marking it stalled after two ticks abandons real work (observed live in
// the 2026-07-11 toy-repo acceptance).
func TestStallLivenessComesFromAgentSessionsNotHookState(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()
	o.Tick(ctx)                             // spawn
	ag.sessions = []string{sessionName(16)} // alive in tmux; NO hook state anywhere
	o.Tick(ctx)
	o.Tick(ctx)
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage == "stalled" {
		t.Fatalf("hookless-but-alive session must not stall, got %+v", e)
	}
}

// One liveness fetch per (server,target) per tick, shared across projects —
// N co-hosted projects must not issue N identical GETs, each able to hold
// tickMu for the full client timeout on a black-holed agent. The fetch must
// also pass the project's RAW target (the same value CreateSession gets):
// resolving it hub-side on one path but not the other would mass-false-stall.
func TestStallLivenessFetchSharedAcrossProjectsPerTick(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{sessions: []string{sessionName(16), SessionNameFor("other", 16, 1)}}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()
	// Second project on the SAME server and target ("" = agent default).
	if err := d.CreateProject(ctx, db.Project{ID: "p2", Name: "other", Repo: "o/other",
		ServerID: "h1", Workdir: "/w2", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	o.Tick(ctx) // both projects spawn; no active-stage epics at checkStalls time
	before := len(ag.sessionsTargets)
	o.Tick(ctx) // both epics now starting → liveness consulted
	calls := ag.sessionsTargets[before:]
	if len(calls) != 1 {
		t.Fatalf("one shared (server,target) must mean ONE Sessions fetch per tick, got %d: %v", len(calls), calls)
	}
	if calls[0] != "" {
		t.Fatalf("liveness must query the project's RAW target %q, got %q", "", calls[0])
	}
}

// An unreachable agent means liveness is UNKNOWN, not dead: marking stalls on
// a hub-side dial failure would mass-stall every epic during an agent restart
// or network blip. Fail safe — skip the liveness verdict, keep stage timeouts.
func TestStallSkippedWhenAgentSessionsUnavailable(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{sessionsErr: errors.New("dial tcp: connection refused")}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()
	o.Tick(ctx) // spawn
	o.Tick(ctx)
	o.Tick(ctx)
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage == "stalled" {
		t.Fatalf("unreachable agent must not produce a stall verdict, got %+v", e)
	}
}

func TestAttemptsExhaustedReachesFailed(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16), "epic-other-16"}}
	o, d := newTestOrch(t, gh, ag)
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
	o, d := newTestOrch(t, gh, ag)
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
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
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

func TestTickGateEnforcesPlatformRequirements(t *testing.T) {
	// A verdict that never reports a project platform requirement must fail the
	// gate closed. This exercises the run-loop plumbing (Requirements:
	// p.Requirements) that the direct Decide unit tests cannot see.
	verdictNoReqs := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictNoReqs, HeadSHA: "s", HeadRef: "epic/16-x"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()

	// Attach a platform requirement to the default project before any tick.
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p.Requirements = []db.Requirement{{ID: "always-use-rls", Text: "Always use RLS"}}
	if ok, err := d.UpdateProject(ctx, p); err != nil || !ok {
		t.Fatalf("attach requirement: ok=%v err=%v", ok, err)
	}

	o.Tick(ctx) // sync + spawn → starting
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"},
	}
	o.Tick(ctx) // drain → pr_open → gate

	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "escalated" {
		t.Fatalf("unreported platform requirement must escalate, stage = %q", e.Stage)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("gate must not merge with an unmet requirement: merged = %v", gh.merged)
	}
	// Pin the escalation reason: the fixture is deliberately built so the
	// requirements check is the SOLE escalation source (verdict reviews match
	// RequiredReviews, epic binds, checks green, no uncertainty/unresolved/failed
	// tests), so a plumbing regression can't hide behind an unrelated escalation.
	if !strings.Contains(strings.Join(gh.comments, "\n"), "platform requirements not met: always-use-rls (missing)") {
		t.Fatalf("escalation must name the missing requirement, comments = %v", gh.comments)
	}
}

// TestApplyReportUpsertsUsageEvenOnNoopTransition covers redelivery recovery:
// a report whose stage transition is a no-op (the epic is already at that
// stage) must still upsert the usage it carries — the same-stage case is
// explicitly treated as legal-for-usage-purposes by applyReport's gate (see
// TestApplyReportGatesUsageUpsertOnTransitionLegality for the genuinely
// illegal case, which must NOT upsert). Provenance still gates it — the
// epic is assigned a session and the report must claim that same session.
func TestApplyReportUpsertsUsageEvenOnNoopTransition(t *testing.T) {
	o, d := newTestOrch(t, &fakeGH{}, &fakeAgents{})
	ctx := context.Background()
	e, err := d.UpsertEpicIssue(ctx, db.Epic{
		ProjectID: "p1", IssueNumber: 7, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range [][3]string{
		{"queued", "starting", "hub"},
		{"starting", "planning", "report"},
		{"planning", "implementing", "report"},
		{"implementing", "reviewing", "report"},
	} {
		if ok, err := d.TransitionEpic(ctx, e.ID, tr[0], tr[1], tr[2], "", "t1"); err != nil || !ok {
			t.Fatalf("%s->%s: ok=%v err=%v", tr[0], tr[1], ok, err)
		}
	}
	if ok, err := d.SetEpicAssignment(ctx, e.ID, "P-7", 1); err != nil || !ok {
		t.Fatalf("assign session: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}

	rep := shared.OrchestratorReport{Repo: "o/r", Epic: 7, Stage: shared.EpicReviewing,
		Session: "P-7", Ts: "2026-07-14T10:00:00Z",
		Usage: []shared.Usage{{Provider: "claude", Model: "m", Output: 42}}}
	if deferred := o.applyReport(ctx, p, rep); deferred {
		t.Fatal("noop-transition report unexpectedly deferred")
	}

	got, err := d.GetEpic(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "reviewing" {
		t.Fatalf("stage must remain unchanged (noop transition): %q", got.Stage)
	}

	rows, err := d.ListEpicUsage(ctx, p.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Output != 42 {
		t.Fatalf("usage not upserted on noop transition: %+v", rows)
	}
	if rows[0].Attempt != 1 || rows[0].Stage != "reviewing" {
		t.Fatalf("usage row must carry epic attempt + report stage: %+v", rows[0])
	}
}

// TestApplyReportGatesUsageUpsertOnTransitionLegality guards Fix D1 and its
// Fix #4 refinement: usage upsert must be gated on the report being fully
// ACCEPTED — a same-stage redelivery (see
// TestApplyReportUpsertsUsageEvenOnNoopTransition), or a real transition
// that is legal AND actually landed — never for a report dropped by ANY
// guard, whether that's a genuinely ILLEGAL transition or a legal-looking
// pr_open claim with no PR number. Both are dropped outright a few lines
// later anyway; upserting their usage first (the pre-Fix-#4 ordering) would
// plant a phantom stage boundary that skews attribution for work never
// really attributed to that stage. A subsequent LEGAL transition that
// actually lands must still upsert normally.
func TestApplyReportGatesUsageUpsertOnTransitionLegality(t *testing.T) {
	o, d := newTestOrch(t, &fakeGH{}, &fakeAgents{})
	ctx := context.Background()
	e, err := d.UpsertEpicIssue(ctx, db.Epic{
		ProjectID: "p1", IssueNumber: 7, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range [][3]string{
		{"queued", "starting", "hub"},
		{"starting", "planning", "report"},
		{"planning", "implementing", "report"},
		{"implementing", "reviewing", "report"},
	} {
		if ok, err := d.TransitionEpic(ctx, e.ID, tr[0], tr[1], tr[2], "", "t1"); err != nil || !ok {
			t.Fatalf("%s->%s: ok=%v err=%v", tr[0], tr[1], ok, err)
		}
	}
	if ok, err := d.SetEpicAssignment(ctx, e.ID, "P-7", 1); err != nil || !ok {
		t.Fatalf("assign session: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}

	// (a) pr_open report with NO PR number and no prior PR on the epic: this
	// is the exact Fix #4 hazard — a legal reviewing->pr_open transition per
	// ValidTransition, but dropped a few lines later by the missing-PR
	// guard. Before Fix #4 this still upserted usage (the D1 gate ran
	// before the PR-number check); it must NOT anymore.
	prOpenNoPR := shared.OrchestratorReport{Repo: "o/r", Epic: 7, Stage: shared.EpicPROpen,
		Session: "P-7", Ts: "2026-07-14T09:55:00Z",
		Usage: []shared.Usage{{Provider: "claude", Model: "m", Output: 7}}}
	if deferred := o.applyReport(ctx, p, prOpenNoPR); deferred {
		t.Fatal("pr_open-without-PR report unexpectedly deferred")
	}
	if got, err := d.GetEpic(ctx, e.ID); err != nil {
		t.Fatal(err)
	} else if got.Stage != "reviewing" {
		t.Fatalf("pr_open without a PR number must not move the stage, got %q", got.Stage)
	}
	if rows, err := d.ListEpicUsage(ctx, p.ID, 7); err != nil {
		t.Fatal(err)
	} else if len(rows) != 0 {
		t.Fatalf("pr_open report dropped for a missing PR number must NOT upsert usage, got %+v", rows)
	}

	// (b) ILLEGAL transition: epic is at "reviewing"; reviewing->planning is
	// a backward jump (not the special reviewing->implementing fix loop), so
	// ValidTransition rejects it. Its usage must NOT land.
	illegal := shared.OrchestratorReport{Repo: "o/r", Epic: 7, Stage: shared.EpicPlanning,
		Session: "P-7", Ts: "2026-07-14T10:00:00Z",
		Usage: []shared.Usage{{Provider: "claude", Model: "m", Output: 999}}}
	if deferred := o.applyReport(ctx, p, illegal); deferred {
		t.Fatal("illegal-transition report unexpectedly deferred")
	}
	if got, err := d.GetEpic(ctx, e.ID); err != nil {
		t.Fatal(err)
	} else if got.Stage != "reviewing" {
		t.Fatalf("illegal transition must not move the stage, got %q", got.Stage)
	}
	rows, err := d.ListEpicUsage(ctx, p.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("illegal transition must NOT upsert usage, got %+v", rows)
	}

	// (c) LEGAL transition: reviewing -> pr_open is a forward move. Its usage
	// must land.
	legal := shared.OrchestratorReport{Repo: "o/r", Epic: 7, Stage: shared.EpicPROpen, PR: 42,
		Session: "P-7", Ts: "2026-07-14T10:05:00Z",
		Usage: []shared.Usage{{Provider: "claude", Model: "m", Output: 111}}}
	if deferred := o.applyReport(ctx, p, legal); deferred {
		t.Fatal("legal-transition report unexpectedly deferred")
	}
	if got, err := d.GetEpic(ctx, e.ID); err != nil {
		t.Fatal(err)
	} else if got.Stage != "pr_open" {
		t.Fatalf("legal transition must actually transition the epic, got %q", got.Stage)
	}
	rows, err = d.ListEpicUsage(ctx, p.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Output != 111 || rows[0].Stage != "pr_open" {
		t.Fatalf("legal transition must upsert its usage, got %+v", rows)
	}
}
