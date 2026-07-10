# Orchestrator Hub Core + GitHub Sync — Implementation Plan (Sub-project 1 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the orchestrator brain to hubd — projects/epics runtime state, GitHub issue mirror + webhooks, per-epic state machine, dependency-aware scheduler, fail-closed merge gate, board broadcaster + SSE, actions API — so GitHub epics flow plan→implement→review→PR→merge with escalation to a human.

**Architecture:** New Go package `hubd/internal/orchestrator` (state machine, scheduler, gate, sync, run loop) + `hubd/internal/github` (minimal REST client + webhook parsing) + new stores on `*db.DB` + new API handlers. Spawn rides the existing hub→agent `CreateSession` (with `Command`); reports are **pulled** from agents (hub is always the client); board deltas fan out via a second broadcaster mirroring `state.Broadcaster`. Spec: `docs/superpowers/specs/2026-07-10-agentmon-orchestrator-design.md`.

**Tech Stack:** Go 1.26, stdlib `net/http` + `http.ServeMux`, `modernc.org/sqlite` via `database/sql`, `gopkg.in/yaml.v3` (already a dep) for verdict parsing, `gorilla/websocket` (already a dep) for send-text. **No new module dependencies.**

## Global Constraints

- Module root: `/root/agentmon/hubd` (workspace `go.work` at repo root). Run all commands from `/root/agentmon/hubd` unless stated.
- SQLite via `db.Open` pattern: single writer (`SetMaxOpenConns(1)`), migrations = embedded `.sql` files in `hubd/internal/db/migrations/`, lexical order; next free slot is `0005_`.
- Store methods hang directly on `*db.DB`: `func (d *DB) Verb(ctx context.Context, …) (…, error)`; raw SQL, `?` placeholders; `datetime('now')` for created/updated; caller-stamped RFC3339 strings for domain timestamps; guarded writes return `(bool, error)` via `RowsAffected`; JSON array columns via marshal helpers (see `servers.go:25-40`).
- DB tests: white-box `package db`, real file in `t.TempDir()` via the existing `openTestDB(t)` helper (`servers_test.go:10`).
- Handlers: `authorizeOr403` first, `http.MaxBytesReader` before decode, `writeJSON` / `writeJSONError` (`{"error":"…"}`), routes in `router.go` as `mux.Handle("METHOD /path", rd.Auth.RequireAuth(…))`; POST gets CSRF automatically from `RequireAuth`. Webhook route is public — HMAC instead.
- Audit: add typed methods on `audit.Recorder` mirroring `SessionKill` (`audit.go:71`). Never fail a request on audit error.
- Config: YAML-only via `config.Load`; defaults set in `Load` after unmarshal (the `SessionCookie.Name` pattern).
- Stage timestamps and all times: RFC3339 UTC strings, injected `func() string` clock (test-friendly, matches `push_dispatcher.go` `NowRFC3339`).
- Commit style: `feat(hub): …` / `test(hub): …`; **never add a Co-Authored-By trailer**.
- The merge gate FAILS CLOSED: missing/malformed verdict, unknown reporter session, unparseable CI ⇒ escalate, never merge.
- Verify the whole module still builds before every commit: `go build ./... && go test ./...`.
- **Checkpoint stops:** after committing Task 5, Task 11, and Task 15, STOP execution and report the checkpoint (tasks completed, suite status). An external cross-provider review of the branch happens at each checkpoint. Do NOT begin Task 6, 12, or 16 until you receive explicit fix instructions or an explicit "continue".

## Shared type registry (single source of truth for cross-task names)

| Name | Defined in | Shape |
|---|---|---|
| `shared.EpicStage` | Task 5 | `string`; consts `EpicQueued/EpicStarting/EpicPlanning/EpicImplementing/EpicReviewing/EpicPROpen/EpicMerging/EpicMerged/EpicEscalated/EpicStalled/EpicFailed/EpicCanceled` |
| `shared.OrchestratorReport` | Task 5 | `{Repo string; Epic int; Stage EpicStage; Note string; PR int; Session string; Ts string}` |
| `db.Project` | Task 3 | `{ID, Name, Repo, ServerID, Target, Workdir, BaseBranch, Provider string; RequiredReviews []string; MaxParallel int; Paused bool}` |
| `db.Epic` | Task 4 | `{ID, ProjectID string; IssueNumber int; Title string; Labels []string; BlockedBy []int; Stage string; Attempt, PRNumber int; SessionName, Branch, Verdict, Needs, IssueState, QueuedAt, StartedAt, StageUpdatedAt, MergedAt string}` |
| `db.EpicEvent` | Task 4 | `{ID, EpicID, FromStage, ToStage, Source, Note, Ts string}` |
| `github.Issue` | Task 6 | `{Number int; Title, Body, State string; Labels []string; UpdatedAt string}` |
| `github.PullRequest` | Task 6 | `{Number int; State string; Merged bool; Body, HeadSHA, HeadRef string}` |
| `github.Client` | Task 6 | `NewClient(token string) *Client`; field `Base string` overridable in tests |
| `github.Event` | Task 7 | `{Kind, Action, Repo string; Issue *Issue; PRNumber int; PRMerged bool}` |
| `orchestrator.Verdict` | Task 8 | see task; `ParseVerdict(prBody string) (*Verdict, error)`, `ErrNoVerdict` |
| `orchestrator.GateInput/GateResult` | Task 9 | `Decide(GateInput) GateResult` |
| `orchestrator.ValidTransition` | Task 10 | `func(from, to shared.EpicStage) bool` |
| `orchestrator.BoardChange/BoardBroadcaster` | Task 11 | `{ProjectID, EpicID string; Issue int; Stage shared.EpicStage; Needs, Title string}` |
| `orchestrator.ReadyEpics` | Task 12 | `func(epics []db.Epic, maxParallel int, paused bool) []db.Epic` |
| `orchestrator.Orchestrator` | Task 15 | `New(Deps) *Orchestrator`, `Run(ctx)`, `Wake()`, action methods (Task 18 consumes) |

---

### Task 1: Config — GitHub + orchestrator settings

**Files:**
- Modify: `hubd/internal/config/config.go`
- Test: `hubd/internal/config/config_test.go` (append — file exists on main; keep all pre-existing tests unchanged)

**Interfaces:**
- Consumes: nothing new.
- Produces: `config.GitHubCfg{Token, WebhookSecret string}`, `config.OrchestratorCfg{Tick, PlanningTimeout, ImplementingTimeout, ReviewingTimeout time.Duration; MaxAttempts int}`, fields `Config.GitHub GitHubCfg` and `Config.Orchestrator OrchestratorCfg` with defaults applied in `Load`.

- [x] **Step 1: Write the failing test**

