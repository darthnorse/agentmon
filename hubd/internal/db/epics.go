package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"agentmon/shared"
)

// Epic is the runtime row for one orchestrated issue. GitHub owns the
// definition (title/labels/deps mirror); the hub owns stage + runtime fields.
type Epic struct {
	ID             string
	ProjectID      string
	IssueNumber    int
	Title          string
	Labels         []string
	BlockedBy      []int
	Stage          string
	Attempt        int
	PRNumber       int
	SessionName    string
	Branch         string
	Verdict        string // raw JSON of the parsed verdict, "" until pr_open
	ApprovedSHA    string // head SHA the gate/human approved; merge retries pin to it
	Needs          string // human-readable needs-attention reason
	IssueState     string // "open" | "closed"
	QueuedAt       string
	StartedAt      string
	StageUpdatedAt string
	MergedAt       string
}

type EpicEvent struct {
	ID        string
	EpicID    string
	FromStage string
	ToStage   string
	Source    string // report | github | hub | user
	Note      string
	Ts        string
}

const epicCols = `id, project_id, issue_number, title, labels, blocked_by, stage, attempt,
 session_name, branch, pr_number, verdict, needs, issue_state,
 queued_at, started_at, stage_updated_at, merged_at, approved_sha`

// SQL fragments are built from the shared stage constants so the db layer
// cannot silently drift when a stage is added or renamed. Values are trusted
// constants, never user input.
var (
	terminalStagesSQL = fmt.Sprintf("('%s','%s','%s')",
		shared.EpicMerged, shared.EpicFailed, shared.EpicCanceled)
	// needs is set from the transition note when ENTERING escalated/stalled
	// and cleared otherwise — atomically under the same stage guard, so a
	// lost transition race can never strand a stale needs text.
	transitionSQL = fmt.Sprintf(`UPDATE epics SET stage=?, stage_updated_at=?,
	   started_at = CASE WHEN ?='%s' AND started_at='' THEN ? ELSE started_at END,
	   merged_at  = CASE WHEN ?='%s' THEN ? ELSE merged_at END,
	   needs      = CASE WHEN ? IN ('%s','%s') THEN ? ELSE '' END,
	   updated_at = datetime('now')
	 WHERE id = ? AND stage = ?`,
		shared.EpicStarting, shared.EpicMerged, shared.EpicEscalated, shared.EpicStalled)
)

func scanEpic(row interface{ Scan(...any) error }) (Epic, error) {
	var e Epic
	var labels, blocked string
	if err := row.Scan(&e.ID, &e.ProjectID, &e.IssueNumber, &e.Title, &labels, &blocked,
		&e.Stage, &e.Attempt, &e.SessionName, &e.Branch, &e.PRNumber, &e.Verdict, &e.Needs,
		&e.IssueState, &e.QueuedAt, &e.StartedAt, &e.StageUpdatedAt, &e.MergedAt, &e.ApprovedSHA); err != nil {
		return Epic{}, err
	}
	e.Labels = unmarshalStrings(labels)
	e.BlockedBy = unmarshalInts(blocked)
	return e, nil
}

