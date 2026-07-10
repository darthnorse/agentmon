package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
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
 queued_at, started_at, stage_updated_at, merged_at`

func scanEpic(row interface{ Scan(...any) error }) (Epic, error) {
	var e Epic
	var labels, blocked string
	if err := row.Scan(&e.ID, &e.ProjectID, &e.IssueNumber, &e.Title, &labels, &blocked,
		&e.Stage, &e.Attempt, &e.SessionName, &e.Branch, &e.PRNumber, &e.Verdict, &e.Needs,
		&e.IssueState, &e.QueuedAt, &e.StartedAt, &e.StageUpdatedAt, &e.MergedAt); err != nil {
		return Epic{}, err
	}
	e.Labels = unmarshalStrings(labels)
	e.BlockedBy = unmarshalInts(blocked)
	return e, nil
}

func (d *DB) UpsertEpicIssue(ctx context.Context, e Epic) (Epic, error) {
	existing, err := d.GetEpicByIssue(ctx, e.ProjectID, e.IssueNumber)
	if err == nil {
		_, uerr := d.sql.ExecContext(ctx,
			`UPDATE epics SET title=?, labels=?, blocked_by=?, issue_state=?, updated_at=datetime('now') WHERE id=?`,
			e.Title, marshalStrings(e.Labels), marshalInts(e.BlockedBy), e.IssueState, existing.ID)
		if uerr != nil {
			return Epic{}, uerr
		}
		return d.GetEpic(ctx, existing.ID)
	}
	if err != sql.ErrNoRows {
		return Epic{}, err
	}
	id := uuid.NewString()
	_, err = d.sql.ExecContext(ctx,
		`INSERT INTO epics(id, project_id, issue_number, title, labels, blocked_by, issue_state,
		   queued_at, stage_updated_at, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		id, e.ProjectID, e.IssueNumber, e.Title, marshalStrings(e.Labels),
		marshalInts(e.BlockedBy), e.IssueState, e.QueuedAt, e.StageUpdatedAt)
	if err != nil {
		return Epic{}, err
	}
	return d.GetEpic(ctx, id)
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
		`SELECT `+epicCols+` FROM epics WHERE stage NOT IN ('merged','failed','canceled') ORDER BY issue_number`)
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

// TransitionEpic performs the guarded stage move. Two statements, no tx: the
// DB is single-writer (SetMaxOpenConns(1)), and a lost event row after a crash
// is tolerable — stage is authoritative, events are the narrative.
func (d *DB) TransitionEpic(ctx context.Context, id, from, to, source, note, now string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE epics SET stage=?, stage_updated_at=?,
		   started_at = CASE WHEN ?='starting' AND started_at='' THEN ? ELSE started_at END,
		   merged_at  = CASE WHEN ?='merged' THEN ? ELSE merged_at END,
		   needs      = CASE WHEN ? IN ('escalated','stalled') THEN needs ELSE '' END,
		   updated_at = datetime('now')
		 WHERE id = ? AND stage = ?`,
		to, now, to, now, to, now, to, id, from)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, d.AppendEpicEvent(ctx, EpicEvent{
		EpicID: id, FromStage: from, ToStage: to, Source: source, Note: note, Ts: now,
	})
}

func (d *DB) SetEpicAssignment(ctx context.Context, id, session string, attempt int) (bool, error) {
	return d.epicUpdate(ctx, `UPDATE epics SET session_name=?, attempt=?, updated_at=datetime('now') WHERE id=?`, session, attempt, id)
}

func (d *DB) SetEpicPR(ctx context.Context, id string, pr int, branch string) (bool, error) {
	return d.epicUpdate(ctx, `UPDATE epics SET pr_number=?, branch=?, updated_at=datetime('now') WHERE id=?`, pr, branch, id)
}

func (d *DB) SetEpicVerdict(ctx context.Context, id, verdictJSON string) (bool, error) {
	return d.epicUpdate(ctx, `UPDATE epics SET verdict=?, updated_at=datetime('now') WHERE id=?`, verdictJSON, id)
}

func (d *DB) SetEpicNeeds(ctx context.Context, id, needs string) (bool, error) {
	return d.epicUpdate(ctx, `UPDATE epics SET needs=?, updated_at=datetime('now') WHERE id=?`, needs, id)
}

func (d *DB) epicUpdate(ctx context.Context, q string, args ...any) (bool, error) {
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
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, epic_id, from_stage, to_stage, source, note, ts
		 FROM epic_events WHERE epic_id = ? ORDER BY ts DESC, id LIMIT ?`, epicID, limit)
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
	b, err := json.Marshal(ns)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalInts(s string) []int {
	var out []int
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		// tolerate a legacy comma list, e.g. "12,13"
		for _, part := range splitNonEmpty(s, ',') {
			if n, err := strconv.Atoi(part); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func splitNonEmpty(s string, sep rune) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == sep {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		if r != ' ' && r != '[' && r != ']' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
