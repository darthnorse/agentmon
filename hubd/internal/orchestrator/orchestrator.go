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

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/registry"
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
	DrainReports(ctx context.Context, srv db.Server, target, instance string, ack uint64) (shared.OrchestratorReportBatch, error)
	// KillSession returns the agent's capture-then-kill usage snapshot along
	// with CapturedAt — the AGENT's own clock at capture time (empty if the
	// agent predates it or captured nothing) — which callers must prefer over
	// the hub's own clock for the reap boundary (see killEpicSession).
	KillSession(ctx context.Context, srv db.Server, target, name string) ([]shared.Usage, string, error)
	TeardownWorktree(ctx context.Context, srv db.Server, target, workdir, branch string) error
	Sessions(ctx context.Context, srv db.Server, target string) ([]shared.Session, error)
}

// drainAck is the per-(server,target) memory of the last received batch; the
// NEXT drain echoes it as the acknowledgment (design doc §4). In-memory only:
// a hub restart forgets it → ack=0 → the agent redelivers everything unacked →
// guarded transitions reject the duplicates. Guarded by tickMu. Keys use the
// project's RAW target string: two projects addressing one agent target as ""
// and as its explicit label alias into two keys (harmless — redelivery is
// absorbed — but use consistent target labels across projects on a server).
type drainAck struct {
	Instance string
	Cursor   uint64
}

type ServerGetter interface {
	Get(ctx context.Context, id string) (db.Server, bool, error)
}

type Deps struct {
	DB     *db.DB
	GH     GitHubAPI
	Agents AgentAPI
	Reg    ServerGetter
	Bcast  *BoardBroadcaster
	Audit  *audit.Recorder
	Cfg    config.OrchestratorCfg
	Now    func() string
}

// userError marks errors whose text is safe and useful to show a client
// verbatim (state errors like "epic is not escalated"). Everything else is
// infrastructure detail the API layer must redact — substring whitelists
// proved leaky (a wrapped GitHub error contained a whitelisted phrase).
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func UserErrorf(format string, a ...any) error { return userError{fmt.Sprintf(format, a...)} }

func IsUserError(err error) bool {
	var u userError
	return errors.As(err, &u)
}

const maxPendingReports = 256

type pendingReport struct {
	ProjectID string
	// ServerID/Target pin the drain origin for repo-routed retries: the
	// routeReport cross-host trust boundary must hold on the retry path too.
	// Empty on ProjectID-routed entries (already past the boundary).
	ServerID string
	Target   string
	R        shared.OrchestratorReport
}

type Orchestrator struct {
	d    Deps
	wake chan struct{}

	// tickMu serializes Tick AND every mutating action method — a Cancel
	// racing schedule() could otherwise spawn a runner for an epic that just
	// went terminal.
	tickMu     sync.Mutex
	watermarks map[string]string // projectID → last successful sync watermark
	syncSkip   map[string]int    // projectID → ticks left to skip after sync failures
	syncFails  map[string]int    // projectID → consecutive sync failures
	stallSeen  map[string]int    // epicID → consecutive ticks with dead session
	// pending holds reports the hub drained but could not apply due to a
	// transient error, with their already-resolved project. Drains are retried
	// next tick, capped to bound memory (in-memory only: reports are acked on the
	// NEXT drain regardless, so a hub crash with entries still pending loses just
	// those transient-DB-error stragglers — a far narrower window than the pre-ack
	// destructive drain).
	pending  []pendingReport
	ackState map[string]drainAck
	// retire holds runner session names whose Cancel/Retry kill failed (agent
	// unreachable). Retried every tick until the kill lands or the session is
	// gone: the next spawn overwrites the epic's SessionName, and branch +
	// worktree names are attempt-agnostic, so a forgotten zombie would share
	// them with its successor. Keyed like ackState; guarded by tickMu.
	retire map[string][]string // serverID+"\x00"+target → session names
	// liveCache memoizes liveSessions results for ONE Tick (reset at Tick
	// start): co-hosted projects share a single agent dial. A present-but-nil
	// entry is a failed fetch (liveness unknown). Keyed like ackState;
	// guarded by tickMu.
	liveCache map[string]map[string]bool
}

