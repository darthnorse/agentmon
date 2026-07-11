package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type GitHubAPI interface {
	GetIssue(ctx context.Context, repo string, num int) (github.Issue, error)
	// ListIssuesLabeledSince keeps boot syncs bounded: only labeled issues
	// enter the mirror, so only labeled issues are listed.
	ListIssuesLabeledSince(ctx context.Context, repo, label, since string) ([]github.Issue, error)
	GetPullRequest(ctx context.Context, repo string, num int) (github.PullRequest, error)
	ListCheckRuns(ctx context.Context, repo, ref string) ([]github.CheckRun, error)
	MergePR(ctx context.Context, repo string, num int, sha string) error
	CreateIssueComment(ctx context.Context, repo string, num int, body string) error
	AddLabels(ctx context.Context, repo string, num int, labels []string) error
}

type AgentAPI interface {
	CreateSession(ctx context.Context, srv db.Server, target string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error)
	DrainReports(ctx context.Context, srv db.Server, target string) ([]shared.OrchestratorReport, error)
}

type ServerGetter interface {
	Get(ctx context.Context, id string) (db.Server, bool, error)
}

// LivenessAPI lists a server's live session views. Lookup is by SESSION NAME
// across targets: the projection keys on the agent-resolved target label
// (e.g. "default"), which never equals a project's raw Target config ("" =
// agent default) — an exact (server, target, session) lookup would miss
// every real deployment and mass-false-stall.
type LivenessAPI interface {
	Server(server string) []state.SessionView
}

type Deps struct {
	DB     *db.DB
	GH     GitHubAPI
	Agents AgentAPI
	Reg    ServerGetter
	Live   LivenessAPI
	Bcast  *BoardBroadcaster
	Cfg    config.OrchestratorCfg
	Now    func() string
}

const maxPendingReports = 256

type Orchestrator struct {
	d    Deps
	wake chan struct{}

	// tickMu serializes Tick: production always calls it from the Run
	// goroutine (external triggers use Wake), the mutex makes direct calls
	// safe for tests and future callers.
	tickMu     sync.Mutex
	watermarks map[string]string // projectID → last successful sync watermark
	syncSkip   map[string]int    // projectID → ticks left to skip after sync failures
	syncFails  map[string]int    // projectID → consecutive sync failures
	stallSeen  map[string]int    // epicID → consecutive ticks with dead session
	// pending holds reports the hub drained but could not apply due to a
	// transient error. Drains are destructive on the agent, so once drained
	// the hub owns them; retried next tick, capped to bound memory.
	pending []shared.OrchestratorReport
}

func New(d Deps) *Orchestrator {
	if d.Now == nil {
		d.Now = func() string { return time.Now().UTC().Format(time.RFC3339) }
	}
	return &Orchestrator{d: d, wake: make(chan struct{}, 1),
		watermarks: map[string]string{}, syncSkip: map[string]int{},
		syncFails: map[string]int{}, stallSeen: map[string]int{}}
}

func (o *Orchestrator) Wake() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *Orchestrator) Run(ctx context.Context) {
	if err := o.reconcile(ctx); err != nil {
		log.Printf("orchestrator: reconcile: %v", err)
	}
	tick := o.d.Cfg.Tick
	if tick <= 0 {
		tick = 15 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-o.wake:
		}
		o.Tick(ctx)
	}
}

// Tick runs one pass of the pipeline over every project. Serialized by
// tickMu; external triggers should use Wake() so work lands on Run's
// goroutine rather than an HTTP handler's.
func (o *Orchestrator) Tick(ctx context.Context) {
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	projects, err := o.d.DB.ListProjects(ctx)
	if err != nil {
		log.Printf("orchestrator: list projects: %v", err)
		return
	}
	o.retryPending(ctx)
	for _, p := range projects {
		o.tickProject(ctx, p)
	}
}

