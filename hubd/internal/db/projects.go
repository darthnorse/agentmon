package db

import (
	"context"
	"encoding/json"
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
}

const projectCols = "id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var reviews string
	if err := row.Scan(&p.ID, &p.Name, &p.Repo, &p.ServerID, &p.Target, &p.Workdir,
		&p.BaseBranch, &p.Provider, &reviews, &p.MaxParallel, &p.Paused, &p.RequireCI); err != nil {
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
