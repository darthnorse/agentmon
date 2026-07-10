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
}

const projectCols = "id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var reviews string
	var paused int
	if err := row.Scan(&p.ID, &p.Name, &p.Repo, &p.ServerID, &p.Target, &p.Workdir,
		&p.BaseBranch, &p.Provider, &reviews, &p.MaxParallel, &paused); err != nil {
		return Project{}, err
	}
	p.RequiredReviews = unmarshalStrings(reviews)
	p.Paused = paused != 0
	return p, nil
}

func (d *DB) CreateProject(ctx context.Context, p Project) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO projects(id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider,
		marshalStrings(p.RequiredReviews), p.MaxParallel, boolToInt(p.Paused))
	return err
}

func (d *DB) GetProject(ctx context.Context, id string) (Project, error) {
	return scanProject(d.sql.QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
}

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
	res, err := d.sql.ExecContext(ctx,
		`UPDATE projects SET paused = ?, updated_at = datetime('now') WHERE id = ?`,
		boolToInt(paused), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) SetProjectMaxParallel(ctx context.Context, id string, n int) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE projects SET max_parallel = ?, updated_at = datetime('now') WHERE id = ?`,
		n, id)
	if err != nil {
		return false, err
	}
	rn, _ := res.RowsAffected()
	return rn > 0, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// marshalStrings / unmarshalStrings mirror servers.go's label helpers for
// TEXT NOT NULL DEFAULT '[]' columns (plain string, never NULL).
func marshalStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "[]"
	}
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