func (o *Orchestrator) tickProject(ctx context.Context, p db.Project) {
	now := o.d.Now()
	if o.syncSkip[p.ID] > 0 {
		o.syncSkip[p.ID]--
	} else if err := o.syncProject(ctx, p, now); err != nil {
		o.syncFails[p.ID]++
		// Exponential-ish backoff in ticks, capped: a permanently failing
		// sync (huge repo, rate limit) must not burn the PAT every tick.
		skip := o.syncFails[p.ID] * 2
		if skip > 40 {
			skip = 40
		}
		o.syncSkip[p.ID] = skip
		log.Printf("orchestrator[%s]: sync: %v (backing off %d ticks)", p.Name, err, skip)
	} else {
		o.syncFails[p.ID] = 0
	}
	o.cancelClosedQueued(ctx, p)
	o.drainReports(ctx, p)
	o.checkStalls(ctx, p, now)
	o.evaluateGates(ctx, p)
	o.schedule(ctx, p)
}

// server resolves the project's agent, logging once per phase on failure.
func (o *Orchestrator) server(ctx context.Context, p db.Project, phase string) (db.Server, bool) {
	srv, ok, err := o.d.Reg.Get(ctx, p.ServerID)
	if err != nil {
		log.Printf("orchestrator[%s]: %s server: %v", p.Name, phase, err)
		return db.Server{}, false
	}
	return srv, ok
}

var orchestratedLabels = []string{"agentmon:epic", "agentmon:run"}

func (o *Orchestrator) syncProject(ctx context.Context, p db.Project, now string) error {
	since := o.watermarks[p.ID]
	seen := map[int]bool{}
	// Watermark advances from GitHub-attributed time (issue updated_at), not
	// the hub clock — clock skew against GitHub would otherwise open a
	// permanent blind window. Fallback: pre-request hub time.
	next := now
	for _, label := range orchestratedLabels {
		issues, err := o.d.GH.ListIssuesLabeledSince(ctx, p.Repo, label, since)
		if err != nil {
			return err
		}
		for _, is := range issues {
			if seen[is.Number] || !IsOrchestratedIssue(is.Labels) {
				continue
			}
			seen[is.Number] = true
			if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, is, now)); err != nil {
				return err
			}
			if is.UpdatedAt > next {
				next = is.UpdatedAt
			}
		}
	}
	o.watermarks[p.ID] = next
	return nil
}

// cancelClosedQueued sweeps queued epics whose GitHub issue was closed —
// closing the issue is the natural "don't do this"; without the sweep the
// epic lingers queued forever (and would spawn if the issue is ever reopened).
func (o *Orchestrator) cancelClosedQueued(ctx context.Context, p db.Project) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		return
	}
	for _, e := range epics {
		if e.Stage == string(shared.EpicQueued) && e.IssueState == "closed" {
			o.transition(ctx, e, shared.EpicCanceled, "github", "issue closed")
		}
	}
}

func (o *Orchestrator) drainReports(ctx context.Context, p db.Project) {
	srv, ok := o.server(ctx, p, "drain")
	if !ok {
		return
	}
	reports, err := o.d.Agents.DrainReports(ctx, srv, p.Target)
	if err != nil {
		log.Printf("orchestrator[%s]: reports: %v", p.Name, err)
		return
	}
	for _, r := range reports {
		o.routeReport(ctx, p, r)
	}
}

// routeReport applies one drained report to the project it names. The drain
// is per (server, target) and destructive, so reports for OTHER projects on
// the same host arrive here too — route by Repo, never assume the drainer.
func (o *Orchestrator) routeReport(ctx context.Context, drained db.Project, r shared.OrchestratorReport) {
	p := drained
	if r.Repo != "" && !strings.EqualFold(r.Repo, p.Repo) {
		other, err := o.d.DB.GetProjectByRepo(ctx, r.Repo)
		if err != nil {
			log.Printf("orchestrator[%s]: dropped report for unknown repo %q: %+v", drained.Name, r.Repo, r)
			return
		}
		p = other
	}
	o.applyReport(ctx, p, r)
}