func New(d Deps) *Orchestrator {
	if d.Now == nil {
		d.Now = func() string { return time.Now().UTC().Format(time.RFC3339) }
	}
	return &Orchestrator{d: d, wake: make(chan struct{}, 1),
		watermarks: map[string]string{}, syncSkip: map[string]int{},
		syncFails: map[string]int{}, stallSeen: map[string]int{},
		ackState: map[string]drainAck{}, retire: map[string][]string{},
		liveCache: map[string]map[string]bool{}}
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
	o.liveCache = map[string]map[string]bool{}
	projects, err := o.d.DB.ListProjects(ctx)
	if err != nil {
		log.Printf("orchestrator: list projects: %v", err)
		return
	}
	o.retryPending(ctx)
	o.retryRetire(ctx)
	for _, p := range projects {
		o.tickProject(ctx, p)
	}
}

// retryRetire re-attempts runner-session kills that failed. An unkilled
// predecessor shares the epic's attempt-agnostic branch and worktree with
// its successor — it must be chased, not forgotten.
func (o *Orchestrator) retryRetire(ctx context.Context) {
	for key, names := range o.retire {
		serverID, target, _ := strings.Cut(key, "\x00")
		srv, found, err := o.d.Reg.Get(ctx, serverID)
		if err != nil || !found {
			continue
		}
		kept := names[:0]
		for _, name := range names {
			if _, _, err := o.d.Agents.KillSession(ctx, srv, target, name); err != nil && !errors.Is(err, registry.ErrNoSession) {
				kept = append(kept, name)
				continue
			}
			log.Printf("orchestrator: retired stale runner session %q on %s", name, serverID)
		}
		if len(kept) == 0 {
			delete(o.retire, key)
		} else {
			o.retire[key] = kept
		}
	}
}

func (o *Orchestrator) queueRetire(serverID, target, name string) {
	key := serverID + "\x00" + target
	for _, n := range o.retire[key] {
		if n == name {
			return
		}
	}
	o.retire[key] = append(o.retire[key], name)
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
	// Watermark advances ONLY from GitHub-attributed time (issue updated_at):
	// seeding it from the hub clock would re-open the skew blind window the
	// moment the hub runs ahead of GitHub. No issues → watermark unchanged
	// (label-scoped listings make the re-list cheap).
	next := since
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
	key := srv.ID + "\x00" + p.Target
	prev := o.ackState[key]
	batch, err := o.d.Agents.DrainReports(ctx, srv, p.Target, prev.Instance, prev.Cursor)
	if err != nil {
		log.Printf("orchestrator[%s]: reports: %v", p.Name, err)
		return
	}
	deferred := false
	for _, r := range batch.Reports {
		if deferred {
			// An earlier report in this batch was deferred to pending. Defer
			// the rest too (retried next tick, before the next drain): applying
			// them now would reorder per-epic reports, and a stale earlier
			// stage applied late can e.g. silently un-escalate an epic.
			if len(o.pending) < maxPendingReports {
				o.pending = append(o.pending, pendingReport{ServerID: p.ServerID, Target: p.Target, R: r})
			} else {
				log.Printf("orchestrator[%s]: report DROPPED (pending queue full): %+v", p.Name, r)
			}
			continue
		}
		deferred = o.routeReport(ctx, p, r)
	}
	// Remember what this batch delivered; the NEXT drain echoes it as the ack.
	// Storing an empty batch's zero cursor is safe: everything previously acked
	// is already deleted agent-side, and an ack of 0 deletes nothing.
	if batch.Instance != "" {
		o.ackState[key] = drainAck{Instance: batch.Instance, Cursor: batch.Cursor}
	}
}

// routeReport applies one drained report to the project it names. The drain
// is per (server, target) — not per project — so reports for OTHER projects
// on the same host arrive here too: route by Repo, never assume the drainer.
// Returns true when the report was DEFERRED to pending (the caller must then
// defer the rest of its batch to preserve per-epic ordering).
func (o *Orchestrator) routeReport(ctx context.Context, drained db.Project, r shared.OrchestratorReport) bool {
	p := drained
	if r.Repo != "" && !strings.EqualFold(r.Repo, p.Repo) {
		other, err := o.d.DB.GetProjectByRepo(ctx, r.Repo)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) && len(o.pending) < maxPendingReports {
				o.pending = append(o.pending, pendingReport{ServerID: drained.ServerID, Target: drained.Target, R: r})
				return true
			}
			log.Printf("orchestrator[%s]: dropped report for unknown repo %q: %+v", drained.Name, r.Repo, r)
			return false
		}
		// Trust boundary: a report drained from one agent may only drive
		// projects on that same server+target — cross-host claims are noise
		// or spoofing, never legitimate.
		if other.ServerID != drained.ServerID || other.Target != drained.Target {
			log.Printf("orchestrator[%s]: dropped cross-host report for %q (server %s≠%s)",
				drained.Name, r.Repo, other.ServerID, drained.ServerID)
			return false
		}
		p = other
	}
	return o.applyReport(ctx, p, r)
}

