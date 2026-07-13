package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Project is a registered orchestrator target: a repo bound to a fleet host.
type Project struct {
	ID              string
	Name            string
	Repo            string // "owner/name"
	ServerID        string
	Target          string // tmux socket target on the host ("" = agent default)
	Workdir         string
	BaseBranch      string
	Provider        string // default runner: "claude" | "codex"
	RequiredReviews []string
	MaxParallel     int
	Paused          bool
	RequireCI       bool
	Pinned          bool
}

const projectCols = "id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci, pinned"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var reviews string
	if err := row.Scan(&p.ID, &p.Name, &p.Repo, &p.ServerID, &p.Target, &p.Workdir,
		&p.BaseBranch, &p.Provider, &reviews, &p.MaxParallel, &p.Paused, &p.RequireCI, &p.Pinned); err != nil {
		return Project{}, err
	}
	p.RequiredReviews = unmarshalStrings(reviews)
	return p, nil
}

func (d *DB) CreateProject(ctx context.Context, p Project) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO projects(id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider,
		marshalStrings(p.RequiredReviews), p.MaxParallel, p.Paused, p.RequireCI)
	return err
}

func (d *DB) GetProject(ctx context.Context, id string) (Project, error) {
	return scanProject(d.sql.QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
}

// GetProjectByRepo matches case-insensitively: the repo column is COLLATE
// NOCASE because GitHub slugs are case-insensitive but case-preserving, and
// webhook payloads carry canonical casing that may differ from what the
// admin typed at registration.
func (d *DB) GetProjectByRepo(ctx context.Context, repo string) (Project, error) {
	return scanProject(d.sql.QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM projects WHERE repo = ?`, repo))
}

func (d *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) SetProjectPaused(ctx context.Context, id string, paused bool) (bool, error) {
	return d.execFound(ctx, `UPDATE projects SET paused = ?, updated_at = datetime('now') WHERE id = ?`, paused, id)
}

func (d *DB) SetProjectMaxParallel(ctx context.Context, id string, n int) (bool, error) {
	return d.execFound(ctx, `UPDATE projects SET max_parallel = ?, updated_at = datetime('now') WHERE id = ?`, n, id)
}

func (d *DB) SetProjectRequireCI(ctx context.Context, id string, v bool) (bool, error) {
	return d.execFound(ctx, `UPDATE projects SET require_ci = ?, updated_at = datetime('now') WHERE id = ?`, v, id)
}

func (d *DB) SetProjectPinned(ctx context.Context, id string, v bool) (bool, error) {
	return d.execFound(ctx, `UPDATE projects SET pinned = ?, updated_at = datetime('now') WHERE id = ?`, v, id)
}

// marshalStrings / unmarshalStrings mirror servers.go's label helpers for
// TEXT NOT NULL DEFAULT '[]' columns (plain string, never NULL).
func marshalStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func unmarshalStrings(s string) []string {
	var out []string
	if s == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// ErrDuplicateName is returned by UpdateProject when a rename collides with the
// UNIQUE(name) constraint. The PATCH handler maps it to 400; ANY other error is
// an internal failure (lock/IO/closed DB) that must surface as 500, not a
// misleading "name already in use" 400.
var ErrDuplicateName = errors.New("project name already in use")

// UpdateProject rewrites the editable registration fields (typo repair from the
// board UI). repo and server_id are deliberately NOT updatable: existing epics
// belong to the repo, and moving hosts mid-flight would orphan runner sessions
// (spec §5.3). paused/max_parallel/require_ci keep their action verbs. A
// UNIQUE(name) violation is translated to ErrDuplicateName so the caller can
// tell a client-side collision apart from a genuine backend failure.
func (d *DB) UpdateProject(ctx context.Context, p Project) (bool, error) {
	found, err := d.execFound(ctx,
		`UPDATE projects SET name = ?, workdir = ?, target = ?, base_branch = ?, provider = ?, required_reviews = ?, updated_at = datetime('now') WHERE id = ?`,
		p.Name, p.Workdir, p.Target, p.BaseBranch, p.Provider, marshalStrings(p.RequiredReviews), p.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return false, ErrDuplicateName
	}
	return found, err
}

// DeleteProject removes a project and its (terminal) epics + events in one
// transaction. It refuses while any non-terminal epic exists — the guard runs
// INSIDE the transaction so a concurrent report/transition can't slip an
// active epic past it. foreign_keys(1) is on and there is no ON DELETE
// CASCADE, so children go first.
func (d *DB) DeleteProject(ctx context.Context, id string) (found bool, active int, err error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM epics WHERE project_id = ? AND stage NOT IN `+terminalStagesSQL, id).Scan(&active); err != nil {
		return false, 0, err
	}
	if active > 0 {
		return true, active, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM epic_events WHERE epic_id IN (SELECT id FROM epics WHERE project_id = ?)`, id); err != nil {
		return false, 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM epics WHERE project_id = ?`, id); err != nil {
		return false, 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return false, 0, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return n > 0, 0, nil
}