func (o *Orchestrator) applyReport(ctx context.Context, p db.Project, r shared.OrchestratorReport) {
	e, err := o.d.DB.GetEpicByIssue(ctx, p.ID, r.Epic)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("orchestrator[%s]: dropped report for unknown epic #%d", p.Name, r.Epic)
			return
		}
		// Transient DB error: the report is already gone from the agent, so
		// the hub must own it — stash for retry instead of dropping.
		if len(o.pending) < maxPendingReports {
			o.pending = append(o.pending, r)
		}
		log.Printf("orchestrator[%s]: report deferred (db error): %v", p.Name, err)
		return
	}
	if !shared.ReportableStage(r.Stage) {
		log.Printf("orchestrator[%s]: dropped non-reportable stage %q for epic #%d", p.Name, r.Stage, r.Epic)
		return
	}
	// Provenance: once an epic has an assigned session, only that session's
	// reports count — an EMPTY session claim does not bypass the check.
	if e.SessionName != "" && r.Session != e.SessionName {
		log.Printf("orchestrator[%s]: report session mismatch: %q != %q", p.Name, r.Session, e.SessionName)
		return
	}
	// A pr_open claim with no PR number would strand the epic in a stage no
	// scanner revisits. Fail closed; the runner stays in reviewing where the
	// stage timeout still applies.
	if r.Stage == shared.EpicPROpen && r.PR <= 0 && e.PRNumber <= 0 {
		log.Printf("orchestrator[%s]: dropped pr_open report without PR number for epic #%d", p.Name, r.Epic)
		return
	}
	if !ValidTransition(shared.EpicStage(e.Stage), r.Stage) {
		log.Printf("orchestrator[%s]: dropped invalid report transition %s→%s", p.Name, e.Stage, r.Stage)
		return
	}
	if r.PR > 0 {
		_, _ = o.d.DB.SetEpicPR(ctx, e.ID, r.PR, e.Branch)
	}
	o.transition(ctx, e, r.Stage, "report", r.Note)
}

func (o *Orchestrator) retryPending(ctx context.Context) {
	if len(o.pending) == 0 {
		return
	}
	batch := o.pending
	o.pending = nil
	for _, r := range batch {
		if r.Repo == "" {
			continue // cannot route without a repo
		}
		p, err := o.d.DB.GetProjectByRepo(ctx, r.Repo)
		if err != nil {
			continue
		}
		o.applyReport(ctx, p, r)
	}
}

func (o *Orchestrator) checkStalls(ctx context.Context, p db.Project, now string) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: stalls: %v", p.Name, err)
		return
	}
	nowTime, _ := time.Parse(time.RFC3339, now)
	var views []state.SessionView
	viewsLoaded := false
	for _, e := range epics {
		stage := shared.EpicStage(e.Stage)
		if stage != shared.EpicStarting && stage != shared.EpicPlanning &&
			stage != shared.EpicImplementing && stage != shared.EpicReviewing {
			delete(o.stallSeen, e.ID)
			continue
		}
		if !viewsLoaded {
			views = o.d.Live.Server(p.ServerID)
			viewsLoaded = true
		}
		alive := false
		for _, v := range views {
			if v.Session == e.SessionName {
				alive = true
				break
			}
		}
		reason := ""
		if !alive {
			o.stallSeen[e.ID]++
			// Two consecutive dead ticks AND a real-time grace: wake-driven
			// ticks can run back-to-back, faster than the state poller can
			// list a freshly spawned session (and the projection is empty at
			// boot). Grace = 2 tick periods from the last stage change.
			if o.stallSeen[e.ID] >= 2 && o.graceElapsed(e.StageUpdatedAt, nowTime) {
				reason = "runner session disappeared"
			}
		} else {
			delete(o.stallSeen, e.ID)
		}
		var timeout time.Duration
		switch stage {
		case shared.EpicStarting, shared.EpicPlanning:
			timeout = o.d.Cfg.PlanningTimeout
		case shared.EpicImplementing:
			timeout = o.d.Cfg.ImplementingTimeout
		case shared.EpicReviewing:
			timeout = o.d.Cfg.ReviewingTimeout
		}
		if timeout > 0 {
			if updated, err := time.Parse(time.RFC3339, e.StageUpdatedAt); err == nil &&
				!nowTime.IsZero() && nowTime.Sub(updated) > timeout {
				reason = fmt.Sprintf("%s timeout", stage)
			}
		}
		if reason != "" {
			o.transition(ctx, e, shared.EpicStalled, "hub", reason)
			delete(o.stallSeen, e.ID)
		}
	}
}

