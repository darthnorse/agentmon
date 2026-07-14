# Project-scoped platform requirements — storage & settings UI (epic #1)

> **For agentic workers:** Implement task-by-task in order. Steps use checkbox
> (`- [ ]`) syntax — the ticks are the resume state. Tick each as you complete it.

**Goal:** Add an inert, per-project `requirements` field (a `[]Requirement` slice)
that round-trips DB → API → web contract → settings UI. Nothing reads it yet; the
epic-02 gate and epic-03 runner consume it later.

**Architecture:** Mirror the existing `required_reviews` storage pattern (JSON in a
`TEXT NOT NULL DEFAULT '[]'` column) but for a struct slice. Each `Requirement` is
`{ id, text, check_cmd? }` with a **stable** kebab `id` derived from `text` when
absent. Derivation/validation lives at the API boundary; the DB stays a faithful
store.

**Tech Stack:** Go (hubd: sqlite + net/http), React 18 + TS (web: Vite, Tailwind,
TanStack Query, vitest + Testing Library).

## Global Constraints

- **FULL GATE — must be green before EVERY commit** (both stacks; run bare so a
  failure exits non-zero — do NOT pipe through `tail` on the pass check):

  ```bash
  GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... && ( cd web && npm run typecheck && npm run test:run )
  ```

  (`GOCACHE=/tmp/agentmon-go-cache` is only needed if the default cache is
  read-only; harmless otherwise.)
- **Commits:** conventional prefixes (`feat(db):`, `feat(api):`, `feat(web):`).
  **Never** add a `Co-Authored-By:` / AI-attribution trailer.
- **`web/src/lib/contracts.ts` hand-mirrors the Go `shared`/DTO types** — a new
  field must traverse DB → API → contract → UI, and match the Go json emission
  exactly (a Go field with no `omitempty` is a **required** TS field).
- **Scope:** storage + settings UI only. The field is **inert** — no gate, verdict,
  or runner wiring. A reviewer must be able to confirm nothing reads it.
- **Requirement shape is fixed:** `{ id: string; text: string; check_cmd?: string }`.
  `id` is a lowercase-kebab slug, **stable**: derive from `text` when the author
  supplies none; when supplied it is normalized to kebab but never re-derived from
  edited `text` (it is the join key the epic-02 gate/verdict match on).
- **DTO json tags are snake_case** (`requirements`, and within each record
  `id`/`text`/`check_cmd`) — this is the API DTO, distinct from the Verdict struct's
  CAPITALIZED-JSON caveat (that is epic-02, not here).

## Provenance & acceptance-criteria coverage

Design is taken from the epic #1 issue body (Scope / Acceptance criteria /
Constraints & decisions) and verified against the current code:
- Storage pattern from `hubd/internal/db/projects.go` (`RequiredReviews`,
  `marshalStrings`/`unmarshalStrings`) and migrations `0007_require_ci.sql`,
  `0008_pinned.sql`.
- API DTO/handlers from `hubd/internal/api/orchestrator.go` (`projectDTO`,
  `projectOut`, create + PATCH handlers; existing input validation like
  provider/max_parallel is the model for requirement validation).
- Contract + UI from `web/src/lib/contracts.ts`,
  `web/src/components/board/ProjectForm.tsx`.

| Acceptance criterion | Task |
|---|---|
| Migration `0009` adds `requirements` (`NOT NULL DEFAULT '[]'`), preserves rows on upgrade | Task 1 |
| `Requirements` round-trips CreateProject/GetProject/ListProjects/UpdateProject; `projects_test` | Task 1 |
| `POST /projects` + PATCH accept/return `requirements`; DTO carries it; `orchestrator_test` | Task 2 |
| Stable `id`: derive from `text` when absent, keep stable on text edits | Task 2 |
| `contracts.ts` mirrors the field + `Requirement` shape; typecheck + contract-mirror test green | Task 3 |
| `ProjectForm.tsx` add/edit/remove rows, persists via PATCH, renders on load; component test | Task 4 |
| Full gate green | Every task |

## Key design decisions (for reviewers)

- **`Requirement` lives in `hubd/internal/db` with json tags.** The same struct is
  json-marshaled both into the `requirements` TEXT column and (embedded in the DTO)
  onto the wire, so one json-tagged struct is the single source of truth. `check_cmd`
  uses `omitempty` to match the optional `check_cmd?`.
- **Validation is an API-layer concern.** `slugify` + `normalizeRequirements` derive
  stable ids, trim, drop rows whose resolved id is empty, and **reject duplicate
  resolved ids (→ 400)**. The DB stores whatever it is handed (faithful round-trip);
  only request handlers normalize/validate. Rationale for rejecting duplicates: the
  issue frames requirements as the *fail-closed source of truth* whose `id` is the
  join key the epic-02 gate matches on — two rows resolving to one id would make a
  single verdict entry ambiguous, and that cannot be repaired downstream. Fail closed
  at authoring time, mirroring the handler's existing provider/max_parallel 400s.
