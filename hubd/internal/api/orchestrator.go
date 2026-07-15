package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"agentmon/hubd/internal/agentws"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/orchestrator"
	"agentmon/shared"
)

const (
	maxOrchestratorBody = 16 << 10
	maxParallelCeiling  = 32
)

var errServerNotFound = errors.New("server not found")

type epicDTO struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Issue          int             `json:"issue"`
	Title          string          `json:"title"`
	Labels         []string        `json:"labels"`
	BlockedBy      []int           `json:"blocked_by"`
	Stage          string          `json:"stage"`
	Attempt        int             `json:"attempt"`
	Session        string          `json:"session"`
	Branch         string          `json:"branch"`
	PR             int             `json:"pr"`
	Verdict        string          `json:"verdict,omitempty"`
	Needs          string          `json:"needs"`
	IssueState     string          `json:"issue_state"`
	QueuedAt       string          `json:"queued_at"`
	StartedAt      string          `json:"started_at"`
	StageUpdatedAt string          `json:"stage_updated_at"`
	MergedAt       string          `json:"merged_at"`
	Usage          *usageRollupDTO `json:"usage,omitempty"`
}

// usageRollupDTO is a light token/cost/duration summary — the board's inline
// figure, not the full per-attempt/per-stage breakdown (that's the drawer's
// detail view, Task 14). Cost is a pointer so an unpriced model can fail
// closed to "$—" (nil) instead of a misleading low number, matching
// orchestrator.AggregateCost.
type usageRollupDTO struct {
	Tokens     int64    `json:"tokens"`
	Cost       *float64 `json:"cost"`
	DurationMs int64    `json:"duration_ms"`
}

func toEpicDTO(e db.Epic, usage *usageRollupDTO) epicDTO {
	return epicDTO{
		ID: e.ID, ProjectID: e.ProjectID, Issue: e.IssueNumber, Title: e.Title,
		Labels: e.Labels, BlockedBy: e.BlockedBy, Stage: e.Stage, Attempt: e.Attempt,
		Session: e.SessionName, Branch: e.Branch, PR: e.PRNumber, Verdict: e.Verdict,
		Needs: e.Needs, IssueState: e.IssueState, QueuedAt: e.QueuedAt,
		StartedAt: e.StartedAt, StageUpdatedAt: e.StageUpdatedAt, MergedAt: e.MergedAt,
		Usage: usage,
	}
}

// epicUsageRollups computes each epic's light usage rollup with ONE grouped
// read of the project's usage ledger (never one query per epic). Rows are
// grouped by issue number and handed to orchestrator.DeriveEpicUsage per
// epic — reusing that derivation keeps this inline figure identical to the
// drawer's detail breakdown (Task 14) rather than a second, divergent sum.
// An epic with no ledger rows is simply absent from the returned map, so a
// lookup miss (nil) is exactly "no usage yet". Best-effort: a ledger read
// failure logs and yields no rollups rather than failing the board.
func (d Deps) epicUsageRollups(ctx context.Context, projectID string, epics []db.Epic) map[string]*usageRollupDTO {
	rows, err := d.DB.ListProjectUsage(ctx, projectID)
	if err != nil {
		log.Printf("api: project %s usage rollup: %v", projectID, err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	byIssue := map[int][]db.UsageRow{}
	for _, r := range rows {
		byIssue[r.IssueNumber] = append(byIssue[r.IssueNumber], r)
	}
	out := map[string]*usageRollupDTO{}
	for _, e := range epics {
		issueRows := byIssue[e.IssueNumber]
		if len(issueRows) == 0 {
			continue
		}
		u := orchestrator.DeriveEpicUsage(issueRows, e)
		out[e.ID] = &usageRollupDTO{Tokens: u.Tokens.Total, Cost: u.Cost, DurationMs: u.DurationMs}
	}
	return out
}

type projectDTO struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Repo            string           `json:"repo"`
	ServerID        string           `json:"server_id"`
	Target          string           `json:"target"`
	Workdir         string           `json:"workdir"`
	BaseBranch      string           `json:"base_branch"`
	Provider        string           `json:"provider"`
	RequiredReviews []string         `json:"required_reviews"`
	MaxParallel     int              `json:"max_parallel"`
	Paused          bool             `json:"paused"`
	RequireCI       bool             `json:"require_ci"`
	Pinned          bool             `json:"pinned"`
	Requirements    []db.Requirement `json:"requirements"`
	Counts          map[string]int   `json:"counts,omitempty"`
}

// projectOut builds the wire DTO. There is deliberately no project-level
// usage rollup here: it was never rendered (ProjectHeader fetches the full
// breakdown from GET /projects/{id}/usage instead) and diverged from that
// endpoint past 50 epics (this board path's ListBoardEpics is capped;
// GET .../usage's ListEpicsByProject isn't) — a field nothing reads that can
// disagree with the real number is worse than no field. The epic-level
// rollup (epicDTO.Usage, from epicUsageRollups) is unaffected: EpicCard does
// render that one.
func projectOut(p db.Project, counts map[string]int) projectDTO {
	return projectDTO{p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider, p.RequiredReviews, p.MaxParallel, p.Paused, p.RequireCI, p.Pinned, p.Requirements, counts}
}

type eventDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"`
	Note   string `json:"note"`
	Ts     string `json:"ts"`
}