// applyReport returns true when the report was deferred to pending.
func (o *Orchestrator) applyReport(ctx context.Context, p db.Project, r shared.OrchestratorReport) bool {
	e, err := o.d.DB.GetEpicByIssue(ctx, p.ID, r.Epic)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("orchestrator[%s]: dropped report for unknown epic #%d", p.Name, r.Epic)
			return false
		}
		// Transient DB error: the report will be acked (deleted agent-side) on
		// the next drain whether or not it applied, so the hub must own it —
		// stash for retry instead of dropping.
		if len(o.pending) < maxPendingReports {
			o.pending = append(o.pending, pendingReport{ProjectID: p.ID, R: r})
			log.Printf("orchestrator[%s]: report deferred (db error): %v", p.Name, err)
			return true
		}
		log.Printf("orchestrator[%s]: report DROPPED (pending queue full): %+v", p.Name, r)
		return false
	}
	if !shared.ReportableStage(r.Stage) {
		log.Printf("orchestrator[%s]: dropped non-reportable stage %q for epic #%d", p.Name, r.Stage, r.Epic)
		return false
	}
	// Provenance: once an epic has an assigned session, only that session's
	// reports count — an EMPTY session claim does not bypass the check.
	if e.SessionName != "" && r.Session != e.SessionName {
		log.Printf("orchestrator[%s]: report session mismatch: %q != %q", p.Name, r.Session, e.SessionName)
		return false
	}
	// SAME-STAGE redelivery (the epic already sits at r.Stage) is the
	// no-op-transition recovery case: a retry after a transient DB write
	// failure must still land its usage snapshot, or the ledger entry is
	// silently lost. It is not a transition at all — ValidTransition(x, x)
	// is always false — so none of the transition-specific guards below
	// (pr_open-without-PR, ValidTransition, branch persistence, SetEpicPR)
	// apply to it; upsert directly and stop here.
	if r.Stage == shared.EpicStage(e.Stage) {
		o.upsertUsage(ctx, p, e, r)
		return false
	}
	// A pr_open claim with no PR number would strand the epic in a stage no
	// scanner revisits. Fail closed; the runner stays in reviewing where the
	// stage timeout still applies. This report is dropped outright below, so
	// — unlike the redelivery case above — its usage must NOT be recorded:
	// see the upsert call at the bottom of this function, which only runs
	// once the transition has actually landed. Upserting here (before this
	// guard, as a prior fix did) would plant a phantom pr_open boundary for
	// a stage the epic never actually entered.
	if r.Stage == shared.EpicPROpen && r.PR <= 0 && e.PRNumber <= 0 {
		log.Printf("orchestrator[%s]: dropped pr_open report without PR number for epic #%d", p.Name, r.Epic)
		return false
	}
	if !ValidTransition(shared.EpicStage(e.Stage), r.Stage) {
		log.Printf("orchestrator[%s]: dropped invalid report transition %s→%s", p.Name, e.Stage, r.Stage)
		return false
	}
	// Branch persistence is plan-gate-escalation ONLY: record the runner's branch
	// pre-PR (so the plan proxy can serve it) exactly once, and never let a
	// non-escalated report claim the epic's branch slot.
	if r.Branch != "" && e.Branch == "" && r.Stage == shared.EpicEscalated && strings.HasPrefix(r.Branch, fmt.Sprintf("epic/%d-", r.Epic)) {
		ok, err := o.d.DB.SetEpicBranch(ctx, e.ID, r.Branch)
		if err != nil {
			if len(o.pending) < maxPendingReports {
				o.pending = append(o.pending, pendingReport{ProjectID: p.ID, R: r})
				log.Printf("orchestrator[%s]: report deferred (branch persist: %v)", p.Name, err)
				return true
			}
			log.Printf("orchestrator[%s]: branch persist DROPPED (pending full): %+v", p.Name, r)
		} else if ok {
			e.Branch = r.Branch
		}
	}
	if r.PR > 0 {
		_, _ = o.d.DB.SetEpicPR(ctx, e.ID, r.PR, e.Branch)
	}
	// Usage is upserted only once the transition has actually landed —
	// o.transition returns false if the transition is illegal (already
	// checked above, so this is really the DB-CAS-loss / lost-race case: the
	// epic moved out from under us between the ValidTransition check and the
	// write) or the DB write itself errors. Either way, a report that never
	// really lands its transition must not plant a usage boundary for a
	// stage the epic never entered.
	if o.transition(ctx, e, r.Stage, "report", r.Note) {
		o.upsertUsage(ctx, p, e, r)
	}
	return false
}