- **Supplied ids are normalized to kebab, not preserved verbatim.** The id must be
  both a valid lowercase-kebab slug and stable across text edits; `slugify` is
  idempotent on an already-derived slug, so `slugify(suppliedID)` keeps UI-authored
  ids unchanged while forcing a direct API caller's `"WCAG 2.2"` to `"wcag-2-2"`. It
  is derived from the id (or text when absent), never from *edited* text.
- **`ProjectDTO.requirements` is REQUIRED (`Requirement[]`), not optional.** The Go
  DTO field has no `omitempty`, so the API always emits it (as `[]` when empty), and
  the web bundle is embedded in and served by the same hub binary (no version skew).
  The truthful mirror is required; `counts?` is not a precedent (its Go field *does*
  use `omitempty`). This forces ~10 board-test `ProjectDTO` fixtures to gain
  `requirements: []` (Task 3).

---

## Setup (before Task 1)

- [x] **Step 1: Install web dependencies** (this worktree has no `node_modules`; the
  web gate needs them)

Run: `cd web && npm ci`
Expected: completes with exit 0 (`vitest`/`tsc` now resolve). Audit warnings are fine.

- [x] **Step 2: Confirm the baseline FULL GATE is green** (so later failures are
  attributable to your changes)

Run the FULL GATE (Global Constraints).
Expected: all Go packages `ok`; web typecheck clean; all web tests pass.

---

## Task 1: Storage layer — migration, `Requirement` type, round-trip

**Files:**
- Create: `hubd/internal/db/migrations/0009_requirements.sql`
- Modify: `hubd/internal/db/projects.go`
- Test: `hubd/internal/db/projects_test.go`

**Interfaces:**
- Produces (consumed by Task 2):
  - `db.Requirement struct { ID string \`json:"id"\`; Text string \`json:"text"\`; CheckCmd string \`json:"check_cmd,omitempty"\` }`
  - `db.Project.Requirements []db.Requirement`
  - round-trip through `CreateProject`, `GetProject`, `GetProjectByRepo`,
    `ListProjects`, `UpdateProject`.

- [x] **Step 1: Write the migration**

Create `hubd/internal/db/migrations/0009_requirements.sql` (one line, mirroring
`0007`/`0008`; loaded in lexical order by `migrate()`):

```sql
ALTER TABLE projects ADD COLUMN requirements TEXT NOT NULL DEFAULT '[]';
```

- [x] **Step 2: Write the failing tests** (append to `hubd/internal/db/projects_test.go`)

Also add `"database/sql"` and `"path/filepath"` to the existing import block (needed
by the upgrade test).

```go
func TestProjectRequirementsRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	p := testProject("aigallery")
	p.Requirements = []Requirement{
		{ID: "rls", Text: "Always use RLS", CheckCmd: "scripts/check-rls.sh"},
		{ID: "wcag", Text: "WCAG 2.2 AA"},
	}
	if err := d.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Requirements) != 2 ||
		got.Requirements[0] != (Requirement{ID: "rls", Text: "Always use RLS", CheckCmd: "scripts/check-rls.sh"}) ||
		got.Requirements[1] != (Requirement{ID: "wcag", Text: "WCAG 2.2 AA"}) {
		t.Fatalf("round-trip via GetProject: %+v", got.Requirements)
	}
	list, _ := d.ListProjects(ctx)
	if len(list) != 1 || len(list[0].Requirements) != 2 {
		t.Fatalf("round-trip via ListProjects: %+v", list)
	}
	byRepo, _ := d.GetProjectByRepo(ctx, "darthnorse/school-platform")
	if len(byRepo.Requirements) != 2 || byRepo.Requirements[1].ID != "wcag" {
		t.Fatalf("round-trip via GetProjectByRepo: %+v", byRepo.Requirements)
	}
	// UpdateProject rewrites the set.
	p.Requirements = []Requirement{{ID: "pii", Text: "No PII in logs"}}
	if ok, err := d.UpdateProject(ctx, p); err != nil || !ok {
		t.Fatalf("update: ok=%v err=%v", ok, err)
	}
	got, _ = d.GetProject(ctx, "p1")
	if len(got.Requirements) != 1 || got.Requirements[0].ID != "pii" {
		t.Fatalf("requirements after update: %+v", got.Requirements)
	}
}

func TestProjectRequirementsDefaultEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")
	// A project created without requirements stores + reads back as empty, never NULL.
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetProject(ctx, "p1"); len(got.Requirements) != 0 {
		t.Fatalf("absent requirements must be empty: %+v", got.Requirements)
	}
}

// The AC requires that applying 0009 to a DB populated at the PRIOR schema
// preserves existing rows. openTestDB/Open apply every migration up front, so we
// build the projects table at the pre-0009 (0008) shape, populate it, then apply
// the REAL 0009 SQL and confirm the pre-existing row survives, backfilled with '[]'.
func TestRequirementsMigrationPreservesExistingRows(t *testing.T) {
	sqldb, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "u.sqlite")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	ctx := context.Background()
	// projects at the pre-0009 shape (0005 + 0007 require_ci + 0008 pinned).
	if _, err := sqldb.ExecContext(ctx, `CREATE TABLE projects (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, repo TEXT NOT NULL, server_id TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '', workdir TEXT NOT NULL, base_branch TEXT NOT NULL DEFAULT 'main',
		provider TEXT NOT NULL DEFAULT 'claude', required_reviews TEXT NOT NULL DEFAULT '[]',
		max_parallel INTEGER NOT NULL DEFAULT 1, paused INTEGER NOT NULL DEFAULT 0,
		require_ci INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, `INSERT INTO projects(id,name,repo,server_id,workdir,created_at,updated_at)
		VALUES('old','old','o/old','h1','/w', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	body, err := migrationFS.ReadFile("migrations/0009_requirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("0009 must apply to a populated table: %v", err)
	}
	var reqs string
	if err := sqldb.QueryRowContext(ctx, `SELECT requirements FROM projects WHERE id='old'`).Scan(&reqs); err != nil {
		t.Fatalf("pre-existing row must survive 0009: %v", err)
	}
	if reqs != "[]" {
		t.Fatalf("backfilled requirements = %q, want []", reqs)
	}
}
```

- [x] **Step 3: Run the db tests to verify they FAIL (compile error)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/db/`
Expected: build FAILS — `p.Requirements undefined` / `undefined: Requirement`.

- [x] **Step 4: Add the `Requirement` type + `Project.Requirements` field**

In `hubd/internal/db/projects.go`, add the type just above `type Project struct`:

```go
// Requirement is one platform-invariant standard a project asserts over every
// epic. It is a value object json-marshaled both into the projects.requirements
// TEXT column and (embedded in the API DTO) onto the wire, so these json tags are
// the single source of truth for both shapes. ID is the stable slug the epic-02
// gate/verdict join on; Text doubles as the review lens; CheckCmd is an optional
// shell command whose exit code can certify the requirement where an LLM cannot.
// Stored inert this epic — nothing reads it yet.
type Requirement struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	CheckCmd string `json:"check_cmd,omitempty"`
}
```

Add the field to `Project` immediately after `Pinned bool`:

```go
	Pinned          bool
	Requirements    []Requirement