// graceElapsed reports whether the liveness-stall grace window has passed
// since the epic's last stage change. Zero/unset Tick (tests) means no
// real-time grace — the consecutive-tick counter alone governs.
func (o *Orchestrator) graceElapsed(stageUpdatedAt string, now time.Time) bool {
	if o.d.Cfg.Tick <= 0 || now.IsZero() {
		return true
	}
	updated, err := time.Parse(time.RFC3339, stageUpdatedAt)
	if err != nil {
		return true
	}
	return now.Sub(updated) >= 2*o.d.Cfg.Tick
}

func (o *Orchestrator) evaluateGates(ctx context.Context, p db.Project) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: gates: %v", p.Name, err)
		return
	}
	for _, e := range epics {
		stage := shared.EpicStage(e.Stage)
		observeOnly := stage == shared.EpicEscalated || stage == shared.EpicStalled
		if (stage != shared.EpicPROpen && stage != shared.EpicMerging && !observeOnly) || e.PRNumber <= 0 {
			continue
		}
		pr, err := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
		if err != nil {
			log.Printf("orchestrator[%s]: PR #%d: %v", p.Name, e.PRNumber, err)
			continue
		}
		if pr.HeadRef != "" && e.Branch != pr.HeadRef {
			_, _ = o.d.DB.SetEpicPR(ctx, e.ID, pr.Number, pr.HeadRef)
			e.Branch = pr.HeadRef
		}
		// Merged/closed observation applies to ALL scanned stages — this is
		// how a human merging an escalated/stalled epic's PR in GitHub is
		// noticed at runtime, not just at boot reconcile (spec §6).
		if pr.Merged {
			o.finishMerged(ctx, p, e, "github", "")
			continue
		}
		if pr.State == "closed" {
			o.transition(ctx, e, shared.EpicCanceled, "github", "PR closed without merge")
			continue
		}
		if observeOnly {
			continue
		}
		if stage == shared.EpicMerging {
			// The merge decision was already made (by gate or human Approve);
			// never re-run Decide here — a re-gate against the stale verdict
			// would silently revert an approval. Just retry the merge.
			if err := o.mergeEpic(ctx, p, e, "hub", pr.HeadSHA); err != nil {
				log.Printf("orchestrator[%s]: merge retry PR #%d: %v", p.Name, e.PRNumber, err)
			}
			continue
		}
		v, verr := ParseVerdict(pr.Body)
		if v != nil {
			if b, err := json.Marshal(v); err == nil {
				_, _ = o.d.DB.SetEpicVerdict(ctx, e.ID, string(b))
			}
		}
		runs, err := o.d.GH.ListCheckRuns(ctx, p.Repo, pr.HeadSHA)
		if err != nil {
			log.Printf("orchestrator[%s]: checks for PR #%d: %v", p.Name, e.PRNumber, err)
			continue
		}
		green, pending := github.ChecksState(runs)
		res := Decide(GateInput{Verdict: v, VerdictErr: verr, Epic: e.IssueNumber, Labels: e.Labels,
			RequiredReviews: p.RequiredReviews, ChecksGreen: green, ChecksPending: pending})
		switch {
		case res.Wait:
		case res.Merge:
			if err := o.mergeEpic(ctx, p, e, "hub", pr.HeadSHA); err != nil {
				log.Printf("orchestrator[%s]: merge PR #%d: %v", p.Name, e.PRNumber, err)
			}
		default:
			if o.transition(ctx, e, shared.EpicEscalated, "hub", res.Reason) {
				o.comment(ctx, p, e.IssueNumber, "⚠ escalated: "+res.Reason)
			}
		}
	}
}

// mergeEpic drives current→merging→merged. Returns an error when the merge
// did not complete so callers (Approve!) can surface it; a nil return means
// the PR is merged (possibly by someone else — 405 is re-checked, not
// mislabeled as a conflict).
func (o *Orchestrator) mergeEpic(ctx context.Context, p db.Project, e db.Epic, source, sha string) error {
	if shared.EpicStage(e.Stage) != shared.EpicMerging {
		if !o.transition(ctx, e, shared.EpicMerging, source, "") {
			return fmt.Errorf("epic #%d: stage moved concurrently", e.IssueNumber)
		}
		e.Stage = string(shared.EpicMerging)
	}
	if err := o.d.GH.MergePR(ctx, p.Repo, e.PRNumber, sha); err != nil {
		if errors.Is(err, github.ErrNotMergeable) {
			// 405/409 covers "already merged" and "head moved" — re-fetch
			// before declaring a conflict.
			if pr, err2 := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber); err2 == nil && pr.Merged {
				o.finishMerged(ctx, p, e, source, "")
				return nil
			}
			reason := "merge rejected by GitHub (branch moved or conflict) — needs a human look"
			o.transition(ctx, e, shared.EpicEscalated, "hub", reason)
			return fmt.Errorf("epic #%d: %s", e.IssueNumber, reason)
		}
		// Transient failure: stay in merging; the next tick retries the merge
		// without re-running the gate, so an Approve survives a network blip.
		return err
	}
	o.finishMerged(ctx, p, e, source, "")
	return nil
}