Append to the existing `hubd/internal/config/config_test.go` (preserve all pre-existing tests):

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadGitHubAndOrchestrator(t *testing.T) {
	c, err := Load(writeCfg(t, `
listen: ":8080"
github:
  token: ghp_x
  webhook_secret: whsec
orchestrator:
  tick: 5s
  max_attempts: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.GitHub.Token != "ghp_x" || c.GitHub.WebhookSecret != "whsec" {
		t.Fatalf("github cfg = %+v", c.GitHub)
	}
	if c.Orchestrator.Tick != 5*time.Second || c.Orchestrator.MaxAttempts != 3 {
		t.Fatalf("orchestrator cfg = %+v", c.Orchestrator)
	}
}

func TestOrchestratorDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, `listen: ":8080"`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Orchestrator.Tick != 15*time.Second {
		t.Fatalf("default tick = %v, want 15s", c.Orchestrator.Tick)
	}
	if c.Orchestrator.PlanningTimeout != 2*time.Hour ||
		c.Orchestrator.ImplementingTimeout != 8*time.Hour ||
		c.Orchestrator.ReviewingTimeout != 2*time.Hour {
		t.Fatalf("default timeouts = %+v", c.Orchestrator)
	}
	if c.Orchestrator.MaxAttempts != 2 {
		t.Fatalf("default max_attempts = %d, want 2", c.Orchestrator.MaxAttempts)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/config/ -v`
Expected: FAIL — `c.GitHub undefined` (compile error).

- [x] **Step 3: Implement**

In `hubd/internal/config/config.go`, add next to `CookieCfg`/`RateLimitCfg`:

```go
// GitHubCfg holds the hub-side GitHub credentials for the orchestrator.
// Token is a fine-grained PAT scoped to the registered repos only.
type GitHubCfg struct {
	Token         string `yaml:"token"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// OrchestratorCfg tunes the epic pipeline engine.
type OrchestratorCfg struct {
	Tick                time.Duration `yaml:"tick"`
	PlanningTimeout     time.Duration `yaml:"planning_timeout"`
	ImplementingTimeout time.Duration `yaml:"implementing_timeout"`
	ReviewingTimeout    time.Duration `yaml:"reviewing_timeout"`
	MaxAttempts         int           `yaml:"max_attempts"`
}
```

Add to `Config`:

```go
	GitHub       GitHubCfg       `yaml:"github"`
	Orchestrator OrchestratorCfg `yaml:"orchestrator"`
```

In `Load`, after the existing `SessionCookie.Name` default block:

```go
	if c.Orchestrator.Tick == 0 {
		c.Orchestrator.Tick = 15 * time.Second
	}
	if c.Orchestrator.PlanningTimeout == 0 {
		c.Orchestrator.PlanningTimeout = 2 * time.Hour
	}
	if c.Orchestrator.ImplementingTimeout == 0 {
		c.Orchestrator.ImplementingTimeout = 8 * time.Hour
	}
	if c.Orchestrator.ReviewingTimeout == 0 {
		c.Orchestrator.ReviewingTimeout = 2 * time.Hour
	}
	if c.Orchestrator.MaxAttempts == 0 {
		c.Orchestrator.MaxAttempts = 2
	}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/config/ -v`
Expected: PASS (both tests).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/config/ && git commit -m "feat(hub): github + orchestrator config sections"
```

---

### Task 2: Migration 0005 — projects, epics, epic_events

**Files:**
- Create: `hubd/internal/db/migrations/0005_orchestrator.sql`
- Modify: `hubd/internal/db/db_test.go` (table want-list)

**Interfaces:**
- Produces: tables `projects`, `epics`, `epic_events` (schema below — later tasks' SQL must match column names exactly).

- [x] **Step 1: Extend the schema test to fail**

In `hubd/internal/db/db_test.go`, add `"projects", "epics", "epic_events"` to the `want` slice of table names in the existing schema test.

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run TestOpen -v`
Expected: FAIL — missing table `projects`.

- [x] **Step 3: Create the migration**

Create `hubd/internal/db/migrations/0005_orchestrator.sql`:

```sql
CREATE TABLE projects (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  repo             TEXT NOT NULL UNIQUE,
  server_id        TEXT NOT NULL REFERENCES servers(id),
  target           TEXT NOT NULL DEFAULT '',
  workdir          TEXT NOT NULL,
  base_branch      TEXT NOT NULL DEFAULT 'main',
  provider         TEXT NOT NULL DEFAULT 'claude',
  required_reviews TEXT NOT NULL DEFAULT '[]',
  max_parallel     INTEGER NOT NULL DEFAULT 1,
  paused           INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);

CREATE TABLE epics (
  id               TEXT PRIMARY KEY,
  project_id       TEXT NOT NULL REFERENCES projects(id),
  issue_number     INTEGER NOT NULL,
  title            TEXT NOT NULL DEFAULT '',
  labels           TEXT NOT NULL DEFAULT '[]',
  blocked_by       TEXT NOT NULL DEFAULT '[]',
  stage            TEXT NOT NULL DEFAULT 'queued',
  attempt          INTEGER NOT NULL DEFAULT 0,
  session_name     TEXT NOT NULL DEFAULT '',
  branch           TEXT NOT NULL DEFAULT '',
  pr_number        INTEGER NOT NULL DEFAULT 0,
  verdict          TEXT NOT NULL DEFAULT '',
  needs            TEXT NOT NULL DEFAULT '',
  issue_state      TEXT NOT NULL DEFAULT 'open',
  queued_at        TEXT NOT NULL,
  started_at       TEXT NOT NULL DEFAULT '',
  stage_updated_at TEXT NOT NULL,
  merged_at        TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE(project_id, issue_number)
);
CREATE INDEX idx_epics_project_stage ON epics(project_id, stage);

CREATE TABLE epic_events (
  id         TEXT PRIMARY KEY,
  epic_id    TEXT NOT NULL REFERENCES epics(id),
  from_stage TEXT NOT NULL,
  to_stage   TEXT NOT NULL,
  source     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  ts         TEXT NOT NULL
);
CREATE INDEX idx_epic_events_epic ON epic_events(epic_id, ts);
```

Note: string timestamps use `''` (not NULL) for "unset" — matches the store code's plain-`string` fields and avoids `sql.NullString` scanning.

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -v`
Expected: PASS (all existing + schema test).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/db/ && git commit -m "feat(hub): 0005 orchestrator schema (projects, epics, epic_events)"
```

---

### Task 3: DB store — projects

**Files:**
- Create: `hubd/internal/db/projects.go`
- Test: `hubd/internal/db/projects_test.go`

**Interfaces:**
- Consumes: tables from Task 2; `openTestDB` helper.
- Produces:
  - `type Project struct { ID, Name, Repo, ServerID, Target, Workdir, BaseBranch, Provider string; RequiredReviews []string; MaxParallel int; Paused bool }`
  - `func (d *DB) CreateProject(ctx context.Context, p Project) error`
  - `func (d *DB) GetProject(ctx context.Context, id string) (Project, error)` — `sql.ErrNoRows` when missing
  - `func (d *DB) GetProjectByRepo(ctx context.Context, repo string) (Project, error)`
  - `func (d *DB) ListProjects(ctx context.Context) ([]Project, error)`
  - `func (d *DB) SetProjectPaused(ctx context.Context, id string, paused bool) (bool, error)`
  - `func (d *DB) SetProjectMaxParallel(ctx context.Context, id string, n int) (bool, error)`

- [x] **Step 1: Write the failing test**

Create `hubd/internal/db/projects_test.go`:

```go
package db

import (
	"context"
	"testing"
)

// NOTE: the package already provides enrollTestServer(t, d, id) in
// state_test.go (same signature/behavior — enrolls an active server
// satisfying the projects/epics FK). REUSE it; do not redeclare.

func testProject(server string) Project {
	return Project{
		ID: "p1", Name: "school-platform", Repo: "darthnorse/school-platform",
		ServerID: server, Target: "", Workdir: "/srv/school-platform",
		BaseBranch: "main", Provider: "claude",
		RequiredReviews: []string{"specialist", "codex"}, MaxParallel: 1,
	}
}

func TestProjectRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "darthnorse/school-platform" || got.MaxParallel != 1 || got.Paused {
		t.Fatalf("got %+v", got)
	}
	if len(got.RequiredReviews) != 2 || got.RequiredReviews[1] != "codex" {
		t.Fatalf("required reviews = %v", got.RequiredReviews)
	}
	byRepo, err := d.GetProjectByRepo(ctx, "darthnorse/school-platform")
	if err != nil || byRepo.ID != "p1" {
		t.Fatalf("byRepo = %+v err=%v", byRepo, err)
	}
	list, err := d.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
}

func TestProjectSetters(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.SetProjectPaused(ctx, "p1", true); err != nil || !ok {
		t.Fatalf("pause: ok=%v err=%v", ok, err)
	}
	if ok, err := d.SetProjectMaxParallel(ctx, "p1", 3); err != nil || !ok {
		t.Fatalf("maxpar: ok=%v err=%v", ok, err)
	}
	got, _ := d.GetProject(ctx, "p1")
	if !got.Paused || got.MaxParallel != 3 {
		t.Fatalf("got %+v", got)
	}
	if ok, _ := d.SetProjectPaused(ctx, "nope", true); ok {
		t.Fatal("pause on missing id should report false")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run TestProject -v`
Expected: FAIL — `d.CreateProject undefined` (compile error).

- [x] **Step 3: Implement**

Create `hubd/internal/db/projects.go`:

```go
package db

import "context"

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
```

Also add the two string-slice helpers used above (and by Task 4) at the bottom of `projects.go` — same encoding as `marshalLabels`/`unmarshalLabels` in `servers.go` but returning `''`-safe plain strings for `TEXT NOT NULL` columns:

```go
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
```

(Add `"encoding/json"` to imports.)

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run TestProject -v && go build ./...`
Expected: PASS, clean build.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/db/ && git commit -m "feat(hub): projects store"
```

---

### Task 4: DB store — epics + epic_events + guarded transition

**Files:**
- Create: `hubd/internal/db/epics.go`
- Test: `hubd/internal/db/epics_test.go`

**Interfaces:**
- Consumes: Task 2 tables, Task 3 helpers (`marshalStrings`, `unmarshalStrings`, `boolToInt`), `enrollTestServer`/`testProject` test helpers, `github.com/google/uuid` (already a dep).
- Produces:
  - `type Epic struct { ID, ProjectID string; IssueNumber int; Title string; Labels []string; BlockedBy []int; Stage string; Attempt, PRNumber int; SessionName, Branch, Verdict, Needs, IssueState, QueuedAt, StartedAt, StageUpdatedAt, MergedAt string }`
  - `type EpicEvent struct { ID, EpicID, FromStage, ToStage, Source, Note, Ts string }`
  - `func (d *DB) UpsertEpicIssue(ctx context.Context, e Epic) (Epic, error)` — insert new (stage `queued`) or refresh mirror fields (title/labels/blocked_by/issue_state) of an existing row; returns the stored row.
  - `func (d *DB) GetEpic(ctx context.Context, id string) (Epic, error)`
  - `func (d *DB) GetEpicByIssue(ctx context.Context, projectID string, issue int) (Epic, error)`
  - `func (d *DB) ListEpicsByProject(ctx context.Context, projectID string) ([]Epic, error)`
  - `func (d *DB) ListNonTerminalEpics(ctx context.Context) ([]Epic, error)` — stage NOT IN (merged, failed, canceled)
  - `func (d *DB) TransitionEpic(ctx context.Context, id, from, to, source, note, now string) (bool, error)` — guarded `WHERE stage = from`; appends an `epic_events` row on success; stamps `started_at` on `starting`, `merged_at` on `merged`, clears `needs` unless entering `escalated`/`stalled`.
  - `func (d *DB) SetEpicAssignment(ctx context.Context, id, session string, attempt int) (bool, error)`
  - `func (d *DB) SetEpicPR(ctx context.Context, id string, pr int, branch string) (bool, error)`
  - `func (d *DB) SetEpicVerdict(ctx context.Context, id, verdictJSON string) (bool, error)`
  - `func (d *DB) SetEpicNeeds(ctx context.Context, id, needs string) (bool, error)`
  - `func (d *DB) AppendEpicEvent(ctx context.Context, ev EpicEvent) error` — stamps UUID if `ID == ""`
  - `func (d *DB) ListEpicEvents(ctx context.Context, epicID string, limit int) ([]EpicEvent, error)` — newest first

- [x] **Step 1: Write the failing test**

Create `hubd/internal/db/epics_test.go`:

```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func seedProject(t *testing.T, d *DB) {
	t.Helper()
	enrollTestServer(t, d, "aigallery")
	if err := d.CreateProject(context.Background(), testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertEpicIssue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, err := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 15, Title: "GDPR framework",
		Labels: []string{"agentmon:epic"}, BlockedBy: []int{13},
		IssueState: "open", QueuedAt: "2026-07-10T10:00:00Z", StageUpdatedAt: "2026-07-10T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.Stage != "queued" || e.BlockedBy[0] != 13 {
		t.Fatalf("insert: %+v", e)
	}
	// Second upsert refreshes mirror fields but never resets stage/runtime.
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "", "2026-07-10T10:01:00Z"); err != nil || !ok {
		t.Fatalf("transition: ok=%v err=%v", ok, err)
	}
	e2, err := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 15, Title: "GDPR consent & retention",
		Labels: []string{"agentmon:epic", "pr-gate"}, BlockedBy: []int{13},
		IssueState: "open", QueuedAt: "x", StageUpdatedAt: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e2.ID != e.ID || e2.Stage != "starting" || e2.Title != "GDPR consent & retention" || len(e2.Labels) != 2 {
		t.Fatalf("upsert: %+v", e2)
	}
}

func TestTransitionEpicGuarded(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, _ := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 16, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if ok, _ := d.TransitionEpic(ctx, e.ID, "planning", "implementing", "report", "", "t1"); ok {
		t.Fatal("stale-from transition must report false")
	}
	if ok, _ := d.TransitionEpic(ctx, e.ID, "queued", "starting", "hub", "spawn", "t1"); !ok {
		t.Fatal("valid transition must succeed")
	}
	got, _ := d.GetEpic(ctx, e.ID)
	if got.Stage != "starting" || got.StartedAt != "t1" || got.StageUpdatedAt != "t1" {
		t.Fatalf("got %+v", got)
	}
	evs, err := d.ListEpicEvents(ctx, e.ID, 10)
	if err != nil || len(evs) != 1 || evs[0].ToStage != "starting" || evs[0].Source != "hub" {
		t.Fatalf("events = %+v err=%v", evs, err)
	}
	// needs set on escalation, cleared on leaving it
	d.SetEpicNeeds(ctx, e.ID, "2 unresolved findings")
	d.TransitionEpic(ctx, e.ID, "starting", "escalated", "hub", "gate", "t2")
	d.TransitionEpic(ctx, e.ID, "escalated", "queued", "user", "retry", "t3")
	got, _ = d.GetEpic(ctx, e.ID)
	if got.Needs != "" {
		t.Fatalf("needs should clear on leaving escalated, got %q", got.Needs)
	}
}

func TestEpicSettersAndLists(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedProject(t, d)
	e, _ := d.UpsertEpicIssue(ctx, Epic{
		ProjectID: "p1", IssueNumber: 17, IssueState: "open",
		QueuedAt: "t0", StageUpdatedAt: "t0",
	})
	if ok, _ := d.SetEpicAssignment(ctx, e.ID, "epic-17", 1); !ok {
		t.Fatal("assignment")
	}
	if ok, _ := d.SetEpicPR(ctx, e.ID, 61, "epic/17-timetabling"); !ok {
		t.Fatal("pr")
	}
	if ok, _ := d.SetEpicVerdict(ctx, e.ID, `{"uncertain":false}`); !ok {
		t.Fatal("verdict")
	}
	got, _ := d.GetEpicByIssue(ctx, "p1", 17)
	if got.SessionName != "epic-17" || got.Attempt != 1 || got.PRNumber != 61 || got.Branch != "epic/17-timetabling" {
		t.Fatalf("got %+v", got)
	}
	if _, err := d.GetEpicByIssue(ctx, "p1", 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
	all, _ := d.ListEpicsByProject(ctx, "p1")
	nt, _ := d.ListNonTerminalEpics(ctx)
	if len(all) != 1 || len(nt) != 1 {
		t.Fatalf("all=%d nt=%d", len(all), len(nt))
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run TestUpsertEpic -v`
Expected: FAIL — `d.UpsertEpicIssue undefined` (compile error).

- [x] **Step 3: Implement**

Create `hubd/internal/db/epics.go`:

```go
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
	ID          string
	ProjectID   string
	IssueNumber int
	Title       string
	Labels      []string
	BlockedBy   []int
	Stage       string
	Attempt     int
	PRNumber    int
	SessionName string
	Branch      string
	Verdict     string // raw JSON of the parsed verdict, "" until pr_open
	Needs       string // human-readable needs-attention reason
	IssueState  string // "open" | "closed"
	QueuedAt    string
	StartedAt   string
	StageUpdatedAt string
	MergedAt    string
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
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -v && go build ./...`
Expected: PASS (all db tests), clean build.

- [x] **Step 5: Commit**

```bash
git add hubd/internal/db/ && git commit -m "feat(hub): epics + epic_events store with guarded transitions"
```

---

### Task 5: shared — epic stages + orchestrator report wire type

**Files:**
- Create: `shared/orchestrator.go`
- Test: `shared/orchestrator_test.go`

**Interfaces:**
- Produces (consumed by hub Tasks 10–15 and by the agent/CLI in sub-project 2):
  - `type EpicStage string` with consts `EpicQueued="queued"`, `EpicStarting="starting"`, `EpicPlanning="planning"`, `EpicImplementing="implementing"`, `EpicReviewing="reviewing"`, `EpicPROpen="pr_open"`, `EpicMerging="merging"`, `EpicMerged="merged"`, `EpicEscalated="escalated"`, `EpicStalled="stalled"`, `EpicFailed="failed"`, `EpicCanceled="canceled"`
  - `func ValidEpicStage(s string) bool`
  - `func ReportableStage(s EpicStage) bool` — the subset a RUNNER may report: planning, implementing, reviewing, pr_open, escalated
  - `type OrchestratorReport struct { Repo string; Epic int; Stage EpicStage; Note string; PR int; Session string; Ts string }` with json tags `repo, epic, stage, note, pr, session, ts` (`note`/`pr` omitempty)

- [x] **Step 1: Write the failing test**

Create `shared/orchestrator_test.go`:

```go
package shared

import (
	"encoding/json"
	"testing"
)

func TestValidEpicStage(t *testing.T) {
	for _, s := range []string{"queued", "starting", "planning", "implementing",
		"reviewing", "pr_open", "merging", "merged", "escalated", "stalled", "failed", "canceled"} {
		if !ValidEpicStage(s) {
			t.Fatalf("%s should be valid", s)
		}
	}
	if ValidEpicStage("deployed") || ValidEpicStage("") {
		t.Fatal("unknown stages must be invalid")
	}
}

func TestReportableStage(t *testing.T) {
	for _, s := range []EpicStage{EpicPlanning, EpicImplementing, EpicReviewing, EpicPROpen, EpicEscalated} {
		if !ReportableStage(s) {
			t.Fatalf("%s should be reportable", s)
		}
	}
	for _, s := range []EpicStage{EpicQueued, EpicStarting, EpicMerging, EpicMerged, EpicStalled, EpicFailed, EpicCanceled} {
		if ReportableStage(s) {
			t.Fatalf("%s must not be runner-reportable", s)
		}
	}
}

func TestOrchestratorReportJSON(t *testing.T) {
	var r OrchestratorReport
	if err := json.Unmarshal([]byte(
		`{"repo":"o/r","epic":15,"stage":"pr_open","pr":58,"session":"epic-15","ts":"2026-07-10T14:00:00Z"}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Repo != "o/r" || r.Epic != 15 || r.Stage != EpicPROpen || r.PR != 58 {
		t.Fatalf("got %+v", r)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/shared && go test ./ -run 'TestValidEpicStage|TestReportableStage|TestOrchestratorReportJSON' -v`
Expected: FAIL — `ValidEpicStage undefined` (compile error).

- [x] **Step 3: Implement**

Create `shared/orchestrator.go`:

```go
package shared

// EpicStage is the orchestrator pipeline stage of one epic. The full state
// machine lives hub-side; agents and the report CLI only need the names.
type EpicStage string

const (
	EpicQueued       EpicStage = "queued"
	EpicStarting     EpicStage = "starting"
	EpicPlanning     EpicStage = "planning"
	EpicImplementing EpicStage = "implementing"
	EpicReviewing    EpicStage = "reviewing"
	EpicPROpen       EpicStage = "pr_open"
	EpicMerging      EpicStage = "merging"
	EpicMerged       EpicStage = "merged"
	EpicEscalated    EpicStage = "escalated"
	EpicStalled      EpicStage = "stalled"
	EpicFailed       EpicStage = "failed"
	EpicCanceled     EpicStage = "canceled"
)

var epicStages = map[EpicStage]bool{
	EpicQueued: true, EpicStarting: true, EpicPlanning: true, EpicImplementing: true,
	EpicReviewing: true, EpicPROpen: true, EpicMerging: true, EpicMerged: true,
	EpicEscalated: true, EpicStalled: true, EpicFailed: true, EpicCanceled: true,
}

func ValidEpicStage(s string) bool { return epicStages[EpicStage(s)] }

// ReportableStage is the subset a runner session may self-report. Everything
// else is hub- or GitHub-derived; a report claiming those is rejected.
func ReportableStage(s EpicStage) bool {
	switch s {
	case EpicPlanning, EpicImplementing, EpicReviewing, EpicPROpen, EpicEscalated:
		return true
	}
	return false
}

// OrchestratorReport is one runner stage report. The CLI posts it to the local
// agent's loopback intake; the hub drains buffered reports over its existing
// poll channel (hub dials agent — there is no agent→hub connection).
type OrchestratorReport struct {
	Repo    string    `json:"repo"`
	Epic    int       `json:"epic"`
	Stage   EpicStage `json:"stage"`
	Note    string    `json:"note,omitempty"`
	PR      int       `json:"pr,omitempty"`
	Session string    `json:"session"`
	Ts      string    `json:"ts"`
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/shared && go test ./ -v && cd /root/agentmon/hubd && go build ./...`
Expected: PASS; hub still builds.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add shared/ && git commit -m "feat(shared): epic stages + orchestrator report wire type"
```

- [x] **Step 6: CHECKPOINT 1 — STOP**

Tasks 1–5 (foundations: config, schema, stores, shared types) are a review checkpoint. STOP here — do not begin Task 6. Report that checkpoint 1 is reached, listing completed tasks and the final `go test ./...` result. Resume only on an explicit "continue" or after applying explicit fix instructions from the checkpoint review.

---

### Task 6: GitHub REST client

**Files:**
- Create: `hubd/internal/github/client.go`
- Test: `hubd/internal/github/client_test.go`

**Interfaces:**
- Consumes: stdlib only.
- Produces (consumed by sync/loop Tasks 14–15 and gate evaluation):
  - `type Issue struct { Number int; Title, Body, State string; Labels []string; UpdatedAt string }`
  - `type PullRequest struct { Number int; State string; Merged bool; Body, HeadSHA, HeadRef string }`
  - `type CheckRun struct { Name, Status, Conclusion string }`
  - `func ChecksState(runs []CheckRun) (green, pending bool)` — no runs ⇒ green=true (repo without CI; gate still needs a clean verdict)
  - `type Client struct { Base, Token string; HTTP *http.Client }`, `func NewClient(token string) *Client` (Base `https://api.github.com`, 15s timeout)
  - Methods (all `ctx context.Context, repo string` first; repo is `owner/name`):
    - `GetIssue(ctx, repo string, num int) (Issue, error)`
    - `ListIssuesSince(ctx, repo, since string) ([]Issue, error)` — `state=all`, `per_page=100`; empty `since` omits the param; PRs filtered out (`pull_request` key)
    - `GetPullRequest(ctx, repo string, num int) (PullRequest, error)`
    - `ListCheckRuns(ctx, repo, ref string) ([]CheckRun, error)`
    - `MergePR(ctx, repo string, num int) error` — squash; 405/409 ⇒ `ErrNotMergeable`
    - `CreateIssueComment(ctx, repo string, num int, body string) error`
    - `AddLabels(ctx, repo string, num int, labels []string) error`
    - `RemoveLabel(ctx, repo string, num int, label string) error` — 404 tolerated (already absent)
  - Sentinels: `ErrNotFound`, `ErrNotMergeable`

- [x] **Step 1: Write the failing test**

Create `hubd/internal/github/client_test.go`:

```go
package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGH records requests and serves canned JSON per path.
func fakeGH(t *testing.T, routes map[string]any, status map[string]int, seen *[]*http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.Clone(context.Background()))
		}
		key := r.Method + " " + r.URL.Path
		if s, ok := status[key]; ok {
			w.WriteHeader(s)
			return
		}
		body, ok := routes[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
}

func TestGetIssueAndAuth(t *testing.T) {
	var seen []*http.Request
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/issues/15": map[string]any{
			"number": 15, "title": "GDPR", "body": "Blocked by #13", "state": "open",
			"updated_at": "2026-07-10T10:00:00Z",
			"labels":     []map[string]any{{"name": "agentmon:epic"}, {"name": "pr-gate"}},
		},
	}, nil, &seen)
	defer srv.Close()
	c := NewClient("tok")
	c.Base = srv.URL
	is, err := c.GetIssue(context.Background(), "o/r", 15)
	if err != nil {
		t.Fatal(err)
	}
	if is.Number != 15 || is.Labels[1] != "pr-gate" || is.State != "open" {
		t.Fatalf("got %+v", is)
	}
	if got := seen[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("auth header = %q", got)
	}
	if _, err := c.GetIssue(context.Background(), "o/r", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListIssuesSinceFiltersPRs(t *testing.T) {
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/issues": []map[string]any{
			{"number": 1, "title": "epic", "state": "open", "labels": []map[string]any{}},
			{"number": 2, "title": "a pr", "state": "open", "pull_request": map[string]any{"url": "x"},
				"labels": []map[string]any{}},
		},
	}, nil, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	got, err := c.ListIssuesSince(context.Background(), "o/r", "")
	if err != nil || len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestGetPullRequestAndChecks(t *testing.T) {
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/pulls/58": map[string]any{
			"number": 58, "state": "open", "merged": false, "body": "…verdict…",
			"head": map[string]any{"sha": "abc123", "ref": "epic/15-gdpr"},
		},
		"GET /repos/o/r/commits/abc123/check-runs": map[string]any{
			"check_runs": []map[string]any{
				{"name": "ci", "status": "completed", "conclusion": "success"},
				{"name": "lint", "status": "in_progress", "conclusion": ""},
			},
		},
	}, nil, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	pr, err := c.GetPullRequest(context.Background(), "o/r", 58)
	if err != nil || pr.HeadSHA != "abc123" || pr.HeadRef != "epic/15-gdpr" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
	runs, err := c.ListCheckRuns(context.Background(), "o/r", "abc123")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	green, pending := ChecksState(runs)
	if green || !pending {
		t.Fatalf("green=%v pending=%v", green, pending)
	}
	if g, p := ChecksState(nil); !g || p {
		t.Fatalf("no CI must read green, got green=%v pending=%v", g, p)
	}
}

func TestMergePR(t *testing.T) {
	srv := fakeGH(t,
		map[string]any{"PUT /repos/o/r/pulls/58/merge": map[string]any{"merged": true}},
		map[string]int{"PUT /repos/o/r/pulls/59/merge": 409}, nil)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	if err := c.MergePR(context.Background(), "o/r", 58); err != nil {
		t.Fatal(err)
	}
	if err := c.MergePR(context.Background(), "o/r", 59); !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("want ErrNotMergeable, got %v", err)
	}
}

func TestWriteBackCalls(t *testing.T) {
	var seen []*http.Request
	srv := fakeGH(t, map[string]any{
		"POST /repos/o/r/issues/15/comments":            map[string]any{"id": 1},
		"POST /repos/o/r/issues/15/labels":              []map[string]any{},
		"DELETE /repos/o/r/issues/15/labels/agentmon:x": map[string]any{},
	}, nil, &seen)
	defer srv.Close()
	c := NewClient("t")
	c.Base = srv.URL
	ctx := context.Background()
	if err := c.CreateIssueComment(ctx, "o/r", 15, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLabels(ctx, "o/r", 15, []string{"agentmon:merged"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveLabel(ctx, "o/r", 15, "agentmon:x"); err != nil {
		t.Fatal(err)
	}
	// RemoveLabel tolerates 404 (label already absent)
	if err := c.RemoveLabel(ctx, "o/r", 15, "gone"); err != nil {
		t.Fatalf("404 remove should be nil, got %v", err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -v`
Expected: FAIL — package doesn't exist / `NewClient undefined`.

- [x] **Step 3: Implement**

Create `hubd/internal/github/client.go`:

```go
// Package github is a minimal GitHub REST v3 client — only the calls the
// orchestrator needs, hand-rolled to avoid a dependency. Fine-grained PAT.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrNotFound     = errors.New("github: not found")
	ErrNotMergeable = errors.New("github: not mergeable")
)

type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string
	Labels    []string
	UpdatedAt string
}

type PullRequest struct {
	Number  int
	State   string
	Merged  bool
	Body    string
	HeadSHA string
	HeadRef string
}

type CheckRun struct {
	Name       string
	Status     string // queued | in_progress | completed
	Conclusion string // success | failure | neutral | skipped | …
}

// ChecksState folds check runs into (green, pending). No runs at all reads
// green: a repo without CI must still pass the verdict gate, which fails closed.
func ChecksState(runs []CheckRun) (green, pending bool) {
	green = true
	for _, r := range runs {
		if r.Status != "completed" {
			return false, true
		}
		switch r.Conclusion {
		case "success", "neutral", "skipped":
		default:
			green = false
		}
	}
	return green, false
}

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		Base:  "https://api.github.com",
		Token: token,
		HTTP:  &http.Client{Timeout: 15 * time.Second},
	}
}

// do performs one API call; out may be nil. okStatus lists acceptable codes.
func (c *Client) do(ctx context.Context, method, path string, in, out any, okStatus ...int) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	for _, s := range okStatus {
		if resp.StatusCode == s {
			if out != nil {
				return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
			}
			return resp.StatusCode, nil
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, ErrNotFound
	}
	return resp.StatusCode, fmt.Errorf("github: %s %s → %d", method, path, resp.StatusCode)
}

type wireIssue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	UpdatedAt   string `json:"updated_at"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (w wireIssue) issue() Issue {
	is := Issue{Number: w.Number, Title: w.Title, Body: w.Body, State: w.State, UpdatedAt: w.UpdatedAt}
	for _, l := range w.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

func (c *Client) GetIssue(ctx context.Context, repo string, num int) (Issue, error) {
	var w wireIssue
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d", repo, num), nil, &w, 200)
	if err != nil {
		return Issue{}, err
	}
	return w.issue(), nil
}

func (c *Client) ListIssuesSince(ctx context.Context, repo, since string) ([]Issue, error) {
	q := url.Values{"state": {"all"}, "per_page": {"100"}}
	if since != "" {
		q.Set("since", since)
	}
	var ws []wireIssue
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/issues?%s", repo, q.Encode()), nil, &ws, 200)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, w := range ws {
		if w.PullRequest != nil { // the issues API interleaves PRs; skip them
			continue
		}
		out = append(out, w.issue())
	}
	return out, nil
}

func (c *Client) GetPullRequest(ctx context.Context, repo string, num int) (PullRequest, error) {
	var w struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Body   string `json:"body"`
		Head   struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	}
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", repo, num), nil, &w, 200)
	if err != nil {
		return PullRequest{}, err
	}
	return PullRequest{Number: w.Number, State: w.State, Merged: w.Merged,
		Body: w.Body, HeadSHA: w.Head.SHA, HeadRef: w.Head.Ref}, nil
}

func (c *Client) ListCheckRuns(ctx context.Context, repo, ref string) ([]CheckRun, error) {
	var w struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	_, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, ref), nil, &w, 200)
	if err != nil {
		return nil, err
	}
	var out []CheckRun
	for _, r := range w.CheckRuns {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
	}
	return out, nil
}

func (c *Client) MergePR(ctx context.Context, repo string, num int) error {
	status, err := c.do(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, num),
		map[string]string{"merge_method": "squash"}, nil, 200)
	if err != nil && (status == 405 || status == 409) {
		return ErrNotMergeable
	}
	return err
}

func (c *Client) CreateIssueComment(ctx context.Context, repo string, num int, body string) error {
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", repo, num),
		map[string]string{"body": body}, nil, 201, 200)
	return err
}

func (c *Client) AddLabels(ctx context.Context, repo string, num int, labels []string) error {
	_, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/labels", repo, num),
		map[string][]string{"labels": labels}, nil, 200, 201)
	return err
}

func (c *Client) RemoveLabel(ctx context.Context, repo string, num int, label string) error {
	_, err := c.do(ctx, "DELETE",
		fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, num, url.PathEscape(label)), nil, nil, 200, 204)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -v && go build ./...`
Expected: PASS (5 tests).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/github/ && git commit -m "feat(hub): minimal github rest client"
```

---

### Task 7: GitHub webhook — HMAC verify + event parse

**Files:**
- Create: `hubd/internal/github/webhook.go`
- Test: `hubd/internal/github/webhook_test.go`

**Interfaces:**
- Consumes: `Issue` from Task 6.
- Produces:
  - `func VerifySignature(secret string, body []byte, sigHeader string) bool` — `X-Hub-Signature-256: sha256=<hex hmac>`; constant-time compare; empty secret or header ⇒ false
  - `type Event struct { Kind, Action, Repo string; Issue *Issue; PRNumber int; PRMerged bool }`
  - `func ParseEvent(kind string, body []byte) (Event, error)` — kinds handled: `issues`, `pull_request`, `check_suite`, `ping`; anything else ⇒ `Event{Kind: kind}` with only Repo filled when present

- [x] **Step 1: Write the failing test**

Create `hubd/internal/github/webhook_test.go`:

```go
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"zen":"x"}`)
	if !VerifySignature("s3cret", body, sign("s3cret", body)) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature("s3cret", body, sign("wrong", body)) {
		t.Fatal("bad signature accepted")
	}
	if VerifySignature("", body, sign("", body)) {
		t.Fatal("empty secret must always fail")
	}
	if VerifySignature("s3cret", body, "") {
		t.Fatal("missing header must fail")
	}
}

func TestParseIssuesEvent(t *testing.T) {
	ev, err := ParseEvent("issues", []byte(`{
	  "action": "labeled",
	  "repository": {"full_name": "o/r"},
	  "issue": {"number": 15, "title": "GDPR", "body": "Blocked by #13", "state": "open",
	            "labels": [{"name":"agentmon:epic"}], "updated_at": "2026-07-10T10:00:00Z"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "issues" || ev.Action != "labeled" || ev.Repo != "o/r" {
		t.Fatalf("got %+v", ev)
	}
	if ev.Issue == nil || ev.Issue.Number != 15 || ev.Issue.Labels[0] != "agentmon:epic" {
		t.Fatalf("issue = %+v", ev.Issue)
	}
}

func TestParsePullRequestEvent(t *testing.T) {
	ev, err := ParseEvent("pull_request", []byte(`{
	  "action": "closed",
	  "repository": {"full_name": "o/r"},
	  "pull_request": {"number": 58, "merged": true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.PRNumber != 58 || !ev.PRMerged || ev.Action != "closed" {
		t.Fatalf("got %+v", ev)
	}
}

func TestParseUnknownKind(t *testing.T) {
	ev, err := ParseEvent("workflow_run", []byte(`{"repository":{"full_name":"o/r"}}`))
	if err != nil || ev.Kind != "workflow_run" || ev.Repo != "o/r" {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -run 'TestVerify|TestParse' -v`
Expected: FAIL — `VerifySignature undefined`.

- [x] **Step 3: Implement**

Create `hubd/internal/github/webhook.go`:

```go
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// VerifySignature checks X-Hub-Signature-256 over the raw body. Fails closed:
// no secret configured or no header ⇒ reject.
func VerifySignature(secret string, body []byte, sigHeader string) bool {
	if secret == "" || !strings.HasPrefix(sigHeader, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sigHeader, "sha256="))
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// Event is the orchestrator-relevant projection of one webhook delivery.
type Event struct {
	Kind     string
	Action   string
	Repo     string
	Issue    *Issue
	PRNumber int
	PRMerged bool
}

func ParseEvent(kind string, body []byte) (Event, error) {
	var w struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue       *wireIssue `json:"issue"`
		PullRequest *struct {
			Number int  `json:"number"`
			Merged bool `json:"merged"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Event{}, err
	}
	ev := Event{Kind: kind, Action: w.Action, Repo: w.Repository.FullName}
	if w.Issue != nil {
		is := w.Issue.issue()
		ev.Issue = &is
	}
	if w.PullRequest != nil {
		ev.PRNumber = w.PullRequest.Number
		ev.PRMerged = w.PullRequest.Merged
	}
	return ev, nil
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add hubd/internal/github/ && git commit -m "feat(hub): webhook hmac verify + event parse"
```

---

### Task 8: Verdict parser

**Files:**
- Create: `hubd/internal/orchestrator/verdict.go`
- Test: `hubd/internal/orchestrator/verdict_test.go`

**Interfaces:**
- Consumes: `gopkg.in/yaml.v3` (existing dep).
- Produces:
  - `type Verdict struct { Schema string; Epic int; Reviews []string; Findings VerdictFindings; Unresolved []string; Tests VerdictTests; Uncertain, LearningsUpdated bool }` with `VerdictFindings{Found, Resolved, Unresolved int}`, `VerdictTests{Passed, Failed int}` (yaml tags below)
  - `var ErrNoVerdict = errors.New("orchestrator: no verdict block")`
  - `func ParseVerdict(prBody string) (*Verdict, error)` — scans fenced ` ```yaml ` blocks, parses the LAST one whose YAML contains key `agentmon-verdict`; malformed YAML in that block ⇒ error (gate treats any error as escalate)

- [x] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/verdict_test.go`:

```go
package orchestrator

import (
	"errors"
	"testing"
)

const goodBody = "Implements #15.\n\n```yaml\n" +
	"agentmon-verdict: v1\n" +
	"epic: 15\n" +
	"reviews: [specialist, simplifier, deep-scan, codex]\n" +
	"findings: { found: 9, resolved: 7, unresolved: 2 }\n" +
	"unresolved:\n  - \"Deletion cascade\"\n  - \"Retention default\"\n" +
	"tests: { passed: 47, failed: 0 }\n" +
	"uncertain: true\n" +
	"learnings_updated: true\n" +
	"```\n"

func TestParseVerdict(t *testing.T) {
	v, err := ParseVerdict(goodBody)
	if err != nil {
		t.Fatal(err)
	}
	if v.Schema != "v1" || v.Epic != 15 || len(v.Reviews) != 4 ||
		v.Findings.Unresolved != 2 || v.Tests.Passed != 47 || !v.Uncertain || !v.LearningsUpdated {
		t.Fatalf("got %+v", v)
	}
	if len(v.Unresolved) != 2 || v.Unresolved[0] != "Deletion cascade" {
		t.Fatalf("unresolved = %v", v.Unresolved)
	}
}

func TestParseVerdictPicksLastBlock(t *testing.T) {
	body := "```yaml\nagentmon-verdict: v1\nepic: 1\nuncertain: true\n```\n\nrevised:\n\n" +
		"```yaml\nagentmon-verdict: v1\nepic: 1\nuncertain: false\n```\n"
	v, err := ParseVerdict(body)
	if err != nil || v.Uncertain {
		t.Fatalf("want last block (uncertain=false), got %+v err=%v", v, err)
	}
}

func TestParseVerdictMissing(t *testing.T) {
	if _, err := ParseVerdict("no block here\n```yaml\nother: doc\n```"); !errors.Is(err, ErrNoVerdict) {
		t.Fatalf("want ErrNoVerdict, got %v", err)
	}
}

func TestParseVerdictMalformed(t *testing.T) {
	if _, err := ParseVerdict("```yaml\nagentmon-verdict: v1\nepic: [broken\n```"); err == nil || errors.Is(err, ErrNoVerdict) {
		t.Fatalf("malformed block must be a distinct error, got %v", err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -v`
Expected: FAIL — package doesn't exist.

- [x] **Step 3: Implement**

Create `hubd/internal/orchestrator/verdict.go`:

```go
// Package orchestrator is the hub-side epic pipeline brain: state machine,
// scheduler, merge gate, GitHub sync, and the run loop.
package orchestrator

import (
	"errors"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrNoVerdict = errors.New("orchestrator: no verdict block")

type VerdictFindings struct {
	Found      int `yaml:"found"`
	Resolved   int `yaml:"resolved"`
	Unresolved int `yaml:"unresolved"`
}

type VerdictTests struct {
	Passed int `yaml:"passed"`
	Failed int `yaml:"failed"`
}

// Verdict is the runner's structured self-report, embedded as the last
// ```yaml block of the PR body. The gate treats it as data, not argument.
type Verdict struct {
	Schema           string          `yaml:"agentmon-verdict"`
	Epic             int             `yaml:"epic"`
	Reviews          []string        `yaml:"reviews"`
	Findings         VerdictFindings `yaml:"findings"`
	Unresolved       []string        `yaml:"unresolved"`
	Tests            VerdictTests    `yaml:"tests"`
	Uncertain        bool            `yaml:"uncertain"`
	LearningsUpdated bool            `yaml:"learnings_updated"`
}

var fencedYAML = regexp.MustCompile("(?s)```(?:yaml|yml)\\s*\\n(.*?)```")

// ParseVerdict extracts the LAST fenced yaml block containing an
// agentmon-verdict key. Returns ErrNoVerdict when absent; a YAML error when
// the block exists but is malformed (the gate escalates on both).
func ParseVerdict(prBody string) (*Verdict, error) {
	matches := fencedYAML.FindAllStringSubmatch(prBody, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		block := matches[i][1]
		if !strings.Contains(block, "agentmon-verdict") {
			continue
		}
		var v Verdict
		if err := yaml.Unmarshal([]byte(block), &v); err != nil {
			return nil, err
		}
		return &v, nil
	}
	return nil, ErrNoVerdict
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -v`
Expected: PASS (4 tests).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): verdict block parser"
```

---

### Task 9: Merge gate

**Files:**
- Create: `hubd/internal/orchestrator/gate.go`
- Test: `hubd/internal/orchestrator/gate_test.go`

**Interfaces:**
- Consumes: `Verdict` (Task 8).
- Produces:
  - `type GateInput struct { Verdict *Verdict; VerdictErr error; Labels []string; RequiredReviews []string; ChecksGreen, ChecksPending bool }`
  - `type GateResult struct { Merge, Wait bool; Reason string }` — exactly one of Merge / Wait / escalate (`!Merge && !Wait`, Reason set)
  - `func Decide(in GateInput) GateResult`

Decision order (first match wins — mirrors spec §6):
1. `ChecksPending` ⇒ Wait ("checks pending")
2. `pr-gate` label ⇒ escalate "pr-gate label: human merges"
3. `VerdictErr != nil` or `Verdict == nil` ⇒ escalate "missing or malformed verdict"
4. `!ChecksGreen` ⇒ escalate "CI checks failing"
5. `Uncertain` ⇒ escalate "runner flagged uncertainty"
6. `Findings.Unresolved > 0` or `len(Unresolved) > 0` ⇒ escalate "N unresolved review findings"
7. `Tests.Failed > 0` ⇒ escalate "tests failing"
8. required reviews not a subset of `Verdict.Reviews` ⇒ escalate "missing required reviews: …"
9. otherwise ⇒ Merge

- [x] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/gate_test.go`:

```go
package orchestrator

import (
	"strings"
	"testing"
)

func cleanVerdict() *Verdict {
	return &Verdict{Schema: "v1", Epic: 15,
		Reviews: []string{"specialist", "simplifier", "deep-scan", "codex"},
		Tests:   VerdictTests{Passed: 10}}
}

func TestDecide(t *testing.T) {
	req := []string{"specialist", "codex"}
	cases := []struct {
		name   string
		in     GateInput
		merge  bool
		wait   bool
		reason string
	}{
		{"clean merges", GateInput{Verdict: cleanVerdict(), RequiredReviews: req, ChecksGreen: true}, true, false, ""},
		{"pending waits", GateInput{Verdict: cleanVerdict(), RequiredReviews: req, ChecksPending: true}, false, true, "pending"},
		{"pr-gate escalates", GateInput{Verdict: cleanVerdict(), Labels: []string{"pr-gate"}, ChecksGreen: true}, false, false, "pr-gate"},
		{"nil verdict escalates", GateInput{Verdict: nil, ChecksGreen: true}, false, false, "verdict"},
		{"verdict err escalates", GateInput{Verdict: cleanVerdict(), VerdictErr: ErrNoVerdict, ChecksGreen: true}, false, false, "verdict"},
		{"red checks escalate", GateInput{Verdict: cleanVerdict(), ChecksGreen: false}, false, false, "CI"},
		{"uncertain escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Uncertain = true; return v }(), ChecksGreen: true}, false, false, "uncertain"},
		{"unresolved escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Findings.Unresolved = 2; return v }(), ChecksGreen: true}, false, false, "unresolved"},
		{"failed tests escalate", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Tests.Failed = 1; return v }(), ChecksGreen: true}, false, false, "tests"},
		{"missing review escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Reviews = []string{"specialist"}; return v }(), RequiredReviews: req, ChecksGreen: true}, false, false, "required reviews"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Merge != c.merge || got.Wait != c.wait {
				t.Fatalf("got %+v", got)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("reason %q missing %q", got.Reason, c.reason)
			}
		})
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestDecide -v`
Expected: FAIL — `Decide undefined`.

- [x] **Step 3: Implement**

Create `hubd/internal/orchestrator/gate.go`:

```go
package orchestrator

import (
	"fmt"
	"strings"
)

type GateInput struct {
	Verdict         *Verdict
	VerdictErr      error
	Labels          []string
	RequiredReviews []string
	ChecksGreen     bool
	ChecksPending   bool
}

// GateResult: Merge, Wait, or (neither) escalate-with-Reason.
type GateResult struct {
	Merge  bool
	Wait   bool
	Reason string
}

// Decide is the deterministic merge gate. It FAILS CLOSED: every ambiguous
// input escalates. The verdict is parsed data — a runner cannot argue past it.
func Decide(in GateInput) GateResult {
	if in.ChecksPending {
		return GateResult{Wait: true, Reason: "checks pending"}
	}
	if hasLabel(in.Labels, "pr-gate") {
		return GateResult{Reason: "pr-gate label: human merges"}
	}
	if in.VerdictErr != nil || in.Verdict == nil {
		return GateResult{Reason: "missing or malformed verdict"}
	}
	if !in.ChecksGreen {
		return GateResult{Reason: "CI checks failing"}
	}
	v := in.Verdict
	if v.Uncertain {
		return GateResult{Reason: "runner flagged uncertainty"}
	}
	if n := max(v.Findings.Unresolved, len(v.Unresolved)); n > 0 {
		return GateResult{Reason: fmt.Sprintf("%d unresolved review findings", n)}
	}
	if v.Tests.Failed > 0 {
		return GateResult{Reason: fmt.Sprintf("tests failing (%d)", v.Tests.Failed)}
	}
	if missing := missingReviews(in.RequiredReviews, v.Reviews); len(missing) > 0 {
		return GateResult{Reason: "missing required reviews: " + strings.Join(missing, ", ")}
	}
	return GateResult{Merge: true}
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func missingReviews(required, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var missing []string
	for _, r := range required {
		if !have[r] {
			missing = append(missing, r)
		}
	}
	return missing
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestDecide -v`
Expected: PASS (10 subtests).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): fail-closed merge gate"
```

---

### Task 10: Transition validity

**Files:**
- Create: `hubd/internal/orchestrator/machine.go`
- Test: `hubd/internal/orchestrator/machine_test.go`

**Interfaces:**
- Consumes: `shared.EpicStage` consts (Task 5).
- Produces: `func ValidTransition(from, to shared.EpicStage) bool`

Rules:
- Any ACTIVE stage (`starting, planning, implementing, reviewing, pr_open, merging`) → `escalated | stalled | canceled`: allowed.
- Forward path: `queued→starting→planning→implementing→reviewing→pr_open→merging→merged`; runners may legitimately skip (`pipeline:light`, fast reports): allow any forward jump along that ordering (e.g. `starting→implementing`, `planning→pr_open`), plus the fix-loop back-edge `reviewing→implementing`.
- Recovery: `escalated → queued | implementing | merging | canceled`; `stalled → queued | canceled | failed`; `queued → canceled`.
- Terminal (`merged, failed, canceled`): no exits.

- [x] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/machine_test.go`:

```go
package orchestrator

import (
	"agentmon/shared"
	"testing"
)

func TestValidTransition(t *testing.T) {
	ok := [][2]shared.EpicStage{
		{shared.EpicQueued, shared.EpicStarting},
		{shared.EpicStarting, shared.EpicPlanning},
		{shared.EpicStarting, shared.EpicImplementing}, // pipeline:light skips planning
		{shared.EpicPlanning, shared.EpicPROpen},       // forward jump
		{shared.EpicReviewing, shared.EpicImplementing}, // fix loop
		{shared.EpicPROpen, shared.EpicMerging},
		{shared.EpicMerging, shared.EpicMerged},
		{shared.EpicImplementing, shared.EpicEscalated},
		{shared.EpicPlanning, shared.EpicStalled},
		{shared.EpicEscalated, shared.EpicQueued},
		{shared.EpicEscalated, shared.EpicMerging},   // board Approve
		{shared.EpicEscalated, shared.EpicImplementing}, // plan-approval resume
		{shared.EpicStalled, shared.EpicQueued},
		{shared.EpicStalled, shared.EpicFailed},
		{shared.EpicQueued, shared.EpicCanceled},
	}
	for _, p := range ok {
		if !ValidTransition(p[0], p[1]) {
			t.Errorf("%s→%s should be valid", p[0], p[1])
		}
	}
	bad := [][2]shared.EpicStage{
		{shared.EpicMerged, shared.EpicQueued},      // terminal
		{shared.EpicCanceled, shared.EpicStarting},  // terminal
		{shared.EpicPROpen, shared.EpicPlanning},    // backward (not the fix loop)
		{shared.EpicQueued, shared.EpicMerged},      // queued only starts or cancels
		{shared.EpicMerging, shared.EpicQueued},
		{shared.EpicQueued, shared.EpicQueued},      // self-loop
	}
	for _, p := range bad {
		if ValidTransition(p[0], p[1]) {
			t.Errorf("%s→%s should be invalid", p[0], p[1])
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestValidTransition -v`
Expected: FAIL — `ValidTransition undefined`.

- [x] **Step 3: Implement**

Create `hubd/internal/orchestrator/machine.go`:

```go
package orchestrator

import "agentmon/shared"

// forwardOrder positions the happy-path stages; a forward jump is legal
// (runners may skip stages: pipeline:light, missed reports).
var forwardOrder = map[shared.EpicStage]int{
	shared.EpicQueued: 0, shared.EpicStarting: 1, shared.EpicPlanning: 2,
	shared.EpicImplementing: 3, shared.EpicReviewing: 4, shared.EpicPROpen: 5,
	shared.EpicMerging: 6, shared.EpicMerged: 7,
}

var activeStages = map[shared.EpicStage]bool{
	shared.EpicStarting: true, shared.EpicPlanning: true, shared.EpicImplementing: true,
	shared.EpicReviewing: true, shared.EpicPROpen: true, shared.EpicMerging: true,
}

// ValidTransition is the single authority on legal stage moves. TransitionEpic
// guards racing writers; this guards nonsense.
func ValidTransition(from, to shared.EpicStage) bool {
	if from == to {
		return false
	}
	switch from {
	case shared.EpicMerged, shared.EpicFailed, shared.EpicCanceled:
		return false // terminal
	case shared.EpicQueued:
		return to == shared.EpicStarting || to == shared.EpicCanceled
	case shared.EpicEscalated:
		switch to {
		case shared.EpicQueued, shared.EpicImplementing, shared.EpicMerging, shared.EpicCanceled:
			return true
		}
		return false
	case shared.EpicStalled:
		switch to {
		case shared.EpicQueued, shared.EpicFailed, shared.EpicCanceled:
			return true
		}
		return false
	}
	// from is an active stage
	switch to {
	case shared.EpicEscalated, shared.EpicStalled, shared.EpicCanceled:
		return true
	case shared.EpicImplementing:
		if from == shared.EpicReviewing {
			return true // fix loop
		}
	}
	f, okF := forwardOrder[from]
	t, okT := forwardOrder[to]
	return okF && okT && t > f
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestValidTransition -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): epic stage transition table"
```

---

### Task 11: Board broadcaster

**Files:**
- Create: `hubd/internal/orchestrator/broadcast.go`
- Test: `hubd/internal/orchestrator/broadcast_test.go`

**Interfaces:**
- Consumes: `shared.EpicStage`.
- Produces (consumed by run loop Task 15, push Task 16, SSE Task 19):
  - `type BoardChange struct { ProjectID, EpicID string; Issue int; Stage shared.EpicStage; Needs, Title string }`
  - `type BoardBroadcaster` with `NewBoardBroadcaster() *BoardBroadcaster`, `Subscribe() (id uint64, ch <-chan BoardChange, cancel func())`, `Publish(c BoardChange)` — same drop-oldest, non-blocking semantics as `state.Broadcaster` (`state/broadcaster.go`), buffer 64.

- [x] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/broadcast_test.go`:

```go
package orchestrator

import (
	"agentmon/shared"
	"testing"
)

func TestBoardBroadcastFanOut(t *testing.T) {
	b := NewBoardBroadcaster()
	_, ch1, cancel1 := b.Subscribe()
	_, ch2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()
	b.Publish(BoardChange{EpicID: "e1", Stage: shared.EpicMerged})
	for i, ch := range []<-chan BoardChange{ch1, ch2} {
		got := <-ch
		if got.EpicID != "e1" || got.Stage != shared.EpicMerged {
			t.Fatalf("sub %d got %+v", i, got)
		}
	}
}

func TestBoardBroadcastDropOldestNeverBlocks(t *testing.T) {
	b := NewBoardBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ { // 3x the buffer; Publish must not block
		b.Publish(BoardChange{Issue: i})
	}
	got := <-ch
	if got.Issue == 0 {
		t.Fatal("oldest change should have been dropped")
	}
}

func TestBoardBroadcastCancelIdempotent(t *testing.T) {
	b := NewBoardBroadcaster()
	_, _, cancel := b.Subscribe()
	cancel()
	cancel() // must not panic
	b.Publish(BoardChange{EpicID: "x"})
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestBoardBroadcast -v`
Expected: FAIL — `NewBoardBroadcaster undefined`.

- [x] **Step 3: Implement**

Create `hubd/internal/orchestrator/broadcast.go` (mirror of `state/broadcaster.go` semantics):

```go
package orchestrator

import (
	"sync"

	"agentmon/shared"
)

const boardSubBufCap = 64

// BoardChange is one epic's board-relevant delta, fanned to SSE + push.
type BoardChange struct {
	ProjectID string
	EpicID    string
	Issue     int
	Stage     shared.EpicStage
	Needs     string
	Title     string
}

type BoardBroadcaster struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]chan BoardChange
}

func NewBoardBroadcaster() *BoardBroadcaster {
	return &BoardBroadcaster{subs: map[uint64]chan BoardChange{}}
}

func (b *BoardBroadcaster) Subscribe() (uint64, <-chan BoardChange, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan BoardChange, boardSubBufCap)
	b.subs[id] = ch
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
		})
	}
	return id, ch, cancel
}

// Publish never blocks: a slow subscriber loses its oldest queued change.
func (b *BoardBroadcaster) Publish(c BoardChange) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- c:
		default:
			select { // drop oldest, then retry once
			case <-ch:
			default:
			}
			select {
			case ch <- c:
			default:
			}
		}
	}
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestBoardBroadcast -v -race`
Expected: PASS under `-race`.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): board change broadcaster"
```

- [x] **Step 6: CHECKPOINT 2 — STOP**

Tasks 6–11 (GitHub client, webhook, verdict, gate, machine, broadcaster) are a review checkpoint. STOP here — do not begin Task 12. Report that checkpoint 2 is reached, listing completed tasks and the final `go test ./...` result. Resume only on an explicit "continue" or after applying explicit fix instructions from the checkpoint review.

---

### Task 12: Scheduler — ready-set + kickoff command

**Files:**
- Create: `hubd/internal/orchestrator/scheduler.go`
- Test: `hubd/internal/orchestrator/scheduler_test.go`

**Interfaces:**
- Consumes: `db.Epic` (Task 4), `shared.EpicStage`, `activeStages` (Task 10), `hasLabel` (Task 9).
- Produces:
  - `func ReadyEpics(epics []db.Epic, maxParallel int, paused bool) []db.Epic` — deps resolved against the passed slice
  - `func KickoffCommand(provider string, issue int) string`
  - `func SessionNameFor(issue int) string` → `epic-N`
  - `func ProviderFor(projectDefault string, labels []string) string` — `agent:codex` / `agent:claude` label overrides

Ready rules: stage `queued` ∧ `issue_state == "open"` ∧ every `blocked_by` issue's epic (matched by `IssueNumber` within the slice) is stage `merged` OR `issue_state == "closed"`; a dep with NO epic row blocks (fail closed) — the sync loop surfaces it via the epic's `needs` later. Capacity = `maxParallel − count(stage ∈ activeStages)`; `paused` ⇒ empty. Order by ascending issue number, truncate to capacity.

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/scheduler_test.go`:

```go
package orchestrator

import (
	"testing"

	"agentmon/hubd/internal/db"
)

func qe(issue int, stage, issueState string, deps ...int) db.Epic {
	return db.Epic{ID: SessionNameFor(issue), IssueNumber: issue, Stage: stage,
		IssueState: issueState, BlockedBy: deps}
}

func TestReadyEpics(t *testing.T) {
	epics := []db.Epic{
		qe(12, "merged", "closed"),
		qe(14, "merged", "closed", 12),
		qe(15, "escalated", "open", 12),
		qe(16, "implementing", "open", 14),
		qe(17, "queued", "open", 16),     // dep active → not ready
		qe(18, "queued", "open", 14),     // dep merged → ready
		qe(19, "queued", "open", 14, 15), // 15 escalated → not ready
		qe(20, "queued", "open", 99),     // unknown dep → blocked (fail closed)
		qe(21, "queued", "closed"),       // closed issue → never ready
		qe(22, "queued", "open"),         // no deps → ready
	}
	// capacity 2, one active (#16) → 1 slot; lowest issue number wins.
	got := ReadyEpics(epics, 2, false)
	if len(got) != 1 || got[0].IssueNumber != 18 {
		t.Fatalf("got %+v", got)
	}
	// capacity 3 → 2 slots → #18 and #22
	got = ReadyEpics(epics, 3, false)
	if len(got) != 2 || got[0].IssueNumber != 18 || got[1].IssueNumber != 22 {
		t.Fatalf("got %+v", got)
	}
	if len(ReadyEpics(epics, 2, true)) != 0 {
		t.Fatal("paused project must schedule nothing")
	}
	if len(ReadyEpics(epics, 1, false)) != 0 {
		t.Fatal("no capacity with one active epic at max_parallel=1")
	}
}

func TestKickoffAndProvider(t *testing.T) {
	if got := KickoffCommand("claude", 16); got != `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline 16"` {
		t.Fatalf("claude kickoff = %q", got)
	}
	if got := KickoffCommand("codex", 16); got != `codex -a never "/epic-pipeline 16"` {
		t.Fatalf("codex kickoff = %q", got)
	}
	if SessionNameFor(16) != "epic-16" {
		t.Fatal("session name")
	}
	if ProviderFor("claude", []string{"agent:codex"}) != "codex" {
		t.Fatal("label override to codex")
	}
	if ProviderFor("codex", []string{"agent:claude"}) != "claude" {
		t.Fatal("label override to claude")
	}
	if ProviderFor("claude", nil) != "claude" {
		t.Fatal("project default")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run 'TestReadyEpics|TestKickoff' -v`
Expected: FAIL — `ReadyEpics undefined`.

- [ ] **Step 3: Implement**

Create `hubd/internal/orchestrator/scheduler.go`:

```go
package orchestrator

import (
	"fmt"
	"sort"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// ReadyEpics computes which queued epics may start now. Pure function; deps
// are resolved against the passed slice (one project's epics). A blocked_by
// issue with no epic row BLOCKS — fail closed, same philosophy as the gate.
func ReadyEpics(epics []db.Epic, maxParallel int, paused bool) []db.Epic {
	if paused {
		return nil
	}
	byIssue := map[int]db.Epic{}
	active := 0
	for _, e := range epics {
		byIssue[e.IssueNumber] = e
		if activeStages[shared.EpicStage(e.Stage)] {
			active++
		}
	}
	capacity := maxParallel - active
	if capacity <= 0 {
		return nil
	}
	var ready []db.Epic
	for _, e := range epics {
		if e.Stage != string(shared.EpicQueued) || e.IssueState != "open" {
			continue
		}
		if !depsSatisfied(e, byIssue) {
			continue
		}
		ready = append(ready, e)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].IssueNumber < ready[j].IssueNumber })
	if len(ready) > capacity {
		ready = ready[:capacity]
	}
	return ready
}

func depsSatisfied(e db.Epic, byIssue map[int]db.Epic) bool {
	for _, dep := range e.BlockedBy {
		d, ok := byIssue[dep]
		if !ok {
			return false // unknown dep blocks
		}
		if d.Stage != string(shared.EpicMerged) && d.IssueState != "closed" {
			return false
		}
	}
	return true
}

// KickoffCommand is what the spawned tmux session runs (tmux executes it via
// `sh -c`, so the env prefix is fine). Runners MUST be autonomous — a
// permission prompt is a stalled epic: Claude needs IS_SANDBOX=1 (root host) +
// --dangerously-skip-permissions; Codex needs approval policy "never". The
// /epic-pipeline skill (sub-project 2) does the rest; exact codex invocation
// is validated there and only lives here.
func KickoffCommand(provider string, issue int) string {
	prompt := fmt.Sprintf("/epic-pipeline %d", issue)
	if provider == "codex" {
		return fmt.Sprintf(`codex -a never %q`, prompt)
	}
	return fmt.Sprintf(`IS_SANDBOX=1 claude --dangerously-skip-permissions %q`, prompt)
}

func SessionNameFor(issue int) string { return fmt.Sprintf("epic-%d", issue) }

// ProviderFor resolves the runner: per-epic agent:* label beats project default.
func ProviderFor(projectDefault string, labels []string) string {
	if hasLabel(labels, "agent:codex") {
		return "codex"
	}
	if hasLabel(labels, "agent:claude") {
		return "claude"
	}
	return projectDefault
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run 'TestReadyEpics|TestKickoff' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/orchestrator/ && git commit -m "feat(hub): dependency-aware scheduler + kickoff commands"
```

---

### Task 13: Registry client — DrainReports

**Files:**
- Modify: `hubd/internal/registry/client.go`
- Test: `hubd/internal/registry/client_test.go` (append; the file exists with the same httptest pattern)

**Interfaces:**
- Consumes: `shared.OrchestratorReport` (Task 5), existing `Client` internals (follow `Sessions` at `client.go:34` exactly: same bearer header, same target query param, same error style).
- Produces: `func (c *Client) DrainReports(ctx context.Context, srv db.Server, target string) ([]shared.OrchestratorReport, error)` — `GET {srv.URL}/orchestrator/reports?drain=1[&target=…]`; **HTTP 404 returns `(nil, nil)`** (agent predates sub-project 2 — tolerated, not an error).

- [ ] **Step 1: Write the failing test**

Append to `hubd/internal/registry/client_test.go` (match the file's existing fake-agent helper style; if it has a helper that builds an `httptest.Server` + `db.Server`, reuse it, else inline as below):

```go
func TestDrainReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orchestrator/reports" || r.URL.Query().Get("drain") != "1" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer btok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"repo":"o/r","epic":16,"stage":"implementing","session":"epic-16","ts":"t1"}]`))
	}))
	defer srv.Close()
	c := NewClient()
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "btok"}, "")
	if err != nil || len(got) != 1 || got[0].Epic != 16 || got[0].Stage != shared.EpicImplementing {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestDrainReportsOldAgent404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClient()
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "b"}, "")
	if err != nil || got != nil {
		t.Fatalf("404 must be tolerated: got %v err=%v", got, err)
	}
}
```

(Adjust `NewClient()` to the constructor's real signature — check the top of `client.go`; if it takes a timeout or http client, pass what existing tests pass.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/registry/ -run TestDrainReports -v`
Expected: FAIL — `c.DrainReports undefined`.

- [ ] **Step 3: Implement**

Append to `hubd/internal/registry/client.go` (mirror `Sessions`' request construction — bearer, target param, timeout):

```go
// DrainReports pulls-and-clears buffered orchestrator reports from an agent.
// 404 means the agent predates the reporter endpoint (sub-project 2): treated
// as "no reports", so mixed-fleet rollout is safe.
func (c *Client) DrainReports(ctx context.Context, srv db.Server, target string) ([]shared.OrchestratorReport, error) {
	u := srv.URL + "/orchestrator/reports?drain=1"
	if target != "" {
		u += "&target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	resp, err := c.http.Do(req) // use the same client field Sessions uses
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent reports: %d", resp.StatusCode)
	}
	var out []shared.OrchestratorReport
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
```

(If the unexported http client field has a different name in `client.go`, use that name; add missing imports `net/url`, `encoding/json`, `fmt` as needed.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/registry/ -v`
Expected: PASS (existing + 2 new).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/registry/ && git commit -m "feat(hub): drain orchestrator reports from agents"
```

---

### Task 14: Sync — blocked-by parser, issue→epic mirror, reconcile

**Files:**
- Create: `hubd/internal/orchestrator/sync.go`
- Test: `hubd/internal/orchestrator/sync_test.go`

**Interfaces:**
- Consumes: `github.Issue` (Task 6), `db.Epic`/`db.Project` (Tasks 3–4).
- Produces (used by run loop Task 15 and webhook handler Task 17):
  - `func ParseBlockedBy(body string) []int` — matches `Blocked by #13`, `Blocked-by: #12, #14`, case-insensitive, dedup, sorted
  - `func IsOrchestratedIssue(labels []string) bool` — has `agentmon:epic` OR `agentmon:run`
  - `func EpicFromIssue(p db.Project, is github.Issue, now string) db.Epic` — fills mirror fields (`ProjectID, IssueNumber, Title, Labels, BlockedBy, IssueState, QueuedAt: now, StageUpdatedAt: now`)

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/sync_test.go`:

```go
package orchestrator

import (
	"reflect"
	"testing"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
)

func TestParseBlockedBy(t *testing.T) {
	cases := []struct {
		body string
		want []int
	}{
		{"Blocked by #13", []int{13}},
		{"blocked-by: #12, #14", []int{12, 14}},
		{"Blocked by #14 and blocked by #12\nBlocked by #14", []int{12, 14}},
		{"nothing here", nil},
		{"#7 mentioned but not a dep", nil},
	}
	for _, c := range cases {
		if got := ParseBlockedBy(c.body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q → %v, want %v", c.body, got, c.want)
		}
	}
}

func TestIsOrchestratedIssue(t *testing.T) {
	if !IsOrchestratedIssue([]string{"agentmon:epic"}) || !IsOrchestratedIssue([]string{"bug", "agentmon:run"}) {
		t.Fatal("epic/run labels must qualify")
	}
	if IsOrchestratedIssue([]string{"bug"}) || IsOrchestratedIssue(nil) {
		t.Fatal("unlabeled issues must not qualify")
	}
}

func TestEpicFromIssue(t *testing.T) {
	p := db.Project{ID: "p1"}
	is := github.Issue{Number: 15, Title: "GDPR", Body: "Blocked by #13", State: "open",
		Labels: []string{"agentmon:epic", "pr-gate"}}
	e := EpicFromIssue(p, is, "t0")
	if e.ProjectID != "p1" || e.IssueNumber != 15 || e.IssueState != "open" ||
		e.QueuedAt != "t0" || e.StageUpdatedAt != "t0" {
		t.Fatalf("got %+v", e)
	}
	if len(e.BlockedBy) != 1 || e.BlockedBy[0] != 13 || len(e.Labels) != 2 {
		t.Fatalf("got %+v", e)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run 'TestParseBlockedBy|TestIsOrchestrated|TestEpicFromIssue' -v`
Expected: FAIL — `ParseBlockedBy undefined`.

- [ ] **Step 3: Implement**

Create `hubd/internal/orchestrator/sync.go`:

```go
package orchestrator

import (
	"regexp"
	"sort"
	"strconv"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
)

// blockedByRe matches the body convention: "Blocked by #13" / "Blocked-by: #12, #14".
// (GitHub's native issue-relationships API can replace this later; the body
// convention is the v1 contract the import script writes.)
var blockedByRe = regexp.MustCompile(`(?i)blocked[ -]by:?\s*((?:#\d+[,\s]*)+)`)
var issueRefRe = regexp.MustCompile(`#(\d+)`)

func ParseBlockedBy(body string) []int {
	seen := map[int]bool{}
	for _, m := range blockedByRe.FindAllStringSubmatch(body, -1) {
		for _, ref := range issueRefRe.FindAllStringSubmatch(m[1], -1) {
			if n, err := strconv.Atoi(ref[1]); err == nil {
				seen[n] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// IsOrchestratedIssue: only labeled issues enter the mirror — the orchestrator
// never touches issues it wasn't pointed at.
func IsOrchestratedIssue(labels []string) bool {
	return hasLabel(labels, "agentmon:epic") || hasLabel(labels, "agentmon:run")
}

func EpicFromIssue(p db.Project, is github.Issue, now string) db.Epic {
	return db.Epic{
		ProjectID:      p.ID,
		IssueNumber:    is.Number,
		Title:          is.Title,
		Labels:         is.Labels,
		BlockedBy:      ParseBlockedBy(is.Body),
		IssueState:     is.State,
		QueuedAt:       now,
		StageUpdatedAt: now,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -v`
Expected: PASS (all orchestrator tests so far).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): issue mirror sync helpers"
```

---

### Task 15: Orchestrator core — deps, run loop, tick pipeline

**Files:**
- Create: `hubd/internal/orchestrator/orchestrator.go`
- Test: `hubd/internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces (Tasks 16–19 and `main.go` consume):

```go
type GitHubAPI interface {
	GetIssue(ctx context.Context, repo string, num int) (github.Issue, error)
	ListIssuesSince(ctx context.Context, repo, since string) ([]github.Issue, error)
	GetPullRequest(ctx context.Context, repo string, num int) (github.PullRequest, error)
	ListCheckRuns(ctx context.Context, repo, ref string) ([]github.CheckRun, error)
	MergePR(ctx context.Context, repo string, num int, sha string) error // sha pins the evaluated head (checkpoint-2 review)
	CreateIssueComment(ctx context.Context, repo string, num int, body string) error
	AddLabels(ctx context.Context, repo string, num int, labels []string) error
	RemoveLabel(ctx context.Context, repo string, num int, label string) error
}

type AgentAPI interface {
	CreateSession(ctx context.Context, srv db.Server, target string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error)
	DrainReports(ctx context.Context, srv db.Server, target string) ([]shared.OrchestratorReport, error)
}

type ServerGetter interface {
	Get(ctx context.Context, id string) (db.Server, bool, error)
}

type LivenessAPI interface {
	Session(server, target, session string) (state.SessionView, bool)
}

type Deps struct {
	DB     *db.DB
	GH     GitHubAPI
	Agents AgentAPI
	Reg    ServerGetter
	Live   LivenessAPI
	Bcast  *BoardBroadcaster
	Cfg    config.OrchestratorCfg
	Now    func() string // RFC3339 UTC
}

func New(d Deps) *Orchestrator
func (o *Orchestrator) Run(ctx context.Context)   // reconcile, then tick/wake loop
func (o *Orchestrator) Wake()                      // non-blocking nudge
func (o *Orchestrator) Tick(ctx context.Context)   // exported for tests + webhook-driven runs
// Action methods (Task 18 wires these to HTTP):
func (o *Orchestrator) Approve(ctx context.Context, epicID, source string) error   // escalated → merging → merge → merged
func (o *Orchestrator) Retry(ctx context.Context, epicID, source string) error     // escalated|stalled → queued
func (o *Orchestrator) Cancel(ctx context.Context, epicID, source string) error
func (o *Orchestrator) RunIssue(ctx context.Context, projectID string, issue int) error // fetch + mirror + wake
func (o *Orchestrator) IngestWebhook(ctx context.Context, ev github.Event) error   // mirror + wake
```

Tick pipeline per project (order matters):
1. `syncProject` — `ListIssuesSince(repo, watermark)`; watermark is in-memory (`map[projectID]string`, empty on boot ⇒ full list); upsert every `IsOrchestratedIssue`.
2. `drainReports` — resolve server via `Reg.Get(p.ServerID)`; for each report: match epic by issue in this project; **reject** (log, skip) if `!shared.ReportableStage(stage)`, or session mismatch (`report.Session != "" && epic.SessionName != "" && report.Session != epic.SessionName`), or `!ValidTransition`; else transition (source `report`), `SetEpicPR` when `report.PR > 0`, `SetEpicNeeds(note)` before an `escalated` transition, publish.
3. `checkStalls` — for epics in `starting/planning/implementing/reviewing`: session gone from `Live.Session(...)` (grace: skip if `StageUpdatedAt` < 2 ticks old) OR stage age > per-stage timeout ⇒ transition `stalled` (needs = reason), publish. `starting` uses `PlanningTimeout`.
4. `evaluateGates` — for epics in `pr_open`/`merging`: `GetPullRequest`; if `Merged` ⇒ transition `merged` (source `github`), write-back label `agentmon:merged` + comment, publish. Else `Decide(GateInput{Verdict: ParseVerdict(pr.Body)…, Labels: epic.Labels, RequiredReviews: p.RequiredReviews, Checks…: ChecksState(ListCheckRuns(repo, pr.HeadSHA))})`: Merge ⇒ transition `merging`, `MergePR` (on `ErrNotMergeable` ⇒ escalate "merge conflict"), then transition `merged` + write-back + publish; Wait ⇒ nothing; else ⇒ `SetEpicNeeds(reason)`, transition `escalated` (source `hub`), comment reason on issue, publish. Also `SetEpicPR(pr.Number, pr.HeadRef)` and `SetEpicVerdict` with the parsed verdict re-marshaled as JSON.
5. `schedule` — `ReadyEpics(...)`; for each: `attempt+1 > Cfg.MaxAttempts` ⇒ transition `failed`; else transition `queued→starting`, `SetEpicAssignment(SessionNameFor(issue), attempt+1)`, `CreateSession(srv, p.Target, {Name: SessionNameFor(issue), Cwd: p.Workdir, Command: KickoffCommand(ProviderFor(p.Provider, labels), issue)})`; spawn error ⇒ transition `starting→stalled` (needs = "spawn failed: …"); publish either way.
- Every GH write-back is best-effort: `log.Printf` on error, never fail the tick.
- `reconcile` (before first tick): for each non-terminal epic with `PRNumber > 0`: PR merged ⇒ `merged`; PR closed unmerged ⇒ `canceled` (source `github`, note "reconcile").
- All stage moves go through one funnel `o.transition(ctx, epic, to, source, note)` → `ValidTransition` check + `db.TransitionEpic` + `Bcast.Publish(BoardChange{…, Needs: epic-after.Needs})`.

- [ ] **Step 1: Write the failing test (fakes + three scenarios)**

Create `hubd/internal/orchestrator/orchestrator_test.go`:

```go
package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// ---- fakes ----

type fakeGH struct {
	issues  map[int]github.Issue
	prs     map[int]github.PullRequest
	checks  map[string][]github.CheckRun
	merged  []int
	labels  [][2]string // [issue-or-pr, label]
	comments []string
}

func (f *fakeGH) GetIssue(_ context.Context, _ string, n int) (github.Issue, error) {
	return f.issues[n], nil
}
func (f *fakeGH) ListIssuesSince(_ context.Context, _, _ string) ([]github.Issue, error) {
	var out []github.Issue
	for _, is := range f.issues {
		out = append(out, is)
	}
	return out, nil
}
func (f *fakeGH) GetPullRequest(_ context.Context, _ string, n int) (github.PullRequest, error) {
	return f.prs[n], nil
}
func (f *fakeGH) ListCheckRuns(_ context.Context, _, ref string) ([]github.CheckRun, error) {
	return f.checks[ref], nil
}
func (f *fakeGH) MergePR(_ context.Context, _ string, n int, _ string) error {
	f.merged = append(f.merged, n)
	pr := f.prs[n]
	pr.Merged = true
	f.prs[n] = pr
	return nil
}
func (f *fakeGH) CreateIssueComment(_ context.Context, _ string, _ int, body string) error {
	f.comments = append(f.comments, body)
	return nil
}
func (f *fakeGH) AddLabels(_ context.Context, _ string, n int, ls []string) error {
	for _, l := range ls {
		f.labels = append(f.labels, [2]string{SessionNameFor(n), l})
	}
	return nil
}
func (f *fakeGH) RemoveLabel(_ context.Context, _ string, _ int, _ string) error { return nil }

type fakeAgents struct {
	created []shared.CreateSessionRequest
	reports []shared.OrchestratorReport
	spawnErr error
}

func (f *fakeAgents) CreateSession(_ context.Context, _ db.Server, _ string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error) {
	if f.spawnErr != nil {
		return shared.CreateSessionResponse{}, f.spawnErr
	}
	f.created = append(f.created, req)
	return shared.CreateSessionResponse{Name: req.Name}, nil
}
func (f *fakeAgents) DrainReports(_ context.Context, _ db.Server, _ string) ([]shared.OrchestratorReport, error) {
	out := f.reports
	f.reports = nil
	return out, nil
}

type fakeReg struct{}

func (fakeReg) Get(_ context.Context, id string) (db.Server, bool, error) {
	return db.Server{ID: id, URL: "http://a", Bearer: "b", Status: "active"}, true, nil
}

type fakeLive struct{ alive map[string]bool }

func (f fakeLive) Session(_, _, name string) (state.SessionView, bool) {
	return state.SessionView{Session: name}, f.alive[name]
}

func newTestOrch(t *testing.T, gh *fakeGH, ag *fakeAgents, live fakeLive) (*Orchestrator, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := d.EnrollServer(ctx, db.Server{ID: "h1", Name: "h1", Hostname: "h1",
		URL: "http://a", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProject(ctx, db.Project{ID: "p1", Name: "proj", Repo: "o/r",
		ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude",
		RequiredReviews: []string{"codex"}, MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	clock := "2026-07-10T14:00:00Z"
	o := New(Deps{DB: d, GH: gh, Agents: ag, Reg: fakeReg{}, Live: live,
		Bcast: NewBoardBroadcaster(),
		Cfg:   config.OrchestratorCfg{MaxAttempts: 2},
		Now:   func() string { return clock }})
	return o, d
}

func TestTickSyncsAndSpawns(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{
		16: {Number: 16, Title: "Curriculum", State: "open", Labels: []string{"agentmon:epic"}},
		99: {Number: 99, Title: "unlabeled", State: "open"},
	}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	ctx := context.Background()
	o.Tick(ctx)
	e, err := d.GetEpicByIssue(ctx, "p1", 16)
	if err != nil {
		t.Fatal(err)
	}
	if e.Stage != "starting" || e.SessionName != "epic-16" || e.Attempt != 1 {
		t.Fatalf("epic = %+v", e)
	}
	if len(ag.created) != 1 || ag.created[0].Command != `claude "/epic-pipeline 16"` || ag.created[0].Cwd != "/w" {
		t.Fatalf("created = %+v", ag.created)
	}
	if _, err := d.GetEpicByIssue(ctx, "p1", 99); err == nil {
		t.Fatal("unlabeled issue must not be mirrored")
	}
}

func TestReportsAdvanceAndGateMerges(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 1, resolved: 1, unresolved: 0}\ntests: {passed: 5, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s", HeadRef: "epic/16-x"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{"epic-16": true}})
	ctx := context.Background()
	o.Tick(ctx) // sync + spawn → starting
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: "epic-16", Ts: "t"},
		{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: "epic-16", Ts: "t"},
	}
	o.Tick(ctx) // drain → pr_open, then gate → merged
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" || e.PRNumber != 61 || e.Branch != "epic/16-x" {
		t.Fatalf("epic = %+v", e)
	}
	if len(gh.merged) != 1 || gh.merged[0] != 61 {
		t.Fatalf("merged = %v", gh.merged)
	}
}

func TestGateEscalatesOnUnresolvedAndApproveRecovers(t *testing.T) {
	verdictBody := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 3, resolved: 1, unresolved: 2}\ntests: {passed: 5, failed: 0}\n" +
		"uncertain: false\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictBody, HeadSHA: "s"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{"epic-16": true}})
	ctx := context.Background()
	o.Tick(ctx)
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: "epic-16", Ts: "t"}}
	o.Tick(ctx)
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "escalated" || e.Needs == "" {
		t.Fatalf("epic = %+v", e)
	}
	if err := o.Approve(ctx, e.ID, "user:admin"); err != nil {
		t.Fatal(err)
	}
	e, _ = d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "merged" || len(gh.merged) != 1 {
		t.Fatalf("after approve: %+v merged=%v", e, gh.merged)
	}
}

func TestStallOnDeadSession(t *testing.T) {
	gh := &fakeGH{issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}}}
	ag := &fakeAgents{}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}}) // session never alive
	ctx := context.Background()
	o.Tick(ctx) // spawn → starting
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 16, Stage: shared.EpicImplementing, Session: "epic-16", Ts: "t"}}
	o.Tick(ctx)
	o.Tick(ctx) // grace tick passed; session still gone → stalled
	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "stalled" {
		t.Fatalf("epic = %+v", e)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run 'TestTick|TestReports|TestGate|TestStall' -v`
Expected: FAIL — `New undefined`.

- [ ] **Step 3: Implement**

Create `hubd/internal/orchestrator/orchestrator.go`. This is the largest file (~300 lines); the structure is fixed by the interfaces block above and the tick pipeline order. Key implementation notes an engineer must follow:

```go
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// (interfaces exactly as in the task header: GitHubAPI, AgentAPI, ServerGetter, LivenessAPI, Deps)

type Orchestrator struct {
	d          Deps
	wake       chan struct{}
	watermarks map[string]string // projectID → last sync time (in-memory; boot = full sync)
	stallSeen  map[string]int    // epicID → consecutive ticks with dead session
}

func New(d Deps) *Orchestrator {
	return &Orchestrator{d: d, wake: make(chan struct{}, 1),
		watermarks: map[string]string{}, stallSeen: map[string]int{}}
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
	if tick == 0 {
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

func (o *Orchestrator) Tick(ctx context.Context) {
	projects, err := o.d.DB.ListProjects(ctx)
	if err != nil {
		log.Printf("orchestrator: list projects: %v", err)
		return
	}
	for _, p := range projects {
		o.tickProject(ctx, p)
	}
}

func (o *Orchestrator) tickProject(ctx context.Context, p db.Project) {
	now := o.d.Now()
	if err := o.syncProject(ctx, p, now); err != nil {
		log.Printf("orchestrator[%s]: sync: %v", p.Name, err)
	}
	o.drainReports(ctx, p)
	o.checkStalls(ctx, p, now)
	o.evaluateGates(ctx, p)
	o.schedule(ctx, p)
}
```

`syncProject`: `since := o.watermarks[p.ID]`; `ListIssuesSince(p.Repo, since)`; for each issue with `IsOrchestratedIssue(is.Labels)` → `UpsertEpicIssue(EpicFromIssue(p, is, now))`; set watermark to `now` on success.

`drainReports`: `srv, ok, err := o.d.Reg.Get(ctx, p.ServerID)`; skip on !ok/err. For each report from `o.d.Agents.DrainReports(ctx, srv, p.Target)`:

```go
	e, err := o.d.DB.GetEpicByIssue(ctx, p.ID, r.Epic)
	if err != nil || !shared.ReportableStage(r.Stage) {
		log.Printf("orchestrator[%s]: dropped report %+v: %v", p.Name, r, err)
		continue
	}
	if r.Session != "" && e.SessionName != "" && r.Session != e.SessionName {
		log.Printf("orchestrator[%s]: report session mismatch: %q != %q", p.Name, r.Session, e.SessionName)
		continue
	}
	if r.PR > 0 {
		o.d.DB.SetEpicPR(ctx, e.ID, r.PR, e.Branch)
	}
	if r.Stage == shared.EpicEscalated && r.Note != "" {
		o.d.DB.SetEpicNeeds(ctx, e.ID, r.Note)
	}
	o.transition(ctx, e, r.Stage, "report", r.Note)
```

`checkStalls` (per epic in `starting/planning/implementing/reviewing`): dead-session grace = 2 consecutive ticks (`stallSeen`); stage-age timeout compares `time.Parse(time.RFC3339, e.StageUpdatedAt)` against `Now()` with `PlanningTimeout` (starting+planning) / `ImplementingTimeout` / `ReviewingTimeout`; zero timeouts (as in tests) mean "no timeout". On stall: `SetEpicNeeds(reason)` then `o.transition(ctx, e, shared.EpicStalled, "hub", reason)`.

`evaluateGates` (per epic in `pr_open` or `merging`, needing `PRNumber > 0`):

```go
	pr, err := o.d.GH.GetPullRequest(ctx, p.Repo, e.PRNumber)
	if err != nil { log; continue }
	if pr.HeadRef != "" && e.Branch != pr.HeadRef {
		o.d.DB.SetEpicPR(ctx, e.ID, pr.Number, pr.HeadRef)
		e.Branch = pr.HeadRef
	}
	if pr.Merged {
		o.finishMerged(ctx, p, e, "github") // transition + label/comment write-back
		continue
	}
	if pr.State == "closed" {
		o.transition(ctx, e, shared.EpicCanceled, "github", "PR closed without merge")
		continue
	}
	v, verr := ParseVerdict(pr.Body)
	if v != nil {
		if b, err := json.Marshal(v); err == nil {
			o.d.DB.SetEpicVerdict(ctx, e.ID, string(b))
		}
	}
	runs, err := o.d.GH.ListCheckRuns(ctx, p.Repo, pr.HeadSHA)
	if err != nil { log; continue }
	green, pending := github.ChecksState(runs)
	res := Decide(GateInput{Verdict: v, VerdictErr: verr, Epic: e.IssueNumber, Labels: e.Labels,
		RequiredReviews: p.RequiredReviews, ChecksGreen: green, ChecksPending: pending})
	switch {
	case res.Wait:
	case res.Merge:
		o.mergeEpic(ctx, p, e, "hub")
	default:
		o.d.DB.SetEpicNeeds(ctx, e.ID, res.Reason)
		if o.transition(ctx, e, shared.EpicEscalated, "hub", res.Reason) {
			o.comment(ctx, p, e.IssueNumber, "⚠ escalated: "+res.Reason)
		}
	}
```

`mergeEpic(ctx, p, e, source)`: transition current→`merging` (source); `MergePR(ctx, p.Repo, e.PRNumber, pr.HeadSHA)` (SHA-pinned); `ErrNotMergeable` ⇒ `SetEpicNeeds` + transition `merging→escalated` "merge conflict — rebase needed"; other error ⇒ log, leave in `merging` (next tick re-fetches PR: merged-by-now or retry); success ⇒ `finishMerged`. `finishMerged`: transition to `merged` (via `merging` if legal path requires — from `merging` directly); best-effort `AddLabels(repo, issue, ["agentmon:merged"])` + `comment("✅ merged PR #N")`.

`schedule`: `epics, _ := ListEpicsByProject`; `for _, e := range ReadyEpics(epics, p.MaxParallel, p.Paused)`: if `e.Attempt+1 > o.d.Cfg.MaxAttempts` ⇒ transition `failed` "attempts exhausted"; else `srv := Reg.Get`; transition `queued→starting` "spawning epic-N"; `SetEpicAssignment(SessionNameFor(e.IssueNumber), e.Attempt+1)`; `CreateSession(ctx, srv, p.Target, shared.CreateSessionRequest{Name: SessionNameFor(e.IssueNumber), Cwd: p.Workdir, Command: KickoffCommand(ProviderFor(p.Provider, e.Labels), e.IssueNumber)})`; on error ⇒ `SetEpicNeeds` + transition `starting→stalled` "spawn failed: …".

The single transition funnel:

```go
// transition validates, persists, and publishes one stage move. Returns
// whether the move happened (guards races: stale `from` loses silently).
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
	after, err := o.d.DB.GetEpic(ctx, e.ID)
	if err != nil {
		after = e
		after.Stage = string(to)
	}
	if o.d.Bcast != nil {
		o.d.Bcast.Publish(BoardChange{ProjectID: e.ProjectID, EpicID: e.ID,
			Issue: e.IssueNumber, Stage: to, Needs: after.Needs, Title: e.Title})
	}
	return true
}
```

Action methods: `Approve` = load epic; must be `escalated`; `mergeEpic(…, "user:"+sourceID)` (requires `PRNumber > 0`, else error "no PR to merge"). `Retry` = `escalated|stalled → queued` (source `user`). `Cancel` = any non-terminal → `canceled`. `RunIssue` = `GetIssue` + force-add `agentmon:run` to its label set locally + `UpsertEpicIssue` + `Wake()`. `IngestWebhook` = for `issues` events with orchestrated labels: upsert via `GetProjectByRepo(ev.Repo)`; for `pull_request`/`check_suite`: just `Wake()`.

`reconcile`: `ListNonTerminalEpics`; group by project; for `PRNumber > 0`: fetch PR; merged ⇒ transition to `merged` (note "reconcile"); closed ⇒ `canceled`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -v -race`
Expected: PASS — all orchestrator tests including the four new scenario tests.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/orchestrator/ && git commit -m "feat(hub): orchestrator core loop — sync, reports, stalls, gate, schedule"
```

- [ ] **Step 6: CHECKPOINT 3 — STOP**

Tasks 12–15 (scheduler, report drain, sync, core loop — the highest-judgment code in the plan) are a review checkpoint. STOP here — do not begin Task 16. Report that checkpoint 3 is reached, listing completed tasks and the final `go test ./...` result. Resume only on an explicit "continue" or after applying explicit fix instructions from the checkpoint review.

---

### Task 16: Authz actions, audit methods, board push dispatcher

**Files:**
- Modify: `hubd/internal/authz/authz.go` (two consts)
- Modify: `hubd/internal/audit/audit.go` (three methods)
- Create: `hubd/internal/orchestrator/push.go`
- Test: `hubd/internal/orchestrator/push_test.go`, extend `hubd/internal/audit/audit_test.go` if present (else skip audit test — methods mirror existing tested pattern)

**Interfaces:**
- Produces:
  - `authz.OrchestratorView Action = "orchestrator.view"`, `authz.OrchestratorControl Action = "orchestrator.control"`
  - `audit.Recorder` methods:
    - `ProjectRegister(ctx, principalID, resource, repo, ip, ua string)` — action `project.register`, meta `{"repo": repo}`
    - `EpicAction(ctx, principalID, resource, action, epicID, ip, ua string)` — action `epic.<action>` (approve/retry/cancel/pause/resume/run_issue/max_parallel), meta `{"epic": epicID}`
    - `EpicMerge(ctx, principalID, resource string, issue, pr int)` — action `epic.merge`, result allow, meta `{"issue": "...", "pr": "..."}` (principal `"orchestrator"` for autonomous merges; IP/UA empty)
  - `orchestrator.RunBoardPushDispatcher(ctx context.Context, d BoardPushDeps)` with

```go
type BoardPushDeps struct {
	Bcast    *BoardBroadcaster
	Presence *state.Presence
	Store    state.PushDispatchStore
	Send     state.PushSender
	Now      func() string
}
```

Behavior: subscribe to `Bcast`; fire ONLY on `Stage == EpicEscalated || EpicStalled`; payload `{"type":"epic","stage":…,"project":…,"epic":<issue>,"title":…,"needs":…,"ts":…}`; same presence-suppression and 404/410-prune semantics as `state.RunPushDispatcher` (`state/push_dispatcher.go:90-150`) — copy its dispatch skeleton, drop the blocked-gate (board transitions are already edge-triggered: `transition()` publishes once per move).

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/orchestrator/push_test.go`:

```go
package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type fakePushStore struct{}

func (fakePushStore) PrincipalIDsWithSubscriptions(ctx context.Context) ([]string, error) {
	return []string{"admin"}, nil
}
func (fakePushStore) ListSubscriptionsForPrincipal(ctx context.Context, id string) ([]db.PushSubscription, error) {
	return []db.PushSubscription{{PrincipalID: id, Endpoint: "https://push/x"}}, nil
}
func (fakePushStore) DeleteSubscription(ctx context.Context, endpoint string) error { return nil }

func TestBoardPushFiresOnEscalatedOnly(t *testing.T) {
	b := NewBoardBroadcaster()
	var mu sync.Mutex
	var payloads []map[string]any
	send := func(_ context.Context, _ db.PushSubscription, payload []byte) (int, error) {
		var m map[string]any
		json.Unmarshal(payload, &m)
		mu.Lock()
		payloads = append(payloads, m)
		mu.Unlock()
		return 201, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunBoardPushDispatcher(ctx, BoardPushDeps{
		Bcast: b, Presence: state.NewPresence(), Store: fakePushStore{},
		Send: send, Now: func() string { return "t" }})
	time.Sleep(20 * time.Millisecond) // let it subscribe
	b.Publish(BoardChange{ProjectID: "p1", Issue: 15, Stage: shared.EpicMerged})
	b.Publish(BoardChange{ProjectID: "p1", Issue: 15, Stage: shared.EpicEscalated, Needs: "2 findings", Title: "GDPR"})
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no push within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 || payloads[0]["type"] != "epic" || payloads[0]["stage"] != "escalated" {
		t.Fatalf("payloads = %+v", payloads)
	}
}
```

(Check `state.NewPresence()`'s real constructor name in `state/presence.go` before running; adjust if it differs.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestBoardPush -v`
Expected: FAIL — `RunBoardPushDispatcher undefined`.

- [ ] **Step 3: Implement**

(a) `authz/authz.go` — append to the const block:

```go
	OrchestratorView    Action = "orchestrator.view"
	OrchestratorControl Action = "orchestrator.control"
```

(b) `audit/audit.go` — append (mirror `SessionKill`'s shape):

```go
func (r *Recorder) ProjectRegister(ctx context.Context, principalID, resource, repo, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"repo": repo})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "project.register",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}

func (r *Recorder) EpicAction(ctx context.Context, principalID, resource, action, epicID, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"epic": epicID})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "epic." + action,
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}

func (r *Recorder) EpicMerge(ctx context.Context, principalID, resource string, issue, pr int) {
	meta, err := json.Marshal(map[string]string{
		"issue": strconv.Itoa(issue), "pr": strconv.Itoa(pr)})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "epic.merge",
		Resource: resource, Result: "allow", Meta: string(meta)})
}
```

(add `strconv` import). Wire `EpicMerge` into `Orchestrator.mergeEpic` via a new optional `Deps.Audit *audit.Recorder` field: principal `"orchestrator"` for `source == "hub"`, the user id for `Approve`. Guard `if o.d.Audit != nil`.

(c) Create `hubd/internal/orchestrator/push.go`:

```go
package orchestrator

import (
	"context"
	"encoding/json"
	"log"

	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type BoardPushDeps struct {
	Bcast    *BoardBroadcaster
	Presence *state.Presence
	Store    state.PushDispatchStore
	Send     state.PushSender
	Now      func() string
}

// RunBoardPushDispatcher pushes on escalated/stalled board changes. Transitions
// are edge-triggered at the publisher, so no per-episode gate is needed here.
func RunBoardPushDispatcher(ctx context.Context, d BoardPushDeps) {
	_, ch, cancel := d.Bcast.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-ch:
			if !ok {
				return
			}
			if c.Stage != shared.EpicEscalated && c.Stage != shared.EpicStalled {
				continue
			}
			go dispatchBoardPush(ctx, d, c)
		}
	}
}

func dispatchBoardPush(ctx context.Context, d BoardPushDeps, c BoardChange) {
	payload, err := json.Marshal(map[string]any{
		"type": "epic", "stage": string(c.Stage), "project": c.ProjectID,
		"epic": c.Issue, "title": c.Title, "needs": c.Needs, "ts": d.Now(),
	})
	if err != nil {
		return
	}
	ids, err := d.Store.PrincipalIDsWithSubscriptions(ctx)
	if err != nil {
		log.Printf("board push: principals: %v", err)
		return
	}
	for _, id := range ids {
		if d.Presence != nil && d.Presence.Online(id) {
			continue // live SSE page → in-app alert covers it
		}
		subs, err := d.Store.ListSubscriptionsForPrincipal(ctx, id)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			status, err := d.Send(ctx, sub, payload)
			if err != nil {
				log.Printf("board push: send: %v", err)
				continue
			}
			if status == 404 || status == 410 {
				_ = d.Store.DeleteSubscription(ctx, sub.Endpoint)
			}
		}
	}
}
```

(Confirm `state.PushDispatchStore` / `state.PushSender` / `Presence.Online` names against `state/push_dispatcher.go:21-38` — they are per the exploration report; if `PushDispatchStore` is unexported or shaped differently, define a local equivalent interface with the same three methods.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ ./internal/audit/ -v -race && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/ && git commit -m "feat(hub): orchestrator authz actions, audit methods, board push dispatcher"
```

---

### Task 17: API — GitHub webhook endpoint

**Files:**
- Create: `hubd/internal/api/orchestrator_webhook.go`
- Modify: `hubd/internal/api/router.go` (one route), `hubd/internal/api/servers.go` (Deps fields)
- Test: `hubd/internal/api/orchestrator_webhook_test.go`

**Interfaces:**
- Consumes: `github.VerifySignature`, `github.ParseEvent` (Task 7), `orchestrator.Orchestrator.IngestWebhook` (Task 15).
- Produces: `func (d Deps) GitHubWebhookHandler() http.HandlerFunc`; route `POST /api/v1/github/webhook` — registered **without** `RequireAuth` (GitHub can't log in): `mux.Handle("POST /api/v1/github/webhook", rd.API.GitHubWebhookHandler())`. Auth = HMAC only. New `Deps` fields: `WebhookSecret string`, `Orch OrchestratorAPI` where

```go
// in api package — keeps api decoupled from the orchestrator concrete type
type OrchestratorAPI interface {
	IngestWebhook(ctx context.Context, ev github.Event) error
	Wake()
	Approve(ctx context.Context, epicID, source string) error
	Retry(ctx context.Context, epicID, source string) error
	Cancel(ctx context.Context, epicID, source string) error
	RunIssue(ctx context.Context, projectID string, issue int) error
}
```

Handler flow: read body (`http.MaxBytesReader`, 1<<20); `VerifySignature(d.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256"))` else 403; `kind := r.Header.Get("X-GitHub-Event")`; `ping` ⇒ 200 `{"ok":"pong"}`; `ParseEvent` ⇒ 400 on error; `d.Orch.IngestWebhook` (log-only on error) ⇒ 202 `{"ok":"accepted"}`.

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/api/orchestrator_webhook_test.go` (white-box `package api`, same style as existing handler tests — check one existing `*_test.go` in the package for the Deps construction helper and reuse it):

```go
package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/hubd/internal/github"
)

type fakeOrch struct {
	ingested []github.Event
	woke     int
}

func (f *fakeOrch) IngestWebhook(_ context.Context, ev github.Event) error {
	f.ingested = append(f.ingested, ev)
	return nil
}
func (f *fakeOrch) Wake()                                                  { f.woke++ }
func (f *fakeOrch) Approve(context.Context, string, string) error          { return nil }
func (f *fakeOrch) Retry(context.Context, string, string) error            { return nil }
func (f *fakeOrch) Cancel(context.Context, string, string) error           { return nil }
func (f *fakeOrch) RunIssue(context.Context, string, int) error            { return nil }

func signBody(secret string, b []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(b)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	d := Deps{WebhookSecret: "s", Orch: &fakeOrch{}}
	body := []byte(`{}`)
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	r.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()
	d.GitHubWebhookHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestWebhookAcceptsSignedIssuesEvent(t *testing.T) {
	fo := &fakeOrch{}
	d := Deps{WebhookSecret: "s", Orch: fo}
	body := []byte(`{"action":"labeled","repository":{"full_name":"o/r"},
	  "issue":{"number":15,"state":"open","labels":[{"name":"agentmon:epic"}]}}`)
	r := httptest.NewRequest("POST", "/api/v1/github/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", signBody("s", body))
	r.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()
	d.GitHubWebhookHandler()(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if len(fo.ingested) != 1 || fo.ingested[0].Issue.Number != 15 {
		t.Fatalf("ingested = %+v", fo.ingested)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestWebhook -v`
Expected: FAIL — `Deps` has no `WebhookSecret`/`Orch`, handler undefined.

- [ ] **Step 3: Implement**

Add to `Deps` in `hubd/internal/api/servers.go` (with the `OrchestratorAPI` interface defined above it):

```go
	// Orchestrator (nil-safe: routes 503 when unconfigured)
	Orch          OrchestratorAPI
	WebhookSecret string
```

Create `hubd/internal/api/orchestrator_webhook.go`:

```go
package api

import (
	"context"
	"io"
	"log"
	"net/http"

	"agentmon/hubd/internal/github"
)

type OrchestratorAPI interface {
	IngestWebhook(ctx context.Context, ev github.Event) error
	Wake()
	Approve(ctx context.Context, epicID, source string) error
	Retry(ctx context.Context, epicID, source string) error
	Cancel(ctx context.Context, epicID, source string) error
	RunIssue(ctx context.Context, projectID string, issue int) error
}

const maxWebhookBody = 1 << 20

// GitHubWebhookHandler is PUBLIC (GitHub cannot hold a session cookie);
// authentication is the HMAC signature — which fails closed on empty secret.
func (d Deps) GitHubWebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if !github.VerifySignature(d.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
			writeJSONError(w, http.StatusForbidden, "bad signature")
			return
		}
		kind := r.Header.Get("X-GitHub-Event")
		if kind == "ping" {
			writeJSON(w, http.StatusOK, map[string]string{"ok": "pong"})
			return
		}
		ev, err := github.ParseEvent(kind, body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "unparseable event")
			return
		}
		if err := d.Orch.IngestWebhook(r.Context(), ev); err != nil {
			log.Printf("webhook ingest: %v", err)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"ok": "accepted"})
	}
}
```

Register in `router.go` next to the other routes (NOT wrapped in `RequireAuth`):

```go
	mux.Handle("POST /api/v1/github/webhook", rd.API.GitHubWebhookHandler())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestWebhook -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/api/ && git commit -m "feat(hub): public hmac-authenticated github webhook endpoint"
```

---

### Task 18: API — projects, board state, actions

**Files:**
- Create: `hubd/internal/api/orchestrator.go`
- Modify: `hubd/internal/api/router.go` (four routes)
- Test: `hubd/internal/api/orchestrator_test.go`

**Interfaces:**
- Consumes: `Deps.DB` (add field if the api package reaches the DB through a narrower interface today — mirror how existing handlers get `db` access; they use `d.Store`-style fields on Deps — check `Deps` in `servers.go` and reuse the same field), `Deps.Orch`, `Deps.Audit`, `authz.OrchestratorView/Control`, `uuid`.
- Produces routes (all `RequireAuth`-wrapped; POSTs get CSRF automatically):
  - `GET  /api/v1/orchestrator/projects` → `[{project fields + counts per stage}]` (authz `OrchestratorView`, resource `"orchestrator:*"`)
  - `POST /api/v1/orchestrator/projects` → register `{name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel}`; validates non-empty `name/repo/server_id/workdir`; defaults `base_branch=main, provider=claude, max_parallel=1`; audits `ProjectRegister` (authz `OrchestratorControl`)
  - `GET  /api/v1/orchestrator/projects/{id}/board` → `{project, epics: […], events: {epicID: [last 20]}}`
  - `POST /api/v1/orchestrator/projects/{id}/actions` → `{action: "approve"|"retry"|"cancel"|"pause"|"resume"|"set_max_parallel"|"run_issue", epic_id?, issue?, value?}`; dispatches to `Orch.*` / `DB.SetProjectPaused` / `DB.SetProjectMaxParallel` (+ `Orch.Wake()` after project-level changes); audits `EpicAction`; unknown action ⇒ 400

Response DTO for an epic (json tags): `{id, issue, title, labels, blocked_by, stage, attempt, session, branch, pr, needs, issue_state, queued_at, started_at, stage_updated_at, merged_at}`.

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/api/orchestrator_test.go`. Reuse the package's existing test scaffolding for authenticated requests (look at how `sessions_test.go` builds `Deps` + injects a principal — typically via `authn` test helper putting the principal in context; copy that exact pattern). Test cases:

```go
// Sketch — adapt Deps/principal scaffolding from sessions_test.go:
func TestRegisterAndListProjects(t *testing.T) { /*
   POST /api/v1/orchestrator/projects {"name":"proj","repo":"o/r","server_id":"h1","workdir":"/w"}
   → 201, then GET /api/v1/orchestrator/projects → 1 entry, defaults applied
   (base_branch main, provider claude, max_parallel 1). Missing repo → 400. */ }

func TestBoardEndpoint(t *testing.T) { /*
   Seed project + 2 epics (one escalated with needs) directly via db store.
   GET .../board → 200; epics array has stage + needs; events map present. */ }

func TestActionsDispatch(t *testing.T) { /*
   POST actions {"action":"pause"} → 200 and db paused=true and fakeOrch.woke>0.
   {"action":"approve","epic_id":"e1"} → fakeOrch.Approve called with "user:<principal>".
   {"action":"nope"} → 400. */ }
```

Write these three as full tests against the real handlers (the sketch comments describe assertions, the code must be complete when writing the file — model each request/recorder pair on `TestWebhookAcceptsSignedIssuesEvent` above plus the package's auth helper).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestRegister|TestBoard|TestActions' -v`
Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement**

Create `hubd/internal/api/orchestrator.go` with four handlers following the kill-session handler's shape exactly (`authorizeOr403` → decode with `MaxBytesReader` → validate → act → audit → `writeJSON`):

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

const maxOrchestratorBody = 16 << 10

type epicDTO struct {
	ID         string   `json:"id"`
	Issue      int      `json:"issue"`
	Title      string   `json:"title"`
	Labels     []string `json:"labels"`
	BlockedBy  []int    `json:"blocked_by"`
	Stage      string   `json:"stage"`
	Attempt    int      `json:"attempt"`
	Session    string   `json:"session"`
	Branch     string   `json:"branch"`
	PR         int      `json:"pr"`
	Needs      string   `json:"needs"`
	IssueState string   `json:"issue_state"`
	QueuedAt   string   `json:"queued_at"`
	StartedAt  string   `json:"started_at"`
	StageUpdatedAt string `json:"stage_updated_at"`
	MergedAt   string   `json:"merged_at"`
}

func toEpicDTO(e db.Epic) epicDTO {
	return epicDTO{ID: e.ID, Issue: e.IssueNumber, Title: e.Title, Labels: e.Labels,
		BlockedBy: e.BlockedBy, Stage: e.Stage, Attempt: e.Attempt, Session: e.SessionName,
		Branch: e.Branch, PR: e.PRNumber, Needs: e.Needs, IssueState: e.IssueState,
		QueuedAt: e.QueuedAt, StartedAt: e.StartedAt, StageUpdatedAt: e.StageUpdatedAt,
		MergedAt: e.MergedAt}
}
```

then `OrchestratorProjectsHandler()` (GET list / POST register on method switch or two registrations), `OrchestratorBoardHandler()`, `OrchestratorActionsHandler()`. Use `r.PathValue("id")` for the project id. For approve/retry/cancel pass `"user:" + p.ID` as source. Audit every successful mutation. Route registrations in `router.go`:

```go
	mux.Handle("GET /api/v1/orchestrator/projects", rd.Auth.RequireAuth(rd.API.OrchestratorProjectsHandler()))
	mux.Handle("POST /api/v1/orchestrator/projects", rd.Auth.RequireAuth(rd.API.OrchestratorProjectsHandler()))
	mux.Handle("GET /api/v1/orchestrator/projects/{id}/board", rd.Auth.RequireAuth(rd.API.OrchestratorBoardHandler()))
	mux.Handle("POST /api/v1/orchestrator/projects/{id}/actions", rd.Auth.RequireAuth(rd.API.OrchestratorActionsHandler()))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -v && go build ./...`
Expected: PASS (whole api package).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/api/ && git commit -m "feat(hub): orchestrator projects/board/actions endpoints"
```

---

### Task 19: API — board SSE stream

**Files:**
- Create: `hubd/internal/api/orchestrator_events.go`
- Modify: `hubd/internal/api/router.go` (one route), `Deps` (add `BoardBcast *orchestrator.BoardBroadcaster`)
- Test: `hubd/internal/api/orchestrator_events_test.go`

**Interfaces:**
- Consumes: `orchestrator.BoardBroadcaster` (Task 11), the SSE conventions of `events.go` (**subscribe BEFORE snapshot** — the load-bearing ordering; `writeSSE`; heartbeat via `d.SSEHeartbeat`).
- Produces: `GET /api/v1/orchestrator/events` (RequireAuth-wrapped, authz `OrchestratorView`): on connect emits `event: board-snapshot` with `{projects: [...], epics: [...]}` (all projects + non-terminal & recently-merged epics), then `event: board` per `BoardChange` `{project_id, epic_id, issue, stage, needs, title}`.

- [ ] **Step 1: Write the failing test**

Model on the existing `events_test.go` (same package) — construct Deps with a `BoardBcast`, seeded DB, authenticated request; read the recorder body after publishing one change; assert the body contains `event: board-snapshot` and then `event: board` with `"stage":"escalated"`. Use a cancellable request context to end the handler. Write the complete test following the existing SSE test's cancellation pattern.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestBoardEvents -v`
Expected: FAIL — handler undefined.

- [ ] **Step 3: Implement**

Create `hubd/internal/api/orchestrator_events.go` mirroring `events.go` structure exactly: authz check (`OrchestratorView`, `"orchestrator:*"`), flusher assert, nil-guard `d.BoardBcast`, **Subscribe() first**, SSE headers, snapshot (query DB: `ListProjects` + per-project `ListEpicsByProject`), then select-loop over ctx / channel / heartbeat writing `writeSSE(w, "board", payload)`. Register:

```go
	mux.Handle("GET /api/v1/orchestrator/events", rd.Auth.RequireAuth(rd.API.OrchestratorEventsHandler()))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/internal/api/ && git commit -m "feat(hub): board SSE stream"
```

---

### Task 20: agentws.SendText — guidance into a live session

**Files:**
- Create: `hubd/internal/agentws/sendtext.go`
- Test: `hubd/internal/agentws/sendtext_test.go`
- Modify: `hubd/internal/api/orchestrator.go` (add `guidance` action), `hubd/internal/api/servers.go` (Deps fields it needs)

**Interfaces:**
- Consumes: the inline dial logic of `PaneRelayHandler` (`hubd/internal/api/ws.go:131-207`) — READ IT FIRST and copy its exact header names (`Authorization`, `X-AgentMon-Directive`, `X-AgentMon-Request-Id`) and URL construction (`agentWSURL` at `ws.go:229-243` — replicate, do not import, since it's unexported in another package); `directive.Minter.Mint(srv, principalID, paneID, target)` (`directive/mint.go:60`); `registry.Client.Sessions` for pane resolution; `gorilla/websocket` dialer.
- Produces:
  - `func FirstPaneID(sessions []shared.Session, name string) (string, bool)` — first pane of the first window of the named session
  - `func SendText(ctx context.Context, srv db.Server, minter *directive.Minter, principalID, target, session, text string, sessions []shared.Session) error` — resolves pane, mints rw directive, dials `{agent}/panes/{pane}/io?target=…&mode=rw`, writes ONE `websocket.BinaryMessage` containing `text` bytes, closes cleanly. **Caller appends `"\n"`** if the text should submit.

The `guidance` board action (`{action:"guidance", epic_id, text}`) resolves the epic's project → server → `Client.Sessions` → `SendText` with `text + "\n"`; audits `EpicAction(…, "guidance", …)`. This is the plan-gate approval / escalation-answer path from the spec (§4).

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/agentws/sendtext_test.go`: an `httptest.Server` that upgrades to WS at `/panes/%25/io` (URL-escaped `%25` = pane id `%25`? — use pane id `%1` and note the path escaping the dial code applies; assert on whatever escaped form the dial produces), asserts `mode=rw` query, bearer + directive headers present, reads one binary frame and records it. Test `FirstPaneID` separately with a two-window fixture. Assert `SendText` delivers exactly the bytes `"approved: option A\n"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/agentws/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement**

`FirstPaneID` is pure iteration. `SendText`:

```go
package agentws

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/directive"
	"agentmon/shared"
)

func FirstPaneID(sessions []shared.Session, name string) (string, bool) {
	for _, s := range sessions {
		if s.Name != name {
			continue
		}
		for _, w := range s.Windows {
			if len(w.Panes) > 0 {
				return w.Panes[0].ID, true
			}
		}
	}
	return "", false
}

// SendText injects one line of text into a session's first pane over the
// agent's rw WS — the same channel a browser terminal uses, so no new agent
// surface and no new credentials.
func SendText(ctx context.Context, srv db.Server, minter *directive.Minter, principalID, target, session, text string, sessions []shared.Session) error {
	paneID, ok := FirstPaneID(sessions, session)
	if !ok {
		return fmt.Errorf("agentws: session %q has no pane", session)
	}
	header, reqID, err := minter.Mint(srv, principalID, paneID, target)
	if err != nil {
		return fmt.Errorf("agentws: mint: %w", err)
	}
	u := strings.Replace(srv.URL, "http", "ws", 1) +
		"/panes/" + url.PathEscape(paneID) + "/io?mode=rw"
	if target != "" {
		u += "&target=" + url.QueryEscape(target)
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+srv.Bearer)
	h.Set("X-AgentMon-Directive", header)
	h.Set("X-AgentMon-Request-Id", reqID)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, u, h)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("agentws: dial %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("agentws: dial: %w", err)
	}
	defer conn.Close()
	return conn.WriteMessage(websocket.BinaryMessage, []byte(text))
}
```

**Before finalizing, open `hubd/internal/api/ws.go:131-157` and `directive/mint.go:60-79` and align exactly:** the `Mint` return values/order, the precise header names, and the ws URL scheme replacement (`agentWSURL` handles `https→wss`; replicate that logic — `strings.Replace(srv.URL, "http", "ws", 1)` covers both `http→ws` and `https→wss`). Fix the test to the same names. Then wire the `guidance` action in `api/orchestrator.go` (Deps needs `Minter *directive.Minter` and an agent-client field for `Sessions` — reuse however `Deps` exposes them for the relay/session handlers; check `Deps` in `servers.go`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/hubd && go test ./internal/agentws/ ./internal/api/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/ && git commit -m "feat(hub): send guidance text into live sessions over agent ws"
```

---

### Task 21: main.go wiring + end-to-end integration test

**Files:**
- Modify: `hubd/cmd/agentmon-hubd/main.go`
- Test: `hubd/internal/orchestrator/integration_test.go`

**Interfaces:**
- Consumes: everything. Wiring goes where the poller/push dispatcher are wired (`main.go:83-105` region) and where `api.Deps` is populated (`main.go:129-146`).

- [ ] **Step 1: Write the integration test**

Create `hubd/internal/orchestrator/integration_test.go` — one test walking TWO epics through the full lifecycle against the Task 15 fakes, exercising the dependency chain (this is the acceptance scenario from spec §11 in miniature):

```go
package orchestrator

import (
	"context"
	"testing"

	"agentmon/hubd/internal/github"
	"agentmon/shared"
)

func TestTwoEpicChainEndToEnd(t *testing.T) {
	cleanVerdict := "```yaml\nagentmon-verdict: v1\nepic: 0\nreviews: [codex]\n" +
		"findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{
			1: {Number: 1, Title: "scaffold", State: "open", Labels: []string{"agentmon:epic"}},
			2: {Number: 2, Title: "auth", State: "open", Labels: []string{"agentmon:epic"}, Body: "Blocked by #1"},
		},
		prs:    map[int]github.PullRequest{},
		checks: map[string][]github.CheckRun{},
	}
	ag := &fakeAgents{}
	live := fakeLive{alive: map[string]bool{"epic-1": true, "epic-2": true}}
	o, d := newTestOrch(t, gh, ag, live)
	ctx := context.Background()

	o.Tick(ctx) // mirror both; spawn #1 only (max_parallel=1 AND #2 blocked)
	if len(ag.created) != 1 || ag.created[0].Name != "epic-1" {
		t.Fatalf("created = %+v", ag.created)
	}
	e2, _ := d.GetEpicByIssue(ctx, "p1", 2)
	if e2.Stage != "queued" {
		t.Fatalf("epic 2 = %+v", e2)
	}

	// runner opens PR 10 for epic 1, everything clean
	gh.prs[10] = github.PullRequest{Number: 10, State: "open", Body: cleanVerdict, HeadSHA: "s1", HeadRef: "epic/1-scaffold"}
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 1, Stage: shared.EpicPROpen, PR: 10, Session: "epic-1", Ts: "t"},
	}
	o.Tick(ctx) // pr_open → gate → merged; capacity freed but schedule ran? next tick spawns #2
	e1, _ := d.GetEpicByIssue(ctx, "p1", 1)
	if e1.Stage != "merged" {
		t.Fatalf("epic 1 = %+v", e1)
	}

	o.Tick(ctx) // dep merged → #2 spawns
	if len(ag.created) != 2 || ag.created[1].Name != "epic-2" {
		t.Fatalf("created = %+v", ag.created)
	}
}
```

- [ ] **Step 2: Run it**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestTwoEpicChain -v`
Expected: PASS immediately (it exercises Task 15 code; if it fails, that's a Task 15 bug — fix there).

- [ ] **Step 3: Wire main.go**

In `hubd/cmd/agentmon-hubd/main.go`, after the poller/push wiring (~line 105), gated so an unconfigured hub runs exactly as before:

```go
	// Orchestrator (only when a GitHub token is configured)
	var orch *orchestrator.Orchestrator
	var boardBcast *orchestrator.BoardBroadcaster
	if cfg.GitHub.Token != "" {
		boardBcast = orchestrator.NewBoardBroadcaster()
		orch = orchestrator.New(orchestrator.Deps{
			DB:     database,
			GH:     github.NewClient(cfg.GitHub.Token),
			Agents: agentClient,
			Reg:    reg,
			Live:   proj,
			Bcast:  boardBcast,
			Audit:  auditRec,
			Cfg:    cfg.Orchestrator,
			Now:    func() string { return time.Now().UTC().Format(time.RFC3339) },
		})
		go orch.Run(ctx)
		go orchestrator.RunBoardPushDispatcher(ctx, orchestrator.BoardPushDeps{
			Bcast: boardBcast, Presence: presence, Store: database,
			Send: pushSender, Now: func() string { return time.Now().UTC().Format(time.RFC3339) },
		})
	}
```

(Use the actual local variable names at that point in `main.go` — `database`, `reg`, `agentClient`, `proj`, `presence`, `pushSender`, `auditRec` per the wiring around `main.go:54-105`; read the file and match.) Then add to the `api.Deps` literal: `Orch: orch` (nil-safe interface — assign only when non-nil via a small `if`), `WebhookSecret: cfg.GitHub.WebhookSecret`, `BoardBcast: boardBcast`, plus the Minter/agent-client fields Task 20 added if not already present.

**Nil-interface gotcha:** assigning a nil `*orchestrator.Orchestrator` to the `api.OrchestratorAPI` interface field makes it non-nil-interface-wrapping-nil-pointer. Guard:

```go
	deps := api.Deps{ /* existing fields */ }
	if orch != nil {
		deps.Orch = orch
		deps.WebhookSecret = cfg.GitHub.WebhookSecret
		deps.BoardBcast = boardBcast
	}
```

- [ ] **Step 4: Full verification**

Run: `cd /root/agentmon/hubd && go build ./... && go test ./... && cd /root/agentmon/agent && go build ./... && cd /root/agentmon/shared && go test ./`
Expected: everything green across all three modules.

Then boot smoke-test (no GitHub token → orchestrator disabled, hub behaves exactly as today):

Run: `cd /root/agentmon/hubd && go run ./cmd/agentmon-hubd -config /dev/null 2>&1 | head -5 || true`
Expected: starts (or fails on config parse of /dev/null identically to current main — if `Load` errors on empty file, use a minimal temp yaml instead: `listen: ":0"`). The point: no panic from nil orchestrator wiring.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): wire orchestrator into main — gated on github token"
```

---

## Out of scope for this plan (tracked in the spec §13)

- **Sub-project 2 (runner):** agent loopback `POST /orchestrator/report` intake + buffered `GET /orchestrator/reports` drain endpoint, `agentmon report` CLI, agent-side `CreateSessionRequest.Command` execution (both ends currently reject it — the orchestrator core sets it; the agent honors it once sub-project 2 lands), `epic-pipeline` skill, Codex playbook, import script, doctor run.
- **Sub-project 3 (board UI):** Board/Timeline tabs consuming `GET /api/v1/orchestrator/events` + actions endpoints (mockup: `docs/superpowers/specs/2026-07-10-orchestrator-board-mockup.html`).
- GitHub App auth, provider auto-failover, native issue-relationship API (body convention stands in).

## Deployment note

Hub-only change, fully inert until `github.token` is set in the hub's `config.yaml`. Deploy order unchanged (rebuild hub first). Webhook: point the repo's webhook at `https://agentmon.runald.net/api/v1/github/webhook` with the shared secret, events: issues, pull requests, check suites. Poll reconciliation covers missed deliveries.