```

- [x] **Step 5: Wire `requirements` into the column list, scan, insert, update**

In `hubd/internal/db/projects.go`:

Append `requirements` to `projectCols`:

```go
const projectCols = "id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci, pinned, requirements"
```

Scan it last in `scanProject` (add `reqs` alongside `reviews`):

```go
func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var reviews, reqs string
	if err := row.Scan(&p.ID, &p.Name, &p.Repo, &p.ServerID, &p.Target, &p.Workdir,
		&p.BaseBranch, &p.Provider, &reviews, &p.MaxParallel, &p.Paused, &p.RequireCI, &p.Pinned, &reqs); err != nil {
		return Project{}, err
	}
	p.RequiredReviews = unmarshalStrings(reviews)
	p.Requirements = unmarshalRequirements(reqs)
	return p, nil
}
```

Add `requirements` to the `CreateProject` INSERT (explicit, so it round-trips —
note the existing INSERT omits `pinned`, relying on its DEFAULT):

```go
func (d *DB) CreateProject(ctx context.Context, p Project) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO projects(id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci, requirements, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider,
		marshalStrings(p.RequiredReviews), p.MaxParallel, p.Paused, p.RequireCI, marshalRequirements(p.Requirements))
	return err
}
```

Add `requirements = ?` to `UpdateProject` (joining the editable set beside
`required_reviews`):

```go
	found, err := d.execFound(ctx,
		`UPDATE projects SET name = ?, workdir = ?, target = ?, base_branch = ?, provider = ?, required_reviews = ?, requirements = ?, updated_at = datetime('now') WHERE id = ?`,
		p.Name, p.Workdir, p.Target, p.BaseBranch, p.Provider, marshalStrings(p.RequiredReviews), marshalRequirements(p.Requirements), p.ID)