func (d Deps) OrchestratorProjectsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*"); !ok {
				return
			}
			ps, err := d.DB.ListProjects(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			out := make([]projectDTO, 0, len(ps))
			for _, p := range ps {
				es, err := d.DB.ListEpicsByProject(r.Context(), p.ID)
				if err != nil {
					log.Printf("api: project %s epic counts: %v", p.ID, err)
				}
				counts := map[string]int{}
				for _, e := range es {
					counts[e.Stage]++
				}
				out = append(out, projectOut(p, counts))
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "orchestrator:*")
		if !ok {
			return
		}
		if d.Orch == nil {
			// Registering projects on a token-less hub would only create rows
			// an absent orchestrator never runs.
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		var in struct {
			Name            string           `json:"name"`
			Repo            string           `json:"repo"`
			ServerID        string           `json:"server_id"`
			Target          string           `json:"target"`
			Workdir         string           `json:"workdir"`
			BaseBranch      string           `json:"base_branch"`
			Provider        string           `json:"provider"`
			RequiredReviews []string         `json:"required_reviews"`
			Requirements    []db.Requirement `json:"requirements"`
			MaxParallel     int              `json:"max_parallel"`
			RequireCI       bool             `json:"require_ci"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrchestratorBody)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if in.Name == "" || in.Repo == "" || in.ServerID == "" || in.Workdir == "" {
			writeJSONError(w, http.StatusBadRequest, "missing required field")
			return
		}
		if !github.IsValidRepo(in.Repo) {
			writeJSONError(w, http.StatusBadRequest, "repo must be owner/name")
			return
		}
		if in.Provider == "" {
			in.Provider = "claude"
		}
		if in.Provider != "claude" && in.Provider != "codex" {
			writeJSONError(w, http.StatusBadRequest, "provider must be claude or codex")
			return
		}
		if in.MaxParallel < 0 || in.MaxParallel > maxParallelCeiling {
			writeJSONError(w, http.StatusBadRequest, "max_parallel out of range")
			return
		}
		if in.MaxParallel == 0 {
			in.MaxParallel = 1
		}
		if in.BaseBranch == "" {
			in.BaseBranch = "main"
		}
		// The server must exist and be ACTIVE — a project bound to an unknown
		// server would fail every tick with no way to fix it (no update API).
		if _, found, err := d.Reg.Get(r.Context(), in.ServerID); err != nil {
			log.Printf("api: project create registry: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		} else if !found {
			writeJSONError(w, http.StatusBadRequest, "unknown server")
			return
		}
		reqs, err := normalizeRequirements(in.Requirements)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		pr := db.Project{ID: uuid.NewString(), Name: in.Name, Repo: in.Repo, ServerID: in.ServerID, Target: in.Target, Workdir: in.Workdir, BaseBranch: in.BaseBranch, Provider: in.Provider, RequiredReviews: in.RequiredReviews, Requirements: reqs, MaxParallel: in.MaxParallel, RequireCI: in.RequireCI}
		if err := d.DB.CreateProject(r.Context(), pr); err != nil {
			writeJSONError(w, http.StatusBadRequest, "create failed")
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectRegister(r.Context(), p.ID, "project:"+pr.ID, pr.Repo, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusCreated, projectOut(pr, nil))
	}
}

func (d Deps) OrchestratorBoardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		es, err := d.DB.ListBoardEpics(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		rollups := d.epicUsageRollups(r.Context(), id, es)
		dto := make([]epicDTO, 0, len(es))
		events := map[string][]eventDTO{}
		for _, e := range es {
			dto = append(dto, toEpicDTO(e, rollups[e.ID]))
			evs, err := d.DB.ListEpicEvents(r.Context(), e.ID, 20)
			if err != nil {
				continue
			}
			out := make([]eventDTO, 0, len(evs))
			for _, ev := range evs {
				out = append(out, eventDTO{From: ev.FromStage, To: ev.ToStage, Source: ev.Source, Note: ev.Note, Ts: ev.Ts})
			}
			events[e.ID] = out
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": projectOut(p, nil), "epics": dto, "events": events})
	}
}

// epicScoped lists the actions that operate on a single epic and therefore
// must verify the epic belongs to the project in the URL — the authorize and
// audit resource is "project:{id}", and letting an epic_id reach another
// project would make that binding a lie (and an IDOR once real policy lands).
func epicScoped(action string) bool {
	switch action {
	case "approve", "retry", "cancel", "guidance":
		return true
	}
	return false
}

func (d Deps) OrchestratorActionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		var in struct {
			Action string `json:"action"`
			EpicID string `json:"epic_id"`
			Issue  int    `json:"issue"`
			Value  int    `json:"value"`
			On     bool   `json:"on"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrchestratorBody)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		var epic db.Epic
		if epicScoped(in.Action) {
			e, err := d.DB.GetEpic(r.Context(), in.EpicID)
			switch {
			case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
				writeJSONError(w, http.StatusNotFound, "epic not found in project")
				return
			case err != nil:
				log.Printf("api: action epic lookup: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			epic = e
		}
		var err error
		source := "user:" + p.ID
		switch in.Action {
		case "approve":
			err = d.Orch.Approve(r.Context(), in.EpicID, source)
		case "retry":
			err = d.Orch.Retry(r.Context(), in.EpicID, source)
		case "cancel":
			err = d.Orch.Cancel(r.Context(), in.EpicID, source)
		case "pause", "resume":
			var found bool
			found, err = d.DB.SetProjectPaused(r.Context(), id, in.Action == "pause")
			if err == nil && !found {
				writeJSONError(w, http.StatusNotFound, "not found")
				return
			}
			if err == nil {
				d.Orch.Wake()
			}
		case "set_max_parallel":
			if in.Value < 1 || in.Value > maxParallelCeiling {
				writeJSONError(w, http.StatusBadRequest, "max_parallel out of range")
				return
			}
			var found bool
			found, err = d.DB.SetProjectMaxParallel(r.Context(), id, in.Value)
			if err == nil && !found {
				writeJSONError(w, http.StatusNotFound, "not found")
				return
			}
			if err == nil {
				d.Orch.Wake()
			}
		case "set_require_ci":
			var found bool
			found, err = d.DB.SetProjectRequireCI(r.Context(), id, in.On)
			if err == nil && !found {
				writeJSONError(w, http.StatusNotFound, "not found")
				return
			}
			if err == nil {
				d.Orch.Wake()
			}
		case "set_pinned":
			// Pin state is presentational (home-header chips) and never feeds
			// the scheduler, so — unlike the other setters — no d.Orch.Wake().
			var found bool
			found, err = d.DB.SetProjectPinned(r.Context(), id, in.On)
			if err == nil && !found {
				writeJSONError(w, http.StatusNotFound, "not found")
				return
			}
			// Fan a project-level board change so OTHER connected clients refresh
			// their pinned-chip row (the initiator already invalidates locally via
			// useEpicActions). Empty Stage → the web-push dispatcher skips it
			// (push.go only fires on escalated/stalled), so no spurious push.
			if err == nil && d.BoardBcast != nil {
				d.BoardBcast.Publish(orchestrator.BoardChange{ProjectID: id})
			}
		case "run_issue":
			if in.Issue < 1 {
				writeJSONError(w, http.StatusBadRequest, "issue must be a positive number")
				return
			}
			err = d.Orch.RunIssue(r.Context(), id, in.Issue)
		case "guidance":
			err = d.sendGuidance(r.Context(), epic, p.ID, in.Text)
		default:
			writeJSONError(w, http.StatusBadRequest, "unknown action")
			return
		}
		if err != nil {
			writeActionError(w, err)
			return
		}
		if d.Audit != nil {
			d.Audit.EpicAction(r.Context(), p.ID, "project:"+id, in.Action, in.EpicID, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// sendGuidance delivers text into the epic's live runner session. CR (\r)
// submits in Claude Code; a bare LF only inserts a soft newline (see
// web/src/lib/terminal-keys.ts) — terminating with \n would type the guidance
// and never send it, silently breaking the plan-approval flow.
func (d Deps) sendGuidance(ctx context.Context, e db.Epic, principalID, text string) error {
	project, err := d.DB.GetProject(ctx, e.ProjectID)
	if err != nil {
		return err
	}
	srv, found, err := d.Reg.Get(ctx, project.ServerID)
	if err != nil {
		return err
	}
	if !found {
		return errServerNotFound
	}
	sessions, err := d.Agent.Sessions(ctx, srv, project.Target)
	if err != nil {
		return err
	}
	msg := strings.TrimRight(text, "\r\n") + "\r"
	if err := agentws.SendText(ctx, srv, &d.Minter, principalID, e.SessionName, msg, sessions); err != nil {
		if strings.Contains(err.Error(), "has no pane") {
			return orchestrator.UserErrorf("runner session %q has no pane — is it still alive?", e.SessionName)
		}
		return err
	}
	return nil
}

func writeActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, errServerNotFound):
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	case orchestrator.IsUserError(err):
		// Typed, not substring-matched: a wrapped infrastructure error must
		// never leak because its text happens to contain a friendly phrase.
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	// Everything else is infrastructure detail (db/github/agent internals):
	// log it, don't ship it to the browser.
	log.Printf("api: orchestrator action failed: %v", err)
	writeJSONError(w, http.StatusBadGateway, "action failed")
}

// OrchestratorProjectPatchHandler edits the registration fields. Partial
// semantics via pointer fields: absent = unchanged. repo/server_id are
// immutable (spec §5.3) — rejecting them loudly beats silently ignoring.
func (d Deps) OrchestratorProjectPatchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		var in struct {
			Name            *string           `json:"name"`
			Repo            *string           `json:"repo"`
			ServerID        *string           `json:"server_id"`
			Workdir         *string           `json:"workdir"`
			Target          *string           `json:"target"`
			BaseBranch      *string           `json:"base_branch"`
			Provider        *string           `json:"provider"`
			RequiredReviews *[]string         `json:"required_reviews"`
			Requirements    *[]db.Requirement `json:"requirements"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrchestratorBody)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if in.Repo != nil {
			writeJSONError(w, http.StatusBadRequest, "repo cannot be changed — register a new project")
			return
		}
		if in.ServerID != nil {
			writeJSONError(w, http.StatusBadRequest, "server cannot be changed")
			return
		}
		pr, err := d.DB.GetProject(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if in.Name != nil {
			pr.Name = *in.Name
		}
		if in.Workdir != nil && *in.Workdir != pr.Workdir {
			// Workdir is where the runner's git worktree lives; the merge-time reap
			// resolves the worktree relative to it. Changing it while a runner is live
			// would point teardown at the wrong clone — deleting an unrelated same-named
			// branch there and leaking the real worktree. Refuse while any non-terminal
			// epic exists, the same rule as target below.
			active, err := d.DB.CountActiveEpics(r.Context(), id)
			if err != nil {
				log.Printf("api: count active epics: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if active > 0 {
				writeJSONError(w, http.StatusBadRequest, "cannot change workdir while epics are running")
				return
			}
			pr.Workdir = *in.Workdir
		}
		if in.Target != nil && *in.Target != pr.Target {
			// Target is the tmux socket identity the orchestrator drains, checks
			// liveness on, and cancels/retires runner sessions against. Changing it
			// while a runner is live would strand that session on the old socket —
			// reports lost, control actions misrouted. Refuse while any non-terminal
			// epic exists (repo/server_id are already immutable above). provider and
			// base_branch stay mutable: they affect only future spawns, not a
			// running session's socket, so they can't orphan a live runner.
			active, err := d.DB.CountActiveEpics(r.Context(), id)
			if err != nil {
				log.Printf("api: count active epics: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if active > 0 {
				writeJSONError(w, http.StatusBadRequest, "cannot change target while epics are running")
				return
			}
			pr.Target = *in.Target
		}
		if in.BaseBranch != nil {
			pr.BaseBranch = *in.BaseBranch
		}
		if in.Provider != nil {
			pr.Provider = *in.Provider
		}
		if in.RequiredReviews != nil {
			pr.RequiredReviews = *in.RequiredReviews
		}
		if in.Requirements != nil {
			reqs, err := normalizeRequirements(*in.Requirements)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			pr.Requirements = reqs
		}
		if pr.Name == "" || pr.Workdir == "" || pr.BaseBranch == "" {
			writeJSONError(w, http.StatusBadRequest, "missing required field")
			return
		}
		if pr.Provider != "claude" && pr.Provider != "codex" {
			writeJSONError(w, http.StatusBadRequest, "provider must be claude or codex")
			return
		}
		found, err := d.DB.UpdateProject(r.Context(), pr)
		if errors.Is(err, db.ErrDuplicateName) {
			writeJSONError(w, http.StatusBadRequest, "name already in use")
			return
		}
		if err != nil {
			// A genuine backend failure (lock/IO) is not a client error; don't
			// misreport it as a 400 name collision.
			log.Printf("api: project update: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectUpdate(r.Context(), p.ID, "project:"+id, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusOK, projectOut(pr, nil))
	}
}

// OrchestratorProjectDeleteHandler removes a project once nothing is running:
// the DB layer refuses (found, active>0) while non-terminal epics exist.
func (d Deps) OrchestratorProjectDeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		pr, err := d.DB.GetProject(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		found, active, err := d.DB.DeleteProject(r.Context(), id)
		if err != nil {
			log.Printf("api: project delete: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if active > 0 {
			plural := "s"
			if active == 1 {
				plural = ""
			}
			writeJSONError(w, http.StatusConflict, fmt.Sprintf("project has %d active epic%s — cancel or finish them first", active, plural))
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectDelete(r.Context(), p.ID, "project:"+id, pr.Repo, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// boardSnapshot assembles the cross-project board (projects + bounded epics).
// One source of truth for both the SSE board-snapshot event and GET /board —
// the two must never drift. Slices are always non-nil so they marshal as [].
func (d Deps) boardSnapshot(ctx context.Context) ([]projectDTO, []epicDTO, error) {
	projects, err := d.DB.ListProjects(ctx)
	if err != nil {
		return nil, nil, err
	}
	projDTOs := make([]projectDTO, 0, len(projects))
	epics := make([]epicDTO, 0, 64)
	for _, pr := range projects {
		es, err := d.DB.ListBoardEpics(ctx, pr.ID)
		if err != nil {
			return nil, nil, err
		}
		rollups := d.epicUsageRollups(ctx, pr.ID, es)
		projDTOs = append(projDTOs, projectOut(pr, nil))
		for _, e := range es {
			epics = append(epics, toEpicDTO(e, rollups[e.ID]))
		}
	}
	return projDTOs, epics, nil
}

// OrchestratorAllBoardHandler is the All-projects board query (spec §5.1).
// orchestrator_enabled tells the web "dormant hub" apart from "no projects".
func (d Deps) OrchestratorAllBoardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*"); !ok {
			return
		}
		projects, epics, err := d.boardSnapshot(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"orchestrator_enabled": d.Orch != nil,
			"projects":             projects,
			"epics":                epics,
		})
	}
}

// ContentsFetcher is the slice of the GitHub client the plan proxy needs.
type ContentsFetcher interface {
	GetContents(ctx context.Context, repo, path, ref string) ([]byte, error)
}

var (
	planNoteRe = regexp.MustCompile(`plan ready at (\S+)`)
	planPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// planDirPrefix is the runner-skill plan convention (epic-pipeline.md commits and
// reports docs/plans/epic-N.md). A note that names any other repo-relative path
// falls back to the default so the proxy can only ever serve a plan doc.
const planDirPrefix = "docs/plans/"

// artifactDirs is the fail-closed allowlist for the generic artifact proxy.
// The endpoint can only ever serve a doc under one of these dirs, via the
// GitHub Contents API — never the host filesystem. Extensible.
var artifactDirs = []string{"docs/plans/", "docs/reviews/"}

// validateArtifactPath applies the plan-proxy's fail-closed rules to a
// user-supplied path: bounded length, no leading slash, no traversal, safe
// chars (planPathRe), .md only, and under an allowlisted artifact dir. This is
// the security boundary (spec §Security) — false → 400.
func validateArtifactPath(p string) bool {
	if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") ||
		!strings.HasSuffix(p, ".md") || !planPathRe.MatchString(p) {
		return false
	}
	for _, dir := range artifactDirs {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

// planDocPath resolves the plan document path from the escalation note
// (runner-skill convention: "plan-gate: plan ready at <path>"), falling back
// to the docs/plans convention. The note is runner-controlled text, so
// sanitization failure falls back silently — never an error, never a 500.
func planDocPath(needs string, issue int) string {
	def := fmt.Sprintf("%sepic-%d.md", planDirPrefix, issue)
	m := planNoteRe.FindStringSubmatch(needs)
	if m == nil {
		return def
	}
	p := m[1]
	// Constrain to the plan directory (defense-in-depth): even a traversal-safe
	// path outside docs/plans/ falls back to the default rather than letting the
	// proxy fetch an arbitrary repo file. The note is runner-controlled today,
	// not user-settable — this bounds a future code path that might change that.
	if len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") ||
		!strings.HasPrefix(p, planDirPrefix) || !planPathRe.MatchString(p) {
		return def
	}
	return p
}

// fetchArtifact resolves a repo-relative .md doc for an epic and writes the
// {path,ref,markdown} JSON response (or the mapped error status). It tries the
// epic branch first, then — only when allowBaseFallback is set — falls back to
// the project base branch on ErrNotFound. That fallback is for a MERGED epic
// only: its branch is often deleted post-merge, so the doc now lives on the
// base branch. An active (non-merged) epic's branch-side 404 is a genuine
// not-pushed-yet 404 — we must NOT serve a possibly-stale same-named doc off
// the base branch (e.g. a prior merged epic's docs/plans/epic-N.md), which
// would let a human approve an obsolete plan. Callers must have validated
// `path`, confirmed branch != "" and d.Contents != nil.
func (d Deps) fetchArtifact(ctx context.Context, w http.ResponseWriter, repo, baseBranch, branch, path string, allowBaseFallback bool) {
	b, err := d.Contents.GetContents(ctx, repo, path, branch)
	ref := branch
	if allowBaseFallback && errors.Is(err, github.ErrNotFound) && baseBranch != "" && baseBranch != branch {
		if b2, err2 := d.Contents.GetContents(ctx, repo, path, baseBranch); err2 == nil || !errors.Is(err2, github.ErrNotFound) {
			b, err, ref = b2, err2, baseBranch
		}
	}
	switch {
	case errors.Is(err, github.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("artifact not available at %s (may not be pushed yet)", path))
	case errors.Is(err, github.ErrTooLarge):
		writeJSONError(w, http.StatusRequestEntityTooLarge, "artifact exceeds 256 KiB — open it on GitHub")
	case err != nil:
		log.Printf("api: epic artifact fetch: %v", err)
		writeJSONError(w, http.StatusBadGateway, "artifact fetch failed")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"path": path, "ref": ref, "markdown": string(b)})
	}
}

// OrchestratorEpicPlanHandler proxies the epic's committed plan doc off its
// branch (spec §5.2). Hub-side because the PAT never reaches the browser; it
// can only ever read from the project's registered repo.
func (d Deps) OrchestratorEpicPlanHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		if d.Contents == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if e.Branch == "" {
			writeJSONError(w, http.StatusConflict, "epic has no branch yet — the plan is committed once the runner starts")
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		merged := e.Stage == string(shared.EpicMerged)
		d.fetchArtifact(r.Context(), w, p.Repo, p.BaseBranch, e.Branch, planDocPath(e.Needs, e.IssueNumber), merged)
	}
}

// OrchestratorEpicArtifactHandler proxies any allowlisted committed .md
// artifact (plan or review evidence) off the epic branch — or the base branch
// for merged epics (spec §1). The `path` query param is user-settable, so it is
// validated fail-closed against artifactDirs BEFORE any GitHub access: the
// endpoint can only ever read docs/plans/ or docs/reviews/, never the host FS.
func (d Deps) OrchestratorEpicArtifactHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		if d.Contents == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		path := r.URL.Query().Get("path")
		if !validateArtifactPath(path) {
			writeJSONError(w, http.StatusBadRequest, "invalid or disallowed artifact path")
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if e.Branch == "" {
			writeJSONError(w, http.StatusConflict, "epic has no branch yet")
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		merged := e.Stage == string(shared.EpicMerged)
		d.fetchArtifact(r.Context(), w, p.Repo, p.BaseBranch, e.Branch, path, merged)
	}
}

// OrchestratorEpicUsageHandler returns one epic's full derived usage
// breakdown (Task 14's detail view, vs. the board's collapsed
// usageRollupDTO). Epic resolution and authorization mirror
// OrchestratorEpicPlanHandler exactly — same path values, same
// project:{id} view-scoped authz, same cross-project-guard 404 — but there
// is no branch/PR requirement: usage rows can exist (and are worth showing)
// before a branch is ever committed.
func (d Deps) OrchestratorEpicUsageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		rows, err := d.DB.ListEpicUsage(r.Context(), id, e.IssueNumber)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, orchestrator.DeriveEpicUsage(rows, e))
	}
}

// projectStageUsageDTO is one pipeline stage's contribution to a project's
// usage summary, folded across every epic and attempt that ever reported it.
type projectStageUsageDTO struct {
	Stage      string                   `json:"stage"`
	Tokens     orchestrator.TokenTotals `json:"tokens"`
	Cost       *float64                 `json:"cost"`
	DurationMs int64                    `json:"duration_ms"`
}

// projectUsageDTO is a project-wide usage summary: every epic's derived
// usage (orchestrator.DeriveEpicUsage) folded into one set of totals, a
// by-stage breakdown, and a by-model breakdown.
type projectUsageDTO struct {
	Tokens     orchestrator.TokenTotals  `json:"tokens"`
	Cost       *float64                  `json:"cost"`
	DurationMs int64                     `json:"duration_ms"`
	ByStage    []projectStageUsageDTO    `json:"by_stage"`
	ByModel    []orchestrator.ModelUsage `json:"by_model"`
}

// OrchestratorProjectUsageHandler returns the project-wide usage summary
// (Task 14): every epic's ledger, derived and folded across attempts/stages/
// models. A project with no ledger rows at all (including an unknown
// project id) returns a zeroed DTO with 200, not 404 — same "absence is a
// valid, displayable zero" stance as the rest of the usage feature.
func (d Deps) OrchestratorProjectUsageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		rows, err := d.DB.ListProjectUsage(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		epics, err := d.DB.ListEpicsByProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, aggregateProjectUsage(rows, epics))
	}
}

// aggregateProjectUsage groups the project's usage ledger by epic (issue
// number), derives each epic's usage via orchestrator.DeriveEpicUsage — the
// same per-attempt/stage/model logic that powers the epic detail endpoint
// and the board's inline per-epic rollup — then folds every epic's
// attempts/stages into one project-wide summary: by_stage sums every
// attempt's UsageStage entries by stage name across epics, by_model sums
// every stage's ByModel entries by (provider,model) across epics.
//
// The (provider,model) folding and final pricing reuse orchestrator's own
// exported helpers (orchestrator.ModelKey/ModelUsageList/AggregateCost)
// rather than a local copy of the same logic — this package's only job is
// the epic→project fold; the model-bucket→priced-breakdown step is
// identical to what DeriveEpicUsage already does internally, so it's built
// once there and called from here.
//
// Cost aggregation mirrors orchestrator.AggregateCost's fail-closed rule at
// every scope except by_model: project-total and per-stage cost are nil the
// moment ANY contributing model in that scope is unpriced, and are the real
// sum only when every contributing model is known. by_model rows have no
// such ambiguity: each is priced directly from its own aggregated token
// bucket via orchestrator.CostUSD, independent of whatever else shares its
// scope, and is left as "sum/blank what you know" — a lone (provider,model)
// row is unambiguously priced-or-null.
func aggregateProjectUsage(rows []db.UsageRow, epics []db.Epic) projectUsageDTO {
	byIssue := map[int][]db.UsageRow{}
	for _, r := range rows {
		byIssue[r.IssueNumber] = append(byIssue[r.IssueNumber], r)
	}

	var tokens orchestrator.TokenTotals
	var durationMs int64

	stageOrder := []string{}
	stageSeen := map[string]bool{}
	stageTokens := map[string]orchestrator.TokenTotals{}
	stageDurationMs := map[string]int64{}
	stageModelTokens := map[string]map[orchestrator.ModelKey]orchestrator.TokenTotals{}
	projectModelTokens := map[orchestrator.ModelKey]orchestrator.TokenTotals{}

	for _, e := range epics {
		issueRows := byIssue[e.IssueNumber]
		if len(issueRows) == 0 {
			continue
		}
		eu := orchestrator.DeriveEpicUsage(issueRows, e)
		tokens = orchestrator.AddTokens(tokens, eu.Tokens)
		durationMs += eu.DurationMs

		for _, att := range eu.Attempts {
			for _, st := range att.Stages {
				if !stageSeen[st.Stage] {
					stageSeen[st.Stage] = true
					stageOrder = append(stageOrder, st.Stage)
					stageModelTokens[st.Stage] = map[orchestrator.ModelKey]orchestrator.TokenTotals{}
				}
				stageTokens[st.Stage] = orchestrator.AddTokens(stageTokens[st.Stage], st.Tokens)
				stageDurationMs[st.Stage] += st.DurationMs
				for _, m := range st.ByModel {
					k := orchestrator.ModelKey{Provider: m.Provider, Model: m.Model}
					stageModelTokens[st.Stage][k] = orchestrator.AddTokens(stageModelTokens[st.Stage][k], m.Tokens)
					projectModelTokens[k] = orchestrator.AddTokens(projectModelTokens[k], m.Tokens)
				}
			}
		}
	}

	byModel := orchestrator.ModelUsageList(projectModelTokens)

	byStage := make([]projectStageUsageDTO, 0, len(stageOrder))
	for _, stage := range stageOrder {
		models := orchestrator.ModelUsageList(stageModelTokens[stage])
		byStage = append(byStage, projectStageUsageDTO{
			Stage:      stage,
			Tokens:     stageTokens[stage],
			Cost:       orchestrator.AggregateCost(models),
			DurationMs: stageDurationMs[stage],
		})
	}

	return projectUsageDTO{
		Tokens:     tokens,
		Cost:       orchestrator.AggregateCost(byModel),
		DurationMs: durationMs,
		ByStage:    byStage,
		ByModel:    byModel,
	}
}
