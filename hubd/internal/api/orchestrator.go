package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"agentmon/hubd/internal/agentws"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

const maxOrchestratorBody = 16 << 10

type epicDTO struct {
	ID             string   `json:"id"`
	Issue          int      `json:"issue"`
	Title          string   `json:"title"`
	Labels         []string `json:"labels"`
	BlockedBy      []int    `json:"blocked_by"`
	Stage          string   `json:"stage"`
	Attempt        int      `json:"attempt"`
	Session        string   `json:"session"`
	Branch         string   `json:"branch"`
	PR             int      `json:"pr"`
	Needs          string   `json:"needs"`
	IssueState     string   `json:"issue_state"`
	QueuedAt       string   `json:"queued_at"`
	StartedAt      string   `json:"started_at"`
	StageUpdatedAt string   `json:"stage_updated_at"`
	MergedAt       string   `json:"merged_at"`
}

func toEpicDTO(e db.Epic) epicDTO {
	return epicDTO{e.ID, e.IssueNumber, e.Title, e.Labels, e.BlockedBy, e.Stage, e.Attempt, e.SessionName, e.Branch, e.PRNumber, e.Needs, e.IssueState, e.QueuedAt, e.StartedAt, e.StageUpdatedAt, e.MergedAt}
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

func (d Deps) OrchestratorProjectsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*"); !ok {
				return
			}
			ps, err := d.DB.ListProjects(r.Context())
			if err != nil {
				writeJSONError(w, 500, "internal error")
				return
			}
			out := make([]projectDTO, 0, len(ps))
			for _, p := range ps {
				es, _ := d.DB.ListEpicsByProject(r.Context(), p.ID)
				counts := map[string]int{}
				for _, e := range es {
					counts[e.Stage]++
				}
				out = append(out, projectOut(p, counts))
			}
			writeJSON(w, 200, out)
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "orchestrator:*")
		if !ok {
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
			writeJSONError(w, 400, "bad request")
			return
		}
		if in.Name == "" || in.Repo == "" || in.ServerID == "" || in.Workdir == "" {
			writeJSONError(w, 400, "missing required field")
			return
		}
		if in.BaseBranch == "" {
			in.BaseBranch = "main"
		}
		if in.Provider == "" {
			in.Provider = "claude"
		}
		if in.MaxParallel == 0 {
			in.MaxParallel = 1
		}
		pr := db.Project{ID: uuid.NewString(), Name: in.Name, Repo: in.Repo, ServerID: in.ServerID, Target: in.Target, Workdir: in.Workdir, BaseBranch: in.BaseBranch, Provider: in.Provider, RequiredReviews: in.RequiredReviews, MaxParallel: in.MaxParallel, RequireCI: in.RequireCI}
		if err := d.DB.CreateProject(r.Context(), pr); err != nil {
			writeJSONError(w, 400, "create failed")
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectRegister(r.Context(), p.ID, "project:"+pr.ID, pr.Repo, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, 201, projectOut(pr, nil))
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
			writeJSONError(w, 404, "not found")
			return
		}
		es, err := d.DB.ListEpicsByProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		dto := make([]epicDTO, 0, len(es))
		events := map[string][]db.EpicEvent{}
		for _, e := range es {
			dto = append(dto, toEpicDTO(e))
			events[e.ID], _ = d.DB.ListEpicEvents(r.Context(), e.ID, 20)
		}
		writeJSON(w, 200, map[string]any{"project": projectOut(p, nil), "epics": dto, "events": events})
	}
}

func (d Deps) OrchestratorActionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		var in struct {
			Action string `json:"action"`
			EpicID string `json:"epic_id"`
			Issue  int    `json:"issue"`
			Value  int    `json:"value"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrchestratorBody)).Decode(&in); err != nil {
			writeJSONError(w, 400, "bad request")
			return
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
		case "pause":
			_, err = d.DB.SetProjectPaused(r.Context(), id, true)
			if err == nil {
				d.Orch.Wake()
			}
		case "resume":
			_, err = d.DB.SetProjectPaused(r.Context(), id, false)
			if err == nil {
				d.Orch.Wake()
			}
		case "set_max_parallel":
			_, err = d.DB.SetProjectMaxParallel(r.Context(), id, in.Value)
			if err == nil {
				d.Orch.Wake()
			}
		case "run_issue":
			err = d.Orch.RunIssue(r.Context(), id, in.Issue)
		case "guidance":
			e, eerr := d.DB.GetEpic(r.Context(), in.EpicID)
			if eerr != nil {
				err = eerr
				break
			}
			project, eerr := d.DB.GetProject(r.Context(), e.ProjectID)
			if eerr != nil {
				err = eerr
				break
			}
			srv, found, eerr := d.Reg.Get(r.Context(), project.ServerID)
			if eerr != nil || !found {
				if eerr != nil {
					err = eerr
				} else {
					err = http.ErrMissingFile
				}
				break
			}
			sessions, eerr := d.Agent.Sessions(r.Context(), srv, project.Target)
			if eerr != nil {
				err = eerr
				break
			}
			err = agentws.SendText(r.Context(), srv, &d.Minter, p.ID, project.Target, e.SessionName, in.Text+"\n", sessions)
		default:
			writeJSONError(w, 400, "unknown action")
			return
		}
		if err != nil {
			writeJSONError(w, 400, err.Error())
			return
		}
		if d.Audit != nil {
			d.Audit.EpicAction(r.Context(), p.ID, "project:"+id, in.Action, in.EpicID, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}