// upsertUsage records every token snapshot a report carries into the epic
// usage ledger. Callers must only invoke this for a report applyReport has
// actually ACCEPTED — a same-stage redelivery, or a real transition that
// ValidTransition allowed and o.transition then actually landed — never for
// one dropped by a guard (missing PR, illegal transition, lost transition
// race): a dropped report never entered the stage boundary it would plant.
// Attempt is read off the epic row (e.Attempt), stable under tickMu, not
// tracked separately on the report. CapturedAt uses the report's own Ts (not
// time.Now()) so an at-least-once redelivery of the same report UPDATEs the
// same idempotent row (UsageRow's UNIQUE key includes captured_at) instead
// of duplicating it. Best-effort: an upsert failure is logged and swallowed
// — usage bookkeeping must never block or reverse a stage transition.
func (o *Orchestrator) upsertUsage(ctx context.Context, p db.Project, e db.Epic, r shared.OrchestratorReport) {
	for _, u := range r.Usage {
		row := db.UsageRow{
			ProjectID: p.ID, ProjectName: p.Name, Repo: p.Repo,
			IssueNumber: e.IssueNumber, Attempt: e.Attempt,
			Stage: string(r.Stage), CapturedAt: r.Ts,
			Provider: u.Provider, Model: u.Model,
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		}
		if err := o.d.DB.UpsertUsage(ctx, row); err != nil {
			log.Printf("orchestrator[%s]: usage upsert epic #%d: %v", p.Name, e.IssueNumber, err)
		}
	}
}

func (o *Orchestrator) retryPending(ctx context.Context) {
	if len(o.pending) == 0 {
		return
	}
	batch := o.pending
	o.pending = nil
	for _, pr := range batch {
		var p db.Project
		var err error
		switch {
		case pr.ProjectID != "":
			p, err = o.d.DB.GetProject(ctx, pr.ProjectID)
		case pr.R.Repo != "":
			p, err = o.d.DB.GetProjectByRepo(ctx, pr.R.Repo)
		default:
			continue
		}
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) && len(o.pending) < maxPendingReports {
				o.pending = append(o.pending, pr) // still transient: keep owning it
			}
			continue
		}
		// Repo-routed retries must re-clear the cross-host trust boundary the
		// deferral skipped past (routeReport enforces it only on the live path).
		if pr.ServerID != "" && (p.ServerID != pr.ServerID || p.Target != pr.Target) {
			log.Printf("orchestrator[%s]: dropped cross-host report for %q (server %s≠%s)",
				p.Name, pr.R.Repo, p.ServerID, pr.ServerID)
			continue
		}
		_ = o.applyReport(ctx, p, pr.R)
	}
}

// stallSessionsTimeout bounds the per-tick liveness GET tighter than the
// generic agent client timeout: liveness is optional this tick (unknown =
// fail safe), so a black-holed host should cost seconds under tickMu, not
// the full client timeout stacked per server.
const stallSessionsTimeout = 3 * time.Second