```

Add the marshal helpers just below `unmarshalStrings`:

```go
// marshalRequirements / unmarshalRequirements mirror marshalStrings for the
// []Requirement TEXT column: a JSON array, "[]" for empty, never NULL.
func marshalRequirements(rs []Requirement) string {
	if len(rs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(rs)
	return string(b)
}

func unmarshalRequirements(s string) []Requirement {
	if s == "" {
		return nil
	}
	var out []Requirement
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
```

- [x] **Step 6: Run the db tests to verify they PASS (bare)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/db/`
Expected: `ok  	agentmon/hubd/internal/db`.

- [x] **Step 7: Run the FULL GATE**

Run the FULL GATE (Global Constraints). Expected: all green.

- [x] **Step 8: Commit**

```bash
git add hubd/internal/db/migrations/0009_requirements.sql hubd/internal/db/projects.go hubd/internal/db/projects_test.go
git commit -m "feat(db): project requirements column + round-trip (epic #1)"
```

---

## Task 2: API layer — DTO, create/PATCH wiring, stable-id + duplicate validation

**Files:**
- Create: `hubd/internal/api/requirements.go`
- Create: `hubd/internal/api/requirements_test.go`
- Modify: `hubd/internal/api/orchestrator.go`
- Test: `hubd/internal/api/orchestrator_test.go`

**Interfaces:**
- Consumes (from Task 1): `db.Requirement`, `db.Project.Requirements`.
- Produces: `slugify(string) string`,
  `normalizeRequirements([]db.Requirement) ([]db.Requirement, error)` (error on
  duplicate resolved id); `projectDTO.Requirements []db.Requirement \`json:"requirements"\``;
  create + PATCH accept/return `requirements`.

- [x] **Step 1: Write the failing unit tests** (`hubd/internal/api/requirements_test.go`)

```go
package api

import (
	"testing"

	"agentmon/hubd/internal/db"
)

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Always use RLS":   "always-use-rls",
		"WCAG 2.2 AA":      "wcag-2-2-aa",
		"  Trim  Me  ":     "trim-me",
		"No PII in logs!":  "no-pii-in-logs",
		"tenant_isolation": "tenant-isolation",
		"---edge---":       "edge",
		"!!!":              "",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRequirements(t *testing.T) {
	got, err := normalizeRequirements([]db.Requirement{
		{Text: "Always use RLS"},                          // id derived from text
		{ID: "WCAG 2.2", Text: "WCAG 2.2 renamed to 2.3"}, // supplied id normalized to kebab, stable vs text
		{Text: "   "},                                     // blank text dropped
		{Text: "!!!"},                                     // unsluggable text dropped
		{Text: "  No PII in logs  ", CheckCmd: "  s.sh "}, // trimmed
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []db.Requirement{
		{ID: "always-use-rls", Text: "Always use RLS"},
		{ID: "wcag-2-2", Text: "WCAG 2.2 renamed to 2.3"},
		{ID: "no-pii-in-logs", Text: "No PII in logs", CheckCmd: "s.sh"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("requirement %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNormalizeRequirementsRejectsDuplicateIDs(t *testing.T) {
	// Two rows resolving to the same id would make the epic-02 join ambiguous.
	if _, err := normalizeRequirements([]db.Requirement{
		{Text: "Always use RLS"},
		{ID: "always-use-rls", Text: "A different standard"},
	}); err == nil {
		t.Fatal("duplicate resolved id must error")
	}
}
```

- [x] **Step 2: Run to verify FAIL (compile error)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/`
Expected: build FAILS — `undefined: slugify` / `undefined: normalizeRequirements`.

- [x] **Step 3: Implement `slugify` + `normalizeRequirements`** (`hubd/internal/api/requirements.go`)

```go
package api

import (
	"fmt"
	"regexp"
	"strings"

	"agentmon/hubd/internal/db"
)

// nonSlugRe collapses every run of non-[a-z0-9] characters to a single dash.
var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a stable lowercase-kebab id: lowercase, non-alphanumeric runs →
// single dash, trimmed of leading/trailing dashes. "Always use RLS" →
// "always-use-rls". It is idempotent on an already-derived slug.
func slugify(s string) string {
	s = nonSlugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// normalizeRequirements shapes + validates author input into the stored form:
//   - trims id/text/check_cmd,
//   - drops any row with blank text (text is the review lens — meaningless without it),
//   - resolves the id by slugifying the supplied id, or the text when none was
//     supplied. Slugifying the supplied id enforces the lowercase-kebab invariant
//     while keeping it STABLE across later text edits (it is derived from the id,
//     never from edited text; slugify is idempotent on an existing slug),
//   - drops any row whose resolved id is empty (text with no slug-able characters),
//   - rejects duplicate resolved ids: the id is the join key the epic-02 gate
//     matches on, so two rows sharing one id would make a single verdict entry
//     ambiguous. Fail closed here — mirroring the handler's provider/max_parallel
//     400s — rather than store an unenforceable set.
func normalizeRequirements(in []db.Requirement) ([]db.Requirement, error) {
	out := make([]db.Requirement, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, r := range in {
		r.ID = strings.TrimSpace(r.ID)
		r.Text = strings.TrimSpace(r.Text)
		r.CheckCmd = strings.TrimSpace(r.CheckCmd)
		if r.Text == "" {
			continue
		}
		base := r.ID
		if base == "" {
			base = r.Text
		}
		r.ID = slugify(base)
		if r.ID == "" {
			continue
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("duplicate requirement id %q", r.ID)
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out, nil
}
```

- [x] **Step 4: Run the unit tests to verify PASS (bare)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run 'TestSlugify|TestNormalizeRequirements'`
Expected: `ok` (all three pass).

- [x] **Step 5: Write the failing API round-trip test** (append to `hubd/internal/api/orchestrator_test.go`)

This references `projectDTO.Requirements`, which does not exist yet → it will not
compile (red) until Step 7.

```go
func TestProjectRequirementsAPI(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	d := Deps{DB: database, Orch: &fakeOrch{}, Reg: registry.New(database), Audit: audit.NewRecorder(&captureSink{})}

	// CREATE: supplied id preserved; missing id derived from text; blank row dropped.
	body := `{"name":"proj","repo":"o/r","server_id":"h1","workdir":"/w",` +
		`"requirements":[{"text":"Always use RLS"},{"id":"wcag","text":"WCAG 2.2 AA"},{"text":"  "}]}`
	r, w := orchReq("POST", "/api/v1/orchestrator/projects", body)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created projectDTO
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Requirements) != 2 ||
		created.Requirements[0] != (db.Requirement{ID: "always-use-rls", Text: "Always use RLS"}) ||
		created.Requirements[1] != (db.Requirement{ID: "wcag", Text: "WCAG 2.2 AA"}) {
		t.Fatalf("create response requirements = %+v", created.Requirements)
	}
	got, _ := database.GetProject(ctx, created.ID)
	if len(got.Requirements) != 2 || got.Requirements[0].ID != "always-use-rls" {
		t.Fatalf("persisted requirements = %+v", got.Requirements)
	}

	// PATCH must accept AND RETURN requirements, keeping the id stable across a
	// text edit.
	patch := `{"requirements":[{"id":"always-use-rls","text":"Always use row-level security"}]}`
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/"+created.ID, patch)
	r.SetPathValue("id", created.ID)
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	var patched projectDTO
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if len(patched.Requirements) != 1 ||
		patched.Requirements[0] != (db.Requirement{ID: "always-use-rls", Text: "Always use row-level security"}) {
		t.Fatalf("patch response requirements = %+v", patched.Requirements)
	}
	if got, _ = database.GetProject(ctx, created.ID); got.Requirements[0].Text != "Always use row-level security" {
		t.Fatalf("patch must persist the edited text: %+v", got.Requirements)
	}

	// Duplicate resolved ids are rejected at create time (fail closed).
	dup := `{"name":"dup","repo":"o/dup","server_id":"h1","workdir":"/w",` +
		`"requirements":[{"text":"Always use RLS"},{"id":"always-use-rls","text":"Other"}]}`
	r, w = orchReq("POST", "/api/v1/orchestrator/projects", dup)
	d.OrchestratorProjectsHandler()(w, r)
	if w.Code != 400 {
		t.Fatalf("duplicate requirement ids must 400, got %d %s", w.Code, w.Body.String())
	}
}
```

- [x] **Step 6: Run to verify FAIL (compile error)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/`
Expected: build FAILS — `created.Requirements undefined (type projectDTO ...)`.

- [x] **Step 7: Wire `requirements` into the DTO + handlers** (`hubd/internal/api/orchestrator.go`)

Add the DTO field to `projectDTO`, immediately after `Pinned`:

```go
	Pinned          bool             `json:"pinned"`
	Requirements    []db.Requirement `json:"requirements"`
	Counts          map[string]int   `json:"counts,omitempty"`
```

Update the **positional** `projectOut` literal to include `p.Requirements` in the
same position (between `p.Pinned` and `counts`):

```go
func projectOut(p db.Project, counts map[string]int) projectDTO {
	return projectDTO{p.ID, p.Name, p.Repo, p.ServerID, p.Target, p.Workdir, p.BaseBranch, p.Provider, p.RequiredReviews, p.MaxParallel, p.Paused, p.RequireCI, p.Pinned, p.Requirements, counts}
}
```

In the **create** handler's `in` struct, add `Requirements` after `RequiredReviews`:

```go
			RequiredReviews []string         `json:"required_reviews"`
			Requirements    []db.Requirement `json:"requirements"`
			MaxParallel     int              `json:"max_parallel"`
			RequireCI       bool             `json:"require_ci"`
```

In the same handler, normalize + validate, then set `Requirements` when building
`pr`. Insert this block immediately BEFORE the `pr := db.Project{...}` line:

```go
		reqs, err := normalizeRequirements(in.Requirements)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
```

Then reference `reqs` in the struct literal (add `Requirements: reqs,`):

```go
		pr := db.Project{ID: uuid.NewString(), Name: in.Name, Repo: in.Repo, ServerID: in.ServerID, Target: in.Target, Workdir: in.Workdir, BaseBranch: in.BaseBranch, Provider: in.Provider, RequiredReviews: in.RequiredReviews, Requirements: reqs, MaxParallel: in.MaxParallel, RequireCI: in.RequireCI}
```

Note: the create handler already declares `err` earlier via `if err := ...Decode(...)`
inside an `if` scope; the `reqs, err :=` above is the first `err` in the function
body scope, so `:=` is correct. If the compiler reports `err` redeclared, change it
to `var err error` + `reqs, err = ...` — but as written the surrounding `err`s are
all block-scoped inside `if` statements, so `:=` compiles.

In the **PATCH** handler's `in` struct, add the pointer field after
`RequiredReviews`:

```go
			RequiredReviews *[]string         `json:"required_reviews"`
			Requirements    *[]db.Requirement `json:"requirements"`
```

And apply it beside the `RequiredReviews` block (absent = unchanged). Place after
the `if in.RequiredReviews != nil { ... }` block:

```go
		if in.Requirements != nil {
			reqs, err := normalizeRequirements(*in.Requirements)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			pr.Requirements = reqs
		}
```

- [x] **Step 8: Run the API tests to verify PASS (bare)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/`
Expected: `ok  	agentmon/hubd/internal/api`.

- [x] **Step 9: Run the FULL GATE**

Run the FULL GATE (Global Constraints). Expected: all green.

- [x] **Step 10: Commit**

```bash
git add hubd/internal/api/requirements.go hubd/internal/api/requirements_test.go hubd/internal/api/orchestrator.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(api): accept + return project requirements, derive stable ids (epic #1)"
```

---

## CHECKPOINT 1 — backend seam (data/schema + API + id validation)

- [x] **CHECKPOINT 1 — reviewed to 0bb3e5e**

The highest-judgment code (stable-id derivation, duplicate rejection) and the whole
DB → API data flow land here — reviewed before any frontend is built.

- [x] **Step 1:** `agentmon report --epic 1 --stage reviewing`
- [x] **Step 2:** segment base = `git merge-base HEAD origin/main`.
- [x] **Step 3:** run `/multi-review <segment-base>..HEAD --codex` in this session.
- [x] **Step 4:** route outcomes — FIX already applied+committed by the review
  (verify the FULL GATE is still green); DISCUSS → escalate with the item as the
  note; NITPICKs → record in the report file only.
- [x] **Step 5:** write the consolidated report to `docs/reviews/epic-1-cp1.md`, tick
  the `CHECKPOINT 1 — reviewed to <sha>` line above with the reviewed SHA, commit
  both: `docs: epic #1 checkpoint 1 review`.
- [x] **Step 6:** `agentmon report --epic 1 --stage implementing` and continue.

---

## Task 3: Web contract mirror + fixture updates

**Files:**
- Modify: `web/src/lib/contracts.ts`
- Create: `web/src/lib/contracts.test.ts`
- Modify (add `requirements: []` to the typed `ProjectDTO` fixtures):
  `web/src/components/board/{ProjectSwitcher,EpicDrawer,PlanPanel,BoardView,EpicCard,ProjectHeader,PinnedProjects,DeleteProject,TimelineView,TerminalPreview}.test.tsx`

**Interfaces:**
- Produces (consumed by Task 4): `Requirement` interface;
  `ProjectDTO.requirements: Requirement[]`; `ProjectCreateRequest.requirements?`;
  `ProjectPatchRequest.requirements?`.

- [x] **Step 1: Add the `Requirement` interface + wire it into the project types**

In `web/src/lib/contracts.ts`, add the interface just above `ProjectDTO`:

```ts
// One platform-invariant requirement (mirrors Go db.Requirement). `id` is a
// stable kebab slug the epic-02 gate matches on; `text` doubles as the review
// lens; `check_cmd` optionally certifies it via a shell exit code.
export interface Requirement { id: string; text: string; check_cmd?: string; }
```

Add `requirements` to `ProjectDTO` — REQUIRED, because the Go DTO field has no
`omitempty` and always emits `[]`:

```ts
export interface ProjectDTO {
  id: string; name: string; repo: string; server_id: string; target: string;
  workdir: string; base_branch: string; provider: string;
  required_reviews: string[] | null; max_parallel: number; paused: boolean;
  require_ci: boolean; pinned: boolean; requirements: Requirement[];
  counts?: Record<string, number>;
}
```

Add `requirements?` to both request types (optional — the client need not send it):

```ts
export interface ProjectCreateRequest {
  name: string; repo: string; server_id: string; target?: string; workdir: string;
  base_branch?: string; provider?: string; required_reviews?: string[];
  requirements?: Requirement[]; max_parallel?: number; require_ci?: boolean;
}
export interface ProjectPatchRequest {
  name?: string; workdir?: string; target?: string; base_branch?: string;
  provider?: string; required_reviews?: string[]; requirements?: Requirement[];
}
```

- [x] **Step 2: Write the contract-mirror test** (`web/src/lib/contracts.test.ts`)

```ts
import { describe, expect, it } from "vitest";
import type { ProjectCreateRequest, ProjectDTO, ProjectPatchRequest, Requirement } from "@/lib/contracts";

// Contract mirror: the web Requirement / ProjectDTO shapes must track Go's
// db.Requirement / project DTO (CLAUDE.md hand-mirror rule). These are
// compile-time-checked type assignments plus a runtime shape check; a drift in
// field names or optionality breaks `npm run typecheck` (and this test).
describe("Requirement contract mirror", () => {
  it("matches the Go db.Requirement json shape { id, text, check_cmd? }", () => {
    const full: Requirement = { id: "rls", text: "Always use RLS", check_cmd: "s.sh" };
    const minimal: Requirement = { id: "wcag", text: "WCAG 2.2 AA" }; // check_cmd optional
    expect(Object.keys(full).sort()).toEqual(["check_cmd", "id", "text"]);
    expect(minimal.check_cmd).toBeUndefined();
  });

  it("is carried (required) by the project DTO and (optional) by both request bodies", () => {
    const reqs: Requirement[] = [{ id: "pii", text: "No PII in logs" }];
    const dto: Pick<ProjectDTO, "requirements"> = { requirements: reqs };
    const create: ProjectCreateRequest = { name: "n", repo: "o/r", server_id: "h", workdir: "/w", requirements: reqs };
    const patch: ProjectPatchRequest = { requirements: reqs };
    expect(dto.requirements).toBe(reqs);
    expect(create.requirements).toBe(reqs);
    expect(patch.requirements).toBe(reqs);
  });
});
```

- [x] **Step 3: Run typecheck to reveal the fixtures that now need `requirements`**

Run: `cd web && npm run typecheck`
Expected: FAILS — each typed `const project: ProjectDTO = { ... }` fixture errors
with "Property 'requirements' is missing". (This is the compile-time proof that the
field is required.)

- [x] **Step 4: Add `requirements: []` to each typed `ProjectDTO` fixture**

For every file listed under **Files** above, add `requirements: [],` inside the
`ProjectDTO` object literal / factory (alongside the other fields such as
`pinned`). Example for `PinnedProjects.test.tsx` (a factory):

```ts
const p = (id: string, name: string, pinned: boolean): ProjectDTO => ({
  id, name, repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: false, pinned, requirements: [],
});
```

Re-run `cd web && npm run typecheck` and repeat until it is clean — the errors are a
complete checklist of the fixtures to touch (do not guess; let typecheck drive it).

- [x] **Step 5: Run the FULL GATE**

Run the FULL GATE (Global Constraints). Expected: all green.

- [x] **Step 6: Commit**

```bash
git add web/src/lib/contracts.ts web/src/lib/contracts.test.ts web/src/components/board/*.test.tsx
git commit -m "feat(web): mirror Requirement contract + fixtures (epic #1)"
```

---

## Task 4: Web UI — ProjectForm requirements editor + tests

**Files:**
- Modify: `web/src/components/board/ProjectForm.tsx`
- Test: `web/src/components/board/ProjectForm.test.tsx`

**Interfaces:**
- Consumes (from Task 3): `Requirement`, `ProjectDTO.requirements`,
  `ProjectCreateRequest.requirements`, `ProjectPatchRequest.requirements`.

- [x] **Step 1: Extend the mocks + add the failing component tests** (`ProjectForm.test.tsx`)

Replace the hoisted-mocks + api-client mock lines at the top:

Replace:
```ts
const h = vi.hoisted(() => ({ createProject: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), createProject: h.createProject }));
```
with:
```ts
const h = vi.hoisted(() => ({ createProject: vi.fn(), patchProject: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), createProject: h.createProject, patchProject: h.patchProject }));
```

Add `ProjectDTO` to the type import:
```ts
import type { ProjectDTO, ServerSummary } from "@/lib/contracts";
```

Append this describe block (note the edit fixture includes `pinned: false` — it is a
typed `ProjectDTO`):
```tsx
describe("ProjectForm requirements", () => {
  beforeEach(() => { h.createProject.mockReset(); h.patchProject.mockReset(); });

  it("sends added requirement rows on create (id blank — server derives it)", async () => {
    h.createProject.mockResolvedValue({ id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, pinned: false, requirements: [] });
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    fireEvent.click(screen.getByRole("button", { name: "Add requirement" }));
    fireEvent.change(screen.getByLabelText("Requirement 1 text"), { target: { value: "Always use RLS" } });
    fireEvent.change(screen.getByLabelText("Requirement 1 check command"), { target: { value: "scripts/rls.sh" } });
    fireEvent.click(screen.getByRole("button", { name: "Register project" }));
    await waitFor(() => expect(h.createProject).toHaveBeenCalled());
    expect(h.createProject).toHaveBeenCalledWith(expect.objectContaining({
      requirements: [{ id: "", text: "Always use RLS", check_cmd: "scripts/rls.sh" }],
    }));
  });

  it("renders existing requirements in edit mode and removes a row, keeping ids", async () => {
    const project: ProjectDTO = { id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, pinned: false, requirements: [{ id: "rls", text: "Always use RLS" }, { id: "wcag", text: "WCAG 2.2 AA" }] };
    h.patchProject.mockResolvedValue(project);
    render(<ProjectForm mode="edit" project={project} onDone={() => {}} />);
    expect((screen.getByLabelText("Requirement 1 text") as HTMLInputElement).value).toBe("Always use RLS");
    expect((screen.getByLabelText("Requirement 2 text") as HTMLInputElement).value).toBe("WCAG 2.2 AA");
    fireEvent.click(screen.getByRole("button", { name: "Remove requirement 1" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(h.patchProject).toHaveBeenCalled());
    expect(h.patchProject).toHaveBeenCalledWith("p1", expect.objectContaining({
      requirements: [{ id: "wcag", text: "WCAG 2.2 AA" }],
    }));
  });
});
```

- [x] **Step 2: Run to verify FAIL**

Run: `cd web && npm run test:run -- ProjectForm`
Expected: the two new tests FAIL (no "Add requirement" button / no requirement inputs).

- [x] **Step 3: Implement the requirements editor** (`web/src/components/board/ProjectForm.tsx`)

Add `Requirement` to the type import:
```tsx
import type { ProjectCreateRequest, ProjectDTO, ProjectPatchRequest, Requirement, ServerSummary } from "@/lib/contracts";
```

Add state + handlers (place after the `maxParallel` state line ~31):
```tsx
  const [requirements, setRequirements] = React.useState<Requirement[]>(init?.requirements ?? []);
  const addRequirement = () => setRequirements((rs) => [...rs, { id: "", text: "" }]);
  const updateRequirement = (i: number, patch: Partial<Requirement>) =>
    setRequirements((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const removeRequirement = (i: number) => setRequirements((rs) => rs.filter((_, j) => j !== i));
```

Include `requirements` in the edit body:
```tsx
        const body: ProjectPatchRequest = {
          name: name.trim(), workdir: workdir.trim(), target: target.trim(),
          base_branch: baseBranch.trim(), provider, required_reviews: reviewList(),
          requirements,
        };
```
…and in the create body:
```tsx
        const body: ProjectCreateRequest = {
          name: name.trim(), repo: repo.trim(), server_id: serverId, target: target.trim() || undefined,
          workdir: workdir.trim(), base_branch: baseBranch.trim(), provider,
          required_reviews: reviewList(), requirements, max_parallel: maxParallel, require_ci: requireCI,
        };
```

Render the editor right after the `{field("pf-reviews", ...)}` line (so it shows in
BOTH modes, like Required reviews):
```tsx
        <div className="space-y-1.5">
          <Label>Platform requirements</Label>
          <div className="space-y-2">
            {requirements.length === 0 && (
              <p className="text-xs text-muted-foreground">Standards enforced on every epic (e.g. “Always use RLS”). Optional — inert until later epics wire the gate.</p>
            )}
            {requirements.map((r, i) => (
              <div key={i} className="flex gap-1.5">
                <Input aria-label={`Requirement ${i + 1} text`} value={r.text}
                  onChange={(e) => updateRequirement(i, { text: e.target.value })}
                  placeholder="Standard, e.g. WCAG 2.2 AA" />
                <Input aria-label={`Requirement ${i + 1} check command`} value={r.check_cmd ?? ""}
                  onChange={(e) => updateRequirement(i, { check_cmd: e.target.value })}
                  placeholder="check cmd (optional)" spellCheck={false} className="font-mono text-xs" />
                <Button type="button" size="sm" variant="ghost" aria-label={`Remove requirement ${i + 1}`}
                  onClick={() => removeRequirement(i)}>✕</Button>
              </div>
            ))}
            <Button type="button" size="sm" variant="outline" onClick={addRequirement}>Add requirement</Button>
          </div>
        </div>
```

- [x] **Step 4: Run the component tests to verify PASS**

Run: `cd web && npm run test:run -- ProjectForm`
Expected: all ProjectForm tests pass.

- [x] **Step 5: Run the FULL GATE**

Run the FULL GATE (Global Constraints). Expected: all green.

- [x] **Step 6: Commit**

```bash
git add web/src/components/board/ProjectForm.tsx web/src/components/board/ProjectForm.test.tsx
git commit -m "feat(web): edit project requirements in settings form (epic #1)"
```

---

## Finish (runner Step 7 — not a task)

After Task 4: rebase onto `origin/main`, run the final whole-branch
`/multi-review <merge-base>..HEAD --codex` (covers the frontend seam + everything),
write learnings back into `CLAUDE.md` if any, run the FULL GATE one last time,
push, open the PR with the verdict block, report `pr_open`.

## Self-review notes

- **Spec coverage:** every acceptance criterion maps to a task (table above),
  including the true-upgrade migration test (Task 1) and the named contract-mirror
  test (Task 3).
- **Placeholder scan:** no TBD/TODO; all code shown in full; the one non-exact step
  (Task 3 Step 4 fixture sweep) is explicitly driven by typecheck errors, not guessed.
- **Type consistency:** `Requirement{ID,Text,CheckCmd}` (Go) ↔
  `Requirement{id,text,check_cmd?}` (TS); `normalizeRequirements` returns
  `([]db.Requirement, error)` consistently across Task 2 impl, unit tests, and both
  handlers; `projectOut` positional literal updated in lockstep with the `projectDTO`
  field (compile-checked).
