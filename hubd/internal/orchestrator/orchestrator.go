package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type GitHubAPI interface {
	GetIssue(ctx context.Context, repo string, num int) (github.Issue, error)
	ListIssuesSince(ctx context.Context, repo, since string) ([]github.Issue, error)
	GetPullRequest(ctx context.Context, repo string, num int) (github.PullRequest, error)
	ListCheckRuns(ctx context.Context, repo, ref string) ([]github.CheckRun, error)
	MergePR(ctx context.Context, repo string, num int, sha string) error
	CreateIssueComment(ctx context.Context, repo string, num int, body string) error
	AddLabels(ctx context.Context, repo string, num int, labels []string) error
	RemoveLabel(ctx context.Context, repo string, num int, label string) error
}

type AgentAPI interface {
	CreateSession(ctx context.Context, srv db.Server, target string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error)
	DrainReports(ctx context.Context, srv db.Server, target string) ([]shared.OrchestratorReport, error)
}

type ServerGetter interface {
	Get(ctx context.Context, id string) (db.Server, bool, error)
}

type LivenessAPI interface {
	Session(server, target, session string) (state.SessionView, bool)
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

type Orchestrator struct {
	d          Deps
	wake       chan struct{}
	watermarks map[string]string
	stallSeen  map[string]int
}

func New(d Deps) *Orchestrator {
	if d.Now == nil {
		d.Now = func() string { return time.Now().UTC().Format(time.RFC3339) }
	}
	return &Orchestrator{d: d, wake: make(chan struct{}, 1),
		watermarks: map[string]string{}, stallSeen: map[string]int{}}
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
	if tick == 0 {
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

func (o *Orchestrator) Tick(ctx context.Context) {
	projects, err := o.d.DB.ListProjects(ctx)
	if err != nil {
		log.Printf("orchestrator: list projects: %v", err)
		return
	}
	for _, p := range projects {
		o.tickProject(ctx, p)
	}
}

func (o *Orchestrator) tickProject(ctx context.Context, p db.Project) {
	now := o.d.Now()
	if err := o.syncProject(ctx, p, now); err != nil {
		log.Printf("orchestrator[%s]: sync: %v", p.Name, err)
	}
	o.drainReports(ctx, p)
	o.checkStalls(ctx, p, now)
	o.evaluateGates(ctx, p)
	o.schedule(ctx, p)
}

func (o *Orchestrator) syncProject(ctx context.Context, p db.Project, now string) error {
	issues, err := o.d.GH.ListIssuesSince(ctx, p.Repo, o.watermarks[p.ID])
	if err != nil {
		return err
	}
	for _, is := range issues {
		if !IsOrchestratedIssue(is.Labels) {
			continue
		}
		if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, is, now)); err != nil {
			return err
		}
	}
	o.watermarks[p.ID] = now
	return nil
}

func (o *Orchestrator) drainReports(ctx context.Context, p db.Project) {
	srv, ok, err := o.d.Reg.Get(ctx, p.ServerID)
	if err != nil || !ok {
		if err != nil {
			log.Printf("orchestrator[%s]: server: %v", p.Name, err)
		}
		return
	}
	reports, err := o.d.Agents.DrainReports(ctx, srv, p.Target)
	if err != nil {
		log.Printf("orchestrator[%s]: reports: %v", p.Name, err)
		return
	}
	for _, r := range reports {
		e, err := o.d.DB.GetEpicByIssue(ctx, p.ID, r.Epic)
		if err != nil || !shared.ReportableStage(r.Stage) {
			log.Printf("orchestrator[%s]: dropped report %+v: %v", p.Name, r, err)
			continue
		}
		if r.Session != "" && e.SessionName != "" && r.Session != e.SessionName {
			log.Printf("orchestrator[%s]: report session mismatch: %q != %q", p.Name, r.Session, e.SessionName)
			continue
		}
		if !ValidTransition(shared.EpicStage(e.Stage), r.Stage) {
			log.Printf("orchestrator[%s]: dropped invalid report transition %s→%s", p.Name, e.Stage, r.Stage)
			continue
		}
		if r.PR > 0 {
			_, _ = o.d.DB.SetEpicPR(ctx, e.ID, r.PR, e.Branch)
		}
		if r.Stage == shared.EpicEscalated && r.Note != "" {
			_, _ = o.d.DB.SetEpicNeeds(ctx, e.ID, r.Note)
		}
		o.transition(ctx, e, r.Stage, "report", r.Note)
	}
}

func (o *Orchestrator) checkStalls(ctx context.Context, p db.Project, now string) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: stalls: %v", p.Name, err)
		return
	}
	nowTime, _ := time.Parse(time.RFC3339, now)
	for _, e := range epics {
		stage := shared.EpicStage(e.Stage)
		if stage != shared.EpicStarting && stage != shared.EpicPlanning &&
			stage != shared.EpicImplementing && stage != shared.EpicReviewing {
			delete(o.stallSeen, e.ID)
			continue
		}
		reason := ""
		if _, alive := o.d.Live.Session(p.ServerID, p.Target, e.SessionName); !alive {
			o.stallSeen[e.ID]++
			if o.stallSeen[e.ID] >= 2 {
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
			_, _ = o.d.DB.SetEpicNeeds(ctx, e.ID, reason)
			o.transition(ctx, e, shared.EpicStalled, "hub", reason)
			delete(o.stallSeen, e.ID)
		}
	}
}