func (o *Orchestrator) finishMerged(ctx context.Context, p db.Project, e db.Epic, source, note string) {
	if !o.transition(ctx, e, shared.EpicMerged, source, note) {
		return
	}
	if err := o.d.GH.AddLabels(ctx, p.Repo, e.IssueNumber, []string{"agentmon:merged"}); err != nil {
		log.Printf("orchestrator[%s]: label merged: %v", p.Name, err)
	}
	o.comment(ctx, p, e.IssueNumber, fmt.Sprintf("✅ merged PR #%d", e.PRNumber))
}

func (o *Orchestrator) comment(ctx context.Context, p db.Project, issue int, body string) {
	if err := o.d.GH.CreateIssueComment(ctx, p.Repo, issue, body); err != nil {
		log.Printf("orchestrator[%s]: comment issue #%d: %v", p.Name, issue, err)
	}
}

func (o *Orchestrator) schedule(ctx context.Context, p db.Project) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: schedule: %v", p.Name, err)
		return
	}
	ready := ReadyEpics(epics, p.MaxParallel, p.Paused)
	if len(ready) == 0 {
		return
	}
	srv, ok := o.server(ctx, p, "schedule")
	if !ok {
		return
	}
	for _, e := range ready {
		attempt := e.Attempt + 1
		if attempt > o.d.Cfg.MaxAttempts {
			o.transition(ctx, e, shared.EpicFailed, "hub", "attempts exhausted")
			continue
		}
		// Session names carry the project slug (cross-project uniqueness on
		// shared hosts) and the attempt (a retry must not collide with a
		// still-alive previous session — tmux 409s duplicates).
		name := SessionNameFor(p.Name, e.IssueNumber, attempt)
		if !o.transition(ctx, e, shared.EpicStarting, "hub", "spawning "+name) {
			continue
		}
		e.Stage = string(shared.EpicStarting)
		if ok, err := o.d.DB.SetEpicAssignment(ctx, e.ID, name, attempt); err != nil || !ok {
			// Without a recorded assignment, liveness and provenance checks
			// have nothing to match — do not spawn an untracked session.
			o.transition(ctx, e, shared.EpicStalled, "hub", "assignment persist failed")
			continue
		}
		_, err = o.d.Agents.CreateSession(ctx, srv, p.Target, shared.CreateSessionRequest{
			Name: name, Cwd: p.Workdir,
			Command: KickoffCommand(ProviderFor(p.Provider, e.Labels), e.IssueNumber),
		})
		if err != nil {
			o.transition(ctx, e, shared.EpicStalled, "hub", "spawn failed: "+err.Error())
		}
	}
}

// transition validates, persists, and publishes one stage move. The note is
// also the needs-attention text when entering escalated/stalled (TransitionEpic
// applies it atomically under the same stage guard). Returns whether the move
// happened (stale `from` loses silently — that is the race guard).
func (o *Orchestrator) transition(ctx context.Context, e db.Epic, to shared.EpicStage, source, note string) bool {
	from := shared.EpicStage(e.Stage)
	if !ValidTransition(from, to) {
		log.Printf("orchestrator: invalid transition %s→%s for epic #%d", from, to, e.IssueNumber)
		return false
	}
	ok, err := o.d.DB.TransitionEpic(ctx, e.ID, string(from), string(to), source, note, o.d.Now())
	if err != nil || !ok {
		if err != nil {
			log.Printf("orchestrator: transition: %v", err)
		}
		return false
	}
	if o.d.Bcast != nil {
		needs := ""
		if to == shared.EpicEscalated || to == shared.EpicStalled {
			needs = note
		}
		o.d.Bcast.Publish(BoardChange{ProjectID: e.ProjectID, EpicID: e.ID,
			Issue: e.IssueNumber, Stage: to, Needs: needs, Title: e.Title})
	}
	return true
}