// UpsertEpicIssue inserts a new epic (stage 'queued') or refreshes only the
// GitHub-mirror fields of an existing one. Single statement: the webhook
// handler and the sync poller may race on the same issue, and a
// check-then-insert pair would lose to UNIQUE(project_id, issue_number).
func (d *DB) UpsertEpicIssue(ctx context.Context, e Epic) (Epic, error) {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO epics(id, project_id, issue_number, title, labels, blocked_by, issue_state,
		   queued_at, stage_updated_at, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))
		 ON CONFLICT(project_id, issue_number) DO UPDATE SET
		   title=excluded.title, labels=excluded.labels, blocked_by=excluded.blocked_by,
		   issue_state=excluded.issue_state, updated_at=datetime('now')`,
		uuid.NewString(), e.ProjectID, e.IssueNumber, e.Title, marshalStrings(e.Labels),
		marshalInts(e.BlockedBy), e.IssueState, e.QueuedAt, e.StageUpdatedAt)
	if err != nil {
		return Epic{}, err
	}
	return d.GetEpicByIssue(ctx, e.ProjectID, e.IssueNumber)
}

func (d *DB) GetEpic(ctx context.Context, id string) (Epic, error) {
	return scanEpic(d.sql.QueryRowContext(ctx,
		`SELECT `+epicCols+` FROM epics WHERE id = ?`, id))
}

func (d *DB) GetEpicByIssue(ctx context.Context, projectID string, issue int) (Epic, error) {
	return scanEpic(d.sql.QueryRowContext(ctx,
		`SELECT `+epicCols+` FROM epics WHERE project_id = ? AND issue_number = ?`, projectID, issue))
}

func (d *DB) ListEpicsByProject(ctx context.Context, projectID string) ([]Epic, error) {
	return d.listEpics(ctx, `SELECT `+epicCols+` FROM epics WHERE project_id = ? ORDER BY issue_number`, projectID)
}

func (d *DB) ListNonTerminalEpics(ctx context.Context) ([]Epic, error) {
	return d.listEpics(ctx,
		`SELECT `+epicCols+` FROM epics WHERE stage NOT IN `+terminalStagesSQL+` ORDER BY issue_number`)
}

func (d *DB) listEpics(ctx context.Context, q string, args ...any) ([]Epic, error) {
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Epic
	for rows.Next() {
		e, err := scanEpic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TransitionEpic performs the guarded stage move. The returned bool ALONE
// reports whether the stage moved; a failed epic_events append is logged and
// swallowed (two statements, no tx — the DB is single-writer and the design
// treats events as narrative, stage as authoritative). The note doubles as
// the needs-attention text when entering escalated/stalled.
func (d *DB) TransitionEpic(ctx context.Context, id, from, to, source, note, now string) (bool, error) {
	res, err := d.sql.ExecContext(ctx, transitionSQL,
		to, now, to, now, to, now, to, note, id, from)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if err := d.AppendEpicEvent(ctx, EpicEvent{
		EpicID: id, FromStage: from, ToStage: to, Source: source, Note: note, Ts: now,
	}); err != nil {
		log.Printf("db: epic %s event append %s→%s failed (stage IS committed): %v", id, from, to, err)
	}
	return true, nil
}

func (d *DB) SetEpicAssignment(ctx context.Context, id, session string, attempt int) (bool, error) {
	return d.execFound(ctx, `UPDATE epics SET session_name=?, attempt=?, updated_at=datetime('now') WHERE id=?`, session, attempt, id)
}

func (d *DB) SetEpicPR(ctx context.Context, id string, pr int, branch string) (bool, error) {
	return d.execFound(ctx, `UPDATE epics SET pr_number=?, branch=?, updated_at=datetime('now') WHERE id=?`, pr, branch, id)
}

func (d *DB) SetEpicVerdict(ctx context.Context, id, verdictJSON string) (bool, error) {
	return d.execFound(ctx, `UPDATE epics SET verdict=?, updated_at=datetime('now') WHERE id=?`, verdictJSON, id)
}

func (d *DB) SetEpicApprovedSHA(ctx context.Context, id, sha string) (bool, error) {
	return d.execFound(ctx, `UPDATE epics SET approved_sha=?, updated_at=datetime('now') WHERE id=?`, sha, id)
}

func (d *DB) SetEpicNeeds(ctx context.Context, id, needs string) (bool, error) {
	return d.execFound(ctx, `UPDATE epics SET needs=?, updated_at=datetime('now') WHERE id=?`, needs, id)
}

// execFound runs a mutation and reports whether any row matched.
func (d *DB) execFound(ctx context.Context, q string, args ...any) (bool, error) {
	res, err := d.sql.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) AppendEpicEvent(ctx context.Context, ev EpicEvent) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO epic_events(id, epic_id, from_stage, to_stage, source, note, ts)
		 VALUES(?,?,?,?,?,?,?)`,
		ev.ID, ev.EpicID, ev.FromStage, ev.ToStage, ev.Source, ev.Note, ev.Ts)
	return err
}

func (d *DB) ListEpicEvents(ctx context.Context, epicID string, limit int) ([]EpicEvent, error) {
	// rowid tie-break: ts is second-resolution RFC3339, and one tick can emit
	// several transitions in the same second; insertion order is the truth.
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, epic_id, from_stage, to_stage, source, note, ts
		 FROM epic_events WHERE epic_id = ? ORDER BY ts DESC, rowid DESC LIMIT ?`, epicID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpicEvent
	for rows.Next() {
		var ev EpicEvent
		if err := rows.Scan(&ev.ID, &ev.EpicID, &ev.FromStage, &ev.ToStage, &ev.Source, &ev.Note, &ev.Ts); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func marshalInts(ns []int) string {
	if len(ns) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ns)
	return string(b)
}

func unmarshalInts(s string) []int {
	var out []int
	if s == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
