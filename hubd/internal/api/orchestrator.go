package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"agentmon/hubd/internal/agentws"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/orchestrator"
)

const (
	maxOrchestratorBody = 16 << 10
	maxParallelCeiling  = 32
)

var errServerNotFound = errors.New("server not found")

type epicDTO struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id"`
	Issue          int      `json:"issue"`
	Title          string   `json:"title"`
	Labels         []string `json:"labels"`
	BlockedBy      []int    `json:"blocked_by"`
	Stage          string   `json:"stage"`
	Attempt        int      `json:"attempt"`
	Session        string   `json:"session"`
	Branch         string   `json:"branch"`
	PR             int      `json:"pr"`
	Verdict        string   `json:"verdict,omitempty"`
	Needs          string   `json:"needs"`
	IssueState     string   `json:"issue_state"`
	QueuedAt       string   `json:"queued_at"`
	StartedAt      string   `json:"started_at"`
	StageUpdatedAt string   `json:"stage_updated_at"`
	MergedAt       string   `json:"merged_at"`
}

func toEpicDTO(e db.Epic) epicDTO {
	return epicDTO{
		ID: e.ID, ProjectID: e.ProjectID, Issue: e.IssueNumber, Title: e.Title,
		Labels: e.Labels, BlockedBy: e.BlockedBy, Stage: e.Stage, Attempt: e.Attempt,
		Session: e.SessionName, Branch: e.Branch, PR: e.PRNumber, Verdict: e.Verdict,
		Needs: e.Needs, IssueState: e.IssueState, QueuedAt: e.QueuedAt,
		StartedAt: e.StartedAt, StageUpdatedAt: e.StageUpdatedAt, MergedAt: e.MergedAt,
	}
}

type projectDTO struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Repo            string         `json:"repo"`
	ServerID        string         `json:"server_id"`
	Target          string         `json:"target"`
	Workdir         string         `json:"workdir"`
	BaseBranch      string         `json:"base_branch"`
	Provider        string         `json:"provider"`
	RequiredReviews []string       `json:"required_reviews"`
	MaxParallel     int            `json:"max_parallel"`
	Paused          bool           `json:"paused"`
	RequireCI       bool           `json:"require_ci"`
	Counts          map[string]int `json:"counts,omitempty"`
}

func projectOut(p db.Project, counts map[string]int) projectDTO {
	return projectDTO{p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider, p.RequiredReviews, p.MaxParallel, p.Paused, p.RequireCI, counts}
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
			Name            string   `json:"name"`
			Repo            string   `json:"repo"`
			ServerID        string   `json:"server_id"`
			Target          string   `json:"target"`
			Workdir         string   `json:"workdir"`
			BaseBranch      string   `json:"base_branch"`
			Provider        string   `json:"provider"`
			RequiredReviews []string `json:"required_reviews"`
			MaxParallel     int      `json:"max_parallel"`
			RequireCI       bool     `json:"require_ci"`
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
		pr := db.Project{ID: uuid.NewString(), Name: in.Name, Repo: in.Repo, ServerID: in.ServerID, Target: in.Target, Workdir: in.Workdir, BaseBranch: in.BaseBranch, Provider: in.Provider, RequiredReviews: in.RequiredReviews, MaxParallel: in.MaxParallel, RequireCI: in.RequireCI}
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
		dto := make([]epicDTO, 0, len(es))
		events := map[string][]eventDTO{}
		for _, e := range es {
			dto = append(dto, toEpicDTO(e))
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
		case "run_issue":
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
			Name            *string   `json:"name"`
			Repo            *string   `json:"repo"`
			ServerID        *string   `json:"server_id"`
			Workdir         *string   `json:"workdir"`
			Target          *string   `json:"target"`
			BaseBranch      *string   `json:"base_branch"`
			Provider        *string   `json:"provider"`
			RequiredReviews *[]string `json:"required_reviews"`
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
		if in.Workdir != nil {
			pr.Workdir = *in.Workdir
		}
		if in.Target != nil {
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
		if pr.Name == "" || pr.Workdir == "" || pr.BaseBranch == "" {
			writeJSONError(w, http.StatusBadRequest, "missing required field")
			return
		}
		if pr.Provider != "claude" && pr.Provider != "codex" {
			writeJSONError(w, http.StatusBadRequest, "provider must be claude or codex")
			return
		}
		found, err := d.DB.UpdateProject(r.Context(), pr)
		if err != nil {
			// Most likely the UNIQUE(name) constraint; the DB error text is not
			// for browsers.
			writeJSONError(w, http.StatusBadRequest, "update failed (name already in use?)")
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
		projDTOs = append(projDTOs, projectOut(pr, nil))
		es, err := d.DB.ListBoardEpics(ctx, pr.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range es {
			epics = append(epics, toEpicDTO(e))
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