func (o *Orchestrator) Approve(ctx context.Context, epicID, source string) error {
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	if shared.EpicStage(e.Stage) != shared.EpicEscalated {
		return fmt.Errorf("epic is not escalated")
	}
	if e.PRNumber <= 0 {
		return fmt.Errorf("no PR to merge")
	}
	p, err := o.d.DB.GetProject(ctx, e.ProjectID)
	if err != nil {
		return err
	}
	pr, err := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
	if err != nil {
		return err
	}
	if pr.Merged {
		o.finishMerged(ctx, p, e, source, "already merged on GitHub")
		return nil
	}
	return o.mergeEpic(ctx, p, e, source, pr.HeadSHA)
}

func (o *Orchestrator) Retry(ctx context.Context, epicID, source string) error {
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	stage := shared.EpicStage(e.Stage)
	if stage != shared.EpicEscalated && stage != shared.EpicStalled {
		return fmt.Errorf("epic is not retryable")
	}
	if !o.transition(ctx, e, shared.EpicQueued, source, "retry") {
		return fmt.Errorf("retry transition failed")
	}
	o.Wake()
	return nil
}

func (o *Orchestrator) Cancel(ctx context.Context, epicID, source string) error {
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	if !o.transition(ctx, e, shared.EpicCanceled, source, "cancel") {
		return fmt.Errorf("cancel transition failed")
	}
	return nil
}

func (o *Orchestrator) RunIssue(ctx context.Context, projectID string, issue int) error {
	p, err := o.d.DB.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	is, err := o.d.GH.GetIssue(ctx, p.Repo, issue)
	if err != nil {
		return err
	}
	if is.IsPR {
		return fmt.Errorf("#%d is a pull request, not an issue", issue)
	}
	if !hasLabel(is.Labels, "agentmon:run") {
		is.Labels = append(is.Labels, "agentmon:run")
		// Label the REAL issue too: sync and webhooks filter on GitHub-side
		// labels, so a mirror-only label would orphan this epic from all
		// future GitHub truth (close, edits, dials).
		if err := o.d.GH.AddLabels(ctx, p.Repo, issue, []string{"agentmon:run"}); err != nil {
			log.Printf("orchestrator[%s]: label run issue #%d: %v", p.Name, issue, err)
		}
	}
	if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, is, o.d.Now())); err != nil {
		return err
	}
	o.Wake()
	return nil
}

func (o *Orchestrator) IngestWebhook(ctx context.Context, ev github.Event) error {
	switch ev.Kind {
	case "issues":
		if ev.Issue == nil || !IsOrchestratedIssue(ev.Issue.Labels) {
			return nil
		}
		p, err := o.d.DB.GetProjectByRepo(ctx, ev.Repo)
		if err != nil {
			return err
		}
		if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, *ev.Issue, o.d.Now())); err != nil {
			return err
		}
	case "pull_request", "check_suite":
	default:
		return nil
	}
	o.Wake()
	return nil
}

func (o *Orchestrator) reconcile(ctx context.Context) error {
	epics, err := o.d.DB.ListNonTerminalEpics(ctx)
	if err != nil {
		return err
	}
	projects := map[string]db.Project{}
	for _, e := range epics {
		if e.PRNumber <= 0 {
			continue
		}
		p, ok := projects[e.ProjectID]
		if !ok {
			var err error
			p, err = o.d.DB.GetProject(ctx, e.ProjectID)
			if err != nil {
				// One bad project row must not abort recovery for the rest.
				log.Printf("orchestrator: reconcile project %s: %v", e.ProjectID, err)
				continue
			}
			projects[e.ProjectID] = p
		}
		pr, err := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
		if err != nil {
			log.Printf("orchestrator[%s]: reconcile PR #%d: %v", p.Name, e.PRNumber, err)
			continue
		}
		if pr.Merged {
			o.finishMerged(ctx, p, e, "github", "reconcile")
		} else if pr.State == "closed" {
			o.transition(ctx, e, shared.EpicCanceled, "github", "reconcile")
		}
	}
	return nil
}