// liveSessions returns the live tmux session-name set for the project's
// (server, target), fetched at most ONCE per Tick across all projects (the
// cache is reset at Tick start; failures are cached too, so a dead agent
// costs one bounded dial per tick, not one per project). nil = liveness
// UNKNOWN this tick: fail SAFE — no stall verdicts, counters freeze, stage
// timeouts still run.
//
// Liveness = the agent's REAL tmux session list, queried with the project's
// RAW target — the same value CreateSession got (create-with-command sessions
// end with their runner, so tmux existence tracks the runner process). NOT
// the hook-fed state projection: a runner with missing/mispointed provider
// hooks emits no state events at all yet works fine — hook state is a display
// surface, never a liveness source (learned live: the 2026-07-11 toy-repo
// acceptance mass-false-stalled on it).
func (o *Orchestrator) liveSessions(ctx context.Context, p db.Project) map[string]bool {
	key := p.ServerID + "\x00" + p.Target
	if names, ok := o.liveCache[key]; ok {
		return names
	}
	var names map[string]bool
	if srv, ok := o.server(ctx, p, "stalls"); ok {
		sctx, cancel := context.WithTimeout(ctx, stallSessionsTimeout)
		list, err := o.d.Agents.Sessions(sctx, srv, p.Target)
		cancel()
		if err != nil {
			log.Printf("orchestrator[%s]: stalls: agent %s sessions: %v", p.Name, p.ServerID, err)
		} else {
			// The stall verdict trusts this list to be COMPLETE. Agent
			// discovery omits malformed session records from a Partial
			// snapshot (and the wire envelope drops the Partial flag) —
			// safe here only because SessionNameFor emits charset-validated
			// names that can never hit the malformed-record skip. Revisit
			// if orchestrator session naming ever loosens.
			names = make(map[string]bool, len(list))
			for _, s := range list {
				names[s.Name] = true
			}
		}
	}
	o.liveCache[key] = names
	return names
}

func (o *Orchestrator) checkStalls(ctx context.Context, p db.Project, now string) {
	epics, err := o.d.DB.ListEpicsByProject(ctx, p.ID)
	if err != nil {
		log.Printf("orchestrator[%s]: stalls: %v", p.Name, err)
		return
	}
	nowTime, _ := time.Parse(time.RFC3339, now)
	var liveNames map[string]bool
	sessionsFetched := false
	for _, e := range epics {
		stage := shared.EpicStage(e.Stage)
		if stage != shared.EpicStarting && stage != shared.EpicPlanning &&
			stage != shared.EpicImplementing && stage != shared.EpicReviewing {
			delete(o.stallSeen, e.ID)
			continue
		}
		if !sessionsFetched {
			sessionsFetched = true
			liveNames = o.liveSessions(ctx, p)
		}
		reason := ""
		if liveNames != nil {
			if !liveNames[e.SessionName] {
				o.stallSeen[e.ID]++
				// Two consecutive dead ticks AND a real-time grace: wake-driven
				// ticks can run back-to-back, faster than a freshly spawned
				// session settles. Grace = 2 tick periods from the last stage
				// change.
				if o.stallSeen[e.ID] >= 2 && o.graceElapsed(e.StageUpdatedAt, nowTime) {
					reason = "runner session disappeared"
				}
			} else {
				delete(o.stallSeen, e.ID)
			}
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
		if p.RequireCI && len(runs) == 0 {
			pending = true
		}
		res := Decide(GateInput{Verdict: v, VerdictErr: verr, Epic: e.IssueNumber, Labels: e.Labels,
			RequiredReviews: p.RequiredReviews, Requirements: p.Requirements, ChecksGreen: green, ChecksPending: pending})
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
			return UserErrorf("epic #%d: stage moved concurrently", e.IssueNumber)
		}
		e.Stage = string(shared.EpicMerging)
		// Pin the APPROVED head: a merge retry after a transient failure must
		// never adopt a fresher HeadSHA — code pushed after the gate/human
		// decision would merge unevaluated.
		_, _ = o.d.DB.SetEpicApprovedSHA(ctx, e.ID, sha)
		e.ApprovedSHA = sha
	} else if e.ApprovedSHA != "" {
		sha = e.ApprovedSHA
	}
	if err := o.d.GH.MergePR(ctx, p.Repo, e.PRNumber, sha); err != nil {
		if errors.Is(err, github.ErrNotMergeable) {
			// 405/409 covers "already merged" and "head moved" — re-fetch
			// before declaring a conflict.
			pr, err2 := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
			if err2 != nil {
				// Cannot verify: stay in merging and retry next tick rather
				// than mislabeling a possibly-merged PR as a conflict.
				return err2
			}
			if pr.Merged {
				o.finishMerged(ctx, p, e, source, "")
				o.auditMerge(ctx, p, e, source)
				return nil
			}
			reason := "merge rejected by GitHub (branch moved or conflict) — needs a human look"
			o.transition(ctx, e, shared.EpicEscalated, "hub", reason)
			return UserErrorf("epic #%d: %s", e.IssueNumber, reason)
		}
		// Transient failure: stay in merging; the next tick retries the merge
		// (pinned to the approved SHA) without re-running the gate, so an
		// Approve survives a network blip.
		return err
	}
	o.finishMerged(ctx, p, e, source, "")
	o.auditMerge(ctx, p, e, source)
	return nil
}