func (o *Orchestrator) evaluateGates(ctx context.Context, p db.Project) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: gates: %v", p.Name, err)
		return
	}
	for _, e := range epics {
		stage := shared.EpicStage(e.Stage)
		if (stage != shared.EpicPROpen && stage != shared.EpicMerging) || e.PRNumber <= 0 {
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
		if pr.Merged {
			o.finishMerged(ctx, p, e, "github")
			continue
		}
		if pr.State == "closed" {
			o.transition(ctx, e, shared.EpicCanceled, "github", "PR closed without merge")
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
			o.mergeEpic(ctx, p, e, "hub", pr.HeadSHA)
		default:
			_, _ = o.d.DB.SetEpicNeeds(ctx, e.ID, res.Reason)
			if o.transition(ctx, e, shared.EpicEscalated, "hub", res.Reason) {
				o.comment(ctx, p, e.IssueNumber, "⚠ escalated: "+res.Reason)
			}
		}
	}
}

func (o *Orchestrator) mergeEpic(ctx context.Context, p db.Project, e db.Epic, source, sha string) {
	if shared.EpicStage(e.Stage) != shared.EpicMerging {
		if !o.transition(ctx, e, shared.EpicMerging, source, "") {
			return
		}
		e.Stage = string(shared.EpicMerging)
	}
	if err := o.d.GH.MergePR(ctx, p.Repo, e.PRNumber, sha); err != nil {
		if errors.Is(err, github.ErrNotMergeable) {
			reason := "merge conflict — rebase needed"
			_, _ = o.d.DB.SetEpicNeeds(ctx, e.ID, reason)
			o.transition(ctx, e, shared.EpicEscalated, "hub", reason)
			return
		}
		log.Printf("orchestrator[%s]: merge PR #%d: %v", p.Name, e.PRNumber, err)
		return
	}
	o.finishMerged(ctx, p, e, source)
}

func (o *Orchestrator) finishMerged(ctx context.Context, p db.Project, e db.Epic, source string) {
	if !o.transition(ctx, e, shared.EpicMerged, source, "") {
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
	for _, e := range ReadyEpics(epics, p.MaxParallel, p.Paused) {
		attempt := e.Attempt + 1
		if attempt > o.d.Cfg.MaxAttempts {
			o.transition(ctx, e, shared.EpicFailed, "hub", "attempts exhausted")
			continue
		}
		srv, ok, err := o.d.Reg.Get(ctx, p.ServerID)
		if err != nil || !ok {
			if err != nil {
				log.Printf("orchestrator[%s]: schedule server: %v", p.Name, err)
			}
			continue
		}
		name := SessionNameFor(e.IssueNumber)
		if !o.transition(ctx, e, shared.EpicStarting, "hub", "spawning "+name) {
			continue
		}
		e.Stage = string(shared.EpicStarting)
		_, _ = o.d.DB.SetEpicAssignment(ctx, e.ID, name, attempt)
		_, err = o.d.Agents.CreateSession(ctx, srv, p.Target, shared.CreateSessionRequest{
			Name: name, Cwd: p.Workdir,
			Command: KickoffCommand(ProviderFor(p.Provider, e.Labels), e.IssueNumber),
		})
		if err != nil {
			reason := "spawn failed: " + err.Error()
			_, _ = o.d.DB.SetEpicNeeds(ctx, e.ID, reason)
			o.transition(ctx, e, shared.EpicStalled, "hub", reason)
		}
	}
}

// transition validates, persists, and publishes one stage move. Returns
// whether the move happened (guards races: stale `from` loses silently).
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
	after, err := o.d.DB.GetEpic(ctx, e.ID)
	if err != nil {
		after = e
		after.Stage = string(to)
	}
	if o.d.Bcast != nil {
		o.d.Bcast.Publish(BoardChange{ProjectID: e.ProjectID, EpicID: e.ID,
			Issue: e.IssueNumber, Stage: to, Needs: after.Needs, Title: e.Title})
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
	o.mergeEpic(ctx, p, e, source, pr.HeadSHA)
	return nil
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
	if !hasLabel(is.Labels, "agentmon:run") {
		is.Labels = append(is.Labels, "agentmon:run")
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
	for _, e := range epics {
		if e.PRNumber <= 0 {
			continue
		}
		p, err := o.d.DB.GetProject(ctx, e.ProjectID)
		if err != nil {
			return err
		}
		pr, err := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
		if err != nil {
			log.Printf("orchestrator[%s]: reconcile PR #%d: %v", p.Name, e.PRNumber, err)
			continue
		}
		if pr.Merged {
			o.finishMerged(ctx, p, e, "github")
		} else if pr.State == "closed" {
			o.transition(ctx, e, shared.EpicCanceled, "github", "reconcile")
		}
	}
	return nil
}
