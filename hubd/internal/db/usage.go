package db

import (
	"context"

	"github.com/google/uuid"
)

// UsageRow is one cumulative per-(provider,model) token snapshot for an epic
// attempt at a given pipeline stage. project_name/repo are denormalized at
// write time so the ledger reads back without joining epics/projects (which
// may have since changed or been deleted) — this table is a durable audit
// trail, not a live mirror.
type UsageRow struct {
	ProjectID   string
	ProjectName string
	Repo        string
	IssueNumber int
	Attempt     int
	Stage       string
	CapturedAt  string
	Provider    string
	Model       string
	Input       int64
	Output      int64
	CacheRead   int64
	CacheWrite  int64
}

const usageCols = `project_id, project_name, repo, issue_number, attempt, stage, captured_at,
 provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens`

func scanUsage(row interface{ Scan(...any) error }) (UsageRow, error) {
	var u UsageRow
	if err := row.Scan(&u.ProjectID, &u.ProjectName, &u.Repo, &u.IssueNumber, &u.Attempt, &u.Stage, &u.CapturedAt,
		&u.Provider, &u.Model, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite); err != nil {
		return UsageRow{}, err
	}
	return u, nil
}

// UpsertUsage records one token snapshot, idempotent on
// UNIQUE(project_id, issue_number, attempt, stage, captured_at, provider, model):
// a redelivered/corrected report with the same key merges into the existing
// row rather than duplicating it. The merge takes the per-column MAX (not a
// plain overwrite): a genuine redelivery carries identical values (no-op), a
// corrected/higher report legitimately grows them, but a lower-valued
// collision — reachable when a best-effort reap lands on the same key a
// report already claimed (same second + stage + provider + model, e.g. a
// retry/cancel reaping an epic in a stage it just reported) — must never
// erase the higher value already stored. Cumulative token counts are
// monotonic within an attempt, so the true value can only ever be the max of
// what's been seen for that key.
func (d *DB) UpsertUsage(ctx context.Context, row UsageRow) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO epic_usage(id, `+usageCols+`)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(project_id, issue_number, attempt, stage, captured_at, provider, model) DO UPDATE SET
		   input_tokens=MAX(input_tokens, excluded.input_tokens), output_tokens=MAX(output_tokens, excluded.output_tokens),
		   cache_read_tokens=MAX(cache_read_tokens, excluded.cache_read_tokens), cache_write_tokens=MAX(cache_write_tokens, excluded.cache_write_tokens)`,
		uuid.NewString(), row.ProjectID, row.ProjectName, row.Repo, row.IssueNumber, row.Attempt, row.Stage, row.CapturedAt,
		row.Provider, row.Model, row.Input, row.Output, row.CacheRead, row.CacheWrite)
	return err
}

// ListEpicUsage returns every token snapshot recorded for one epic, in the
// order they accrued. The final `rowid` tie-break is SQLite's implicit
// monotonic insertion order (epic_usage has a TEXT PRIMARY KEY, not
// WITHOUT ROWID, so rowid always exists) — it disambiguates two rows that
// share a captured_at second (e.g. a stage report and a reap landing in the
// same second): the row inserted later (a reap, always last) sorts last
// within that second, and same-second same-stage/different-stage reports
// keep a deterministic, reproducible order instead of one that depends on
// SQLite's unspecified tie order. usage_derive.go's buildBoundaries relies
// on this ordering to sequence same-second boundaries correctly.
func (d *DB) ListEpicUsage(ctx context.Context, projectID string, issue int) ([]UsageRow, error) {
	return d.listUsage(ctx,
		`SELECT `+usageCols+` FROM epic_usage WHERE project_id = ? AND issue_number = ? ORDER BY attempt, captured_at, rowid`,
		projectID, issue)
}

// ListProjectUsage returns every token snapshot recorded for a project across
// all its epics, grouped by epic via the ORDER BY. See ListEpicUsage's
// doc comment for why the trailing `rowid` tie-break matters.
func (d *DB) ListProjectUsage(ctx context.Context, projectID string) ([]UsageRow, error) {
	return d.listUsage(ctx,
		`SELECT `+usageCols+` FROM epic_usage WHERE project_id = ? ORDER BY issue_number, attempt, captured_at, rowid`,
		projectID)
}

func (d *DB) listUsage(ctx context.Context, q string, args ...any) ([]UsageRow, error) {
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