func (o *Orchestrator) auditMerge(ctx context.Context, p db.Project, e db.Epic, source string) {
	if o.d.Audit == nil {
		return
	}
	principal := source
	if source == "hub" {
		principal = "orchestrator"
	} else {
		principal = strings.TrimPrefix(source, "user:")
	}
	o.d.Audit.EpicMerge(ctx, principal, "project:"+p.ID, e.IssueNumber, e.PRNumber)
}

func (o *Orchestrator) finishMerged(ctx context.Context, p db.Project, e db.Epic, source, note string) {
	if !o.transition(ctx, e, shared.EpicMerged, source, note) {
		return
	}
	if err := o.d.GH.AddLabels(ctx, p.Repo, e.IssueNumber, []string{"agentmon:merged"}); err != nil {
		log.Printf("orchestrator[%s]: label merged: %v", p.Name, err)
	}
	o.comment(ctx, p, e.IssueNumber, fmt.Sprintf("✅ merged PR #%d", e.PRNumber))
	// Best-effort reap: kill the now-idle runner session (a failed kill is queued
	// for per-tick retry inside killEpicSession) and, ONLY once it is confirmed
	// gone, tear down its worktree — never pull a worktree out from under a live
	// runner. The worktree teardown is one-shot best-effort: a transient miss leaves
	// a benign orphaned dir that a later `git worktree prune`/manual sweep clears;
	// we deliberately do not persist reap state (crash-mid-cleanup is a rare,
	// invisible leak not worth a durable state machine). Runner sessions do NOT
	// self-exit after pr_open, so without this a merged epic leaves a dormant session.
	if o.killEpicSession(ctx, e, "merged") && e.Branch != "" {
		if srv, ok := o.server(ctx, p, "merged-teardown"); ok {
			if err := o.d.Agents.TeardownWorktree(ctx, srv, p.Target, p.Workdir, e.Branch); err != nil {
				log.Printf("orchestrator[%s]: merged worktree teardown (branch %q): %v", p.Name, e.Branch, err)
			}
		}
	}
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
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	if shared.EpicStage(e.Stage) != shared.EpicEscalated {
		return UserErrorf("epic is not escalated")
	}
	if e.PRNumber <= 0 {
		return UserErrorf("no PR to merge")
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

// killEpicSession best-effort retires an epic's runner session (design doc
// D12: Cancel, Retry, and a clean merge retire it; a mere stall never kills —
// the human decides, and Retry IS that decision). ErrNoSession is success: the
// session was already gone (a prior reap, a host reboot, or Retry replacing it).
// Any other failure is logged and the kill queued for per-tick retry — the state
// transition already happened and must not be blocked by an unreachable agent.
//
// Returns true when the session is CONFIRMED gone (killed, or already absent, or
// nothing to kill). finishMerged gates worktree teardown on this so it never
// removes a worktree out from under a runner whose kill has not yet landed.
func (o *Orchestrator) killEpicSession(ctx context.Context, e db.Epic, phase string) bool {
	if e.SessionName == "" {
		return true
	}
	p, err := o.d.DB.GetProject(ctx, e.ProjectID)
	if err != nil {
		log.Printf("orchestrator: %s kill: project %s: %v", phase, e.ProjectID, err)
		return false
	}
	srv, ok := o.server(ctx, p, phase+"-kill")
	if !ok {
		return false
	}
	usage, capturedAt, err := o.d.Agents.KillSession(ctx, srv, p.Target, e.SessionName)
	if err != nil && !errors.Is(err, registry.ErrNoSession) {
		// The runner may still be alive, sharing the epic's attempt-agnostic
		// branch/worktree with its successor — queue the kill for per-tick
		// retry before the next spawn overwrites e.SessionName.
		o.queueRetire(srv.ID, p.Target, e.SessionName)
		log.Printf("orchestrator[%s]: %s kill session %q failed (queued for retry): %v", p.Name, phase, e.SessionName, err)
		return false
	}
	// The kill is CONFIRMED (killed, or already gone) — the agent's best-effort
	// capture-then-kill snapshot (if any) is the epic's terminal usage boundary,
	// closing the wasted-cost tail of a merge/cancel/retry reap that would
	// otherwise report nothing past the last stage report.
	if len(usage) > 0 {
		// Prefer the AGENT's own clock (capturedAt) over the hub's: stage-report
		// boundaries are stamped on the agent (r.Ts), and in a multi-host fleet
		// clock skew could otherwise sort the hub-clock reap BEFORE the last
		// real report, misattributing the interval and silently dropping the
		// terminal tail the reap exists to capture. Fall back to the hub clock
		// only for an agent that predates CapturedAt (mixed fleet).
		ts := capturedAt
		if ts == "" {
			ts = o.d.Now()
		}
		o.upsertUsage(ctx, p, e, shared.OrchestratorReport{Stage: shared.EpicStage(e.Stage), Ts: ts, Usage: usage})
	}
	return true
}

func (o *Orchestrator) Retry(ctx context.Context, epicID, source string) error {
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	stage := shared.EpicStage(e.Stage)
	if stage != shared.EpicEscalated && stage != shared.EpicStalled {
		return UserErrorf("epic is not retryable")
	}
	if !o.transition(ctx, e, shared.EpicQueued, source, "retry") {
		return UserErrorf("retry transition failed — the epic moved concurrently")
	}
	o.killEpicSession(ctx, e, "retry")
	o.Wake()
	return nil
}

func (o *Orchestrator) Cancel(ctx context.Context, epicID, source string) error {
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	e, err := o.d.DB.GetEpic(ctx, epicID)
	if err != nil {
		return err
	}
	if !o.transition(ctx, e, shared.EpicCanceled, source, "cancel") {
		return UserErrorf("cancel transition failed — the epic moved concurrently")
	}
	o.killEpicSession(ctx, e, "cancel")
	return nil
}

func (o *Orchestrator) RunIssue(ctx context.Context, projectID string, issue int) error {
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	p, err := o.d.DB.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	is, err := o.d.GH.GetIssue(ctx, p.Repo, issue)
	if err != nil {
		return err
	}
	if is.IsPR {
		return UserErrorf("#%d is a pull request, not an issue", issue)
	}
	if !hasLabel(is.Labels, "agentmon:run") {
		// Label the REAL issue first: sync and webhooks filter on GitHub-side
		// labels, so a mirror-only label would orphan this epic from all
		// future GitHub truth (close, edits, dials). Fail the action rather
		// than schedule an orphan — the user just clicks again.
		if err := o.d.GH.AddLabels(ctx, p.Repo, issue, []string{"agentmon:run"}); err != nil {
			log.Printf("orchestrator[%s]: label run issue #%d: %v", p.Name, issue, err)
			return UserErrorf("labeling issue #%d on GitHub failed — not scheduled", issue)
		}
		is.Labels = append(is.Labels, "agentmon:run")
	}
	if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, is, o.d.Now())); err != nil {
		return err
	}
	o.Wake()
	return nil
}

// IngestWebhook needs no tickMu: its only mutation is the single-statement
// race-safe UpsertEpicIssue (+ guarded transitions), and holding the lock
// here would block GitHub deliveries behind slow ticks.
func (o *Orchestrator) IngestWebhook(ctx context.Context, ev github.Event) error {
	switch ev.Kind {
	case "issues":
		if ev.Issue == nil {
			return nil
		}
		p, err := o.d.DB.GetProjectByRepo(ctx, ev.Repo)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if !IsOrchestratedIssue(ev.Issue.Labels) {
			// Label removal is the opt-out. The label-scoped sync will never
			// see this issue again, so refresh the mirror of an epic we
			// already track and cancel it if still queued.
			e, err := o.d.DB.GetEpicByIssue(ctx, p.ID, ev.Issue.Number)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil // never tracked — not ours
				}
				return err
			}
			if _, err := o.d.DB.UpsertEpicIssue(ctx, EpicFromIssue(p, *ev.Issue, o.d.Now())); err != nil {
				return err
			}
			if e.Stage == string(shared.EpicQueued) {
				// Cancel with the ORIGINAL queued row: the guarded UPDATE then
				// races the scheduler on the same expected stage and exactly
				// one wins. A fresh re-fetch could observe `starting` and
				// legally cancel an epic whose session is mid-spawn.
				o.transition(ctx, e, shared.EpicCanceled, "github", "orchestration label removed")
			}
			o.Wake()
			return nil
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
