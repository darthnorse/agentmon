# Project-scoped platform requirements — storage & settings UI (epic #1)

> **For agentic workers:** Implement task-by-task in order. Steps use checkbox
> (`- [ ]`) syntax — the ticks are the resume state. Tick each as you complete it.

**Goal:** Add an inert, per-project `requirements` field (a `[]Requirement` slice)
that round-trips DB → API → web contract → settings UI. Nothing reads it yet; the
epic-02 gate and epic-03 runner consume it later.

**Architecture:** Mirror the existing `required_reviews` storage pattern (JSON in a
`TEXT NOT NULL DEFAULT '[]'` column) but for a struct slice. Each `Requirement` is
`{ id, text, check_cmd? }` with a **stable** kebab `id` derived from `text` only
when absent. Derivation/normalization lives at the API boundary; the DB stays a
faithful store.

**Tech Stack:** Go (hubd: sqlite + net/http), React 18 + TS (web: Vite, Tailwind,
TanStack Query, vitest + Testing Library).

## Global Constraints

- **Gate command (must be green before EVERY commit):**
  - Go: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...`
  - Web: `cd web && npm run typecheck && npm run test:run`
- **Commits:** conventional prefixes (`feat(db):`, `feat(api):`, `feat(web):`).
  **Never** add a `Co-Authored-By:` / AI-attribution trailer.
- **`web/src/lib/contracts.ts` hand-mirrors the Go `shared`/DTO types** — a new
  field must traverse DB → API → contract → UI.
- **Scope:** storage + settings UI only. The field is **inert** — no gate, verdict,
  or runner wiring. A reviewer must be able to confirm nothing reads it.
- **Requirement shape is fixed:** `{ id: string; text: string; check_cmd?: string }`.
  `id` is a lowercase-kebab slug, **stable**: derive from `text` when the author
  supplies none, but never re-derive when `text` is later edited (it is the join
  key the epic-02 gate/verdict match on).
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
  `projectOut`, create + PATCH handlers).
- Contract + UI from `web/src/lib/contracts.ts`,
  `web/src/components/board/ProjectForm.tsx`.

| Acceptance criterion | Task |
|---|---|
| Migration `0009` adds `requirements` (`NOT NULL DEFAULT '[]'`), preserves rows | Task 1 |
| `Requirements` round-trips CreateProject/GetProject/ListProjects/UpdateProject; `projects_test` | Task 1 |
| `POST /projects` + PATCH accept/return `requirements`; DTO carries it; `orchestrator_test` | Task 2 |
| Stable `id`: derive from `text` when absent, preserve on edit | Task 2 |
| `contracts.ts` mirrors the field + `Requirement` shape; typecheck green | Task 3 |
| `ProjectForm.tsx` add/edit/remove rows, persists via PATCH, renders on load; component test | Task 4 |
| Full gate green | Every task |

## Key design decisions (for reviewers)

- **`Requirement` lives in `hubd/internal/db` with json tags.** The same struct is
  json-marshaled both into the `requirements` TEXT column and (embedded in the DTO)
  onto the wire, so one json-tagged struct is the single source of truth for both
  shapes. `check_cmd` uses `omitempty` to match the optional `check_cmd?`.
- **Normalization is an API-layer concern.** `slugify` + `normalizeRequirements`
  derive stable ids, trim, and drop rows whose resolved id is empty. The DB stores
  whatever it is handed (faithful round-trip); only request handlers normalize.
- **`ProjectDTO.requirements` is OPTIONAL (`requirements?`) in the TS contract**, not
  `Requirement[] | null` like `required_reviews`. Rationale: (1) during the hub
  rollout window an older API response legitimately lacks the field, so
  possibly-absent is the *more* truthful client type; (2) it avoids churning ~11
  unrelated board-test `ProjectDTO` fixtures; (3) `counts?` already sets the
  optional-field precedent. The form reads `init?.requirements ?? []`, handling both.
- **Duplicate ids are NOT de-duplicated here.** The field is inert this epic;
  de-duplication belongs with the gate that consumes ids (epic-02). Documented in
  `normalizeRequirements`.

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

- [ ] **Step 1: Write the migration**

Create `hubd/internal/db/migrations/0009_requirements.sql` (one line, mirroring
`0007`/`0008`; loaded in lexical order by `migrate()`):

```sql
ALTER TABLE projects ADD COLUMN requirements TEXT NOT NULL DEFAULT '[]';
```

- [ ] **Step 2: Write the failing tests** (append to `hubd/internal/db/projects_test.go`)

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

func TestProjectRequirementsColumnDefault(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	enrollTestServer(t, d, "aigallery")

	// (1) CreateProject with no requirements → stored + read back empty, never NULL.
	if err := d.CreateProject(ctx, testProject("aigallery")); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetProject(ctx, "p1"); len(got.Requirements) != 0 {
		t.Fatalf("absent requirements must be empty via CreateProject: %+v", got.Requirements)
	}

	// (2) A row written before 0009 (raw insert omitting the column) must be
	// backfilled by the NOT NULL DEFAULT '[]' — GetProject succeeds, empty.
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO projects(id, name, repo, server_id, target, workdir, base_branch, provider, required_reviews, max_parallel, paused, require_ci, created_at, updated_at)
		 VALUES('legacy','legacy','o/legacy','aigallery','','/w','main','claude','[]',1,0,0, datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetProject(ctx, "legacy")
	if err != nil {
		t.Fatalf("legacy row must survive 0009: %v", err)
	}
	if len(got.Requirements) != 0 {
		t.Fatalf("legacy row requirements must default empty: %+v", got.Requirements)
	}
}
```

- [ ] **Step 3: Run the tests to verify they FAIL (compile error)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/db/ 2>&1 | tail -5`
Expected: build FAILS — `p.Requirements undefined` / `undefined: Requirement`.

- [ ] **Step 4: Add the `Requirement` type + `Project.Requirements` field**

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

- [ ] **Step 5: Wire `requirements` into the column list, scan, insert, update**

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

- [ ] **Step 6: Run the tests to verify they PASS**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/db/ 2>&1 | tail -5`
Expected: `ok  	agentmon/hubd/internal/db`.

- [ ] **Step 7: Run the full Go gate**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...`
Expected: all `ok` (no `FAIL`).

- [ ] **Step 8: Commit**

```bash
git add hubd/internal/db/migrations/0009_requirements.sql hubd/internal/db/projects.go hubd/internal/db/projects_test.go
git commit -m "feat(db): project requirements column + round-trip (epic #1)"
```

---

## Task 2: API layer — DTO, create/PATCH wiring, stable-id normalization

**Files:**
- Create: `hubd/internal/api/requirements.go`
- Create: `hubd/internal/api/requirements_test.go`
- Modify: `hubd/internal/api/orchestrator.go`
- Test: `hubd/internal/api/orchestrator_test.go`

**Interfaces:**
- Consumes (from Task 1): `db.Requirement`, `db.Project.Requirements`.
- Produces: `slugify(string) string`, `normalizeRequirements([]db.Requirement) []db.Requirement`;
  `projectDTO.Requirements []db.Requirement \`json:"requirements"\``; create + PATCH
  accept/return `requirements`.

- [ ] **Step 1: Write the failing unit tests** (`hubd/internal/api/requirements_test.go`)

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
	got := normalizeRequirements([]db.Requirement{
		{Text: "Always use RLS"},                          // id derived from text
		{ID: "wcag", Text: "WCAG 2.2 renamed to 2.3"},     // provided id preserved despite text edit
		{Text: "   "},                                     // blank text dropped
		{Text: "!!!"},                                     // unsluggable text dropped
		{Text: "  No PII in logs  ", CheckCmd: "  s.sh "}, // trimmed
	})
	want := []db.Requirement{
		{ID: "always-use-rls", Text: "Always use RLS"},
		{ID: "wcag", Text: "WCAG 2.2 renamed to 2.3"},
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
```

- [ ] **Step 2: Run to verify FAIL (compile error)**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ 2>&1 | tail -5`
Expected: build FAILS — `undefined: slugify` / `undefined: normalizeRequirements`.

- [ ] **Step 3: Implement `slugify` + `normalizeRequirements`** (`hubd/internal/api/requirements.go`)

```go
package api

import (
	"regexp"
	"strings"

	"agentmon/hubd/internal/db"
)

// nonSlugRe collapses every run of non-[a-z0-9] characters to a single dash.
var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a stable lowercase-kebab id from a requirement's text:
// lowercase, non-alphanumeric runs → single dash, trimmed of leading/trailing
// dashes. "Always use RLS" → "always-use-rls".
func slugify(s string) string {
	s = nonSlugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// normalizeRequirements shapes author input into the stored form:
//   - trims id/text/check_cmd,
//   - drops any row with blank text (text is the review lens — a requirement
//     without it is meaningless),
//   - derives id from text ONLY when the author supplied none; a provided id is
//     preserved verbatim so it stays stable across later text edits (the id is
//     the join key the epic-02 gate/verdict match on — re-deriving it would
//     silently break enforcement),
//   - drops any row whose resolved id is still empty (text with no slug-able
//     characters) so every stored requirement has a usable join key.
//
// Duplicate ids are intentionally NOT collapsed: this epic stores the field
// inert, and de-duplication belongs with the gate that consumes ids (epic-02).
func normalizeRequirements(in []db.Requirement) []db.Requirement {
	out := make([]db.Requirement, 0, len(in))
	for _, r := range in {
		r.ID = strings.TrimSpace(r.ID)
		r.Text = strings.TrimSpace(r.Text)
		r.CheckCmd = strings.TrimSpace(r.CheckCmd)
		if r.Text == "" {
			continue
		}
		if r.ID == "" {
			r.ID = slugify(r.Text)
		}
		if r.ID == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 4: Run the unit tests to verify PASS**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run 'TestSlugify|TestNormalizeRequirements' -v 2>&1 | tail -12`
Expected: both PASS.

- [ ] **Step 5: Wire `requirements` into the DTO + handlers** (`hubd/internal/api/orchestrator.go`)

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

In the same handler, set `Requirements` (normalized) when building `pr`:

```go
		pr := db.Project{ID: uuid.NewString(), Name: in.Name, Repo: in.Repo, ServerID: in.ServerID, Target: in.Target, Workdir: in.Workdir, BaseBranch: in.BaseBranch, Provider: in.Provider, RequiredReviews: in.RequiredReviews, Requirements: normalizeRequirements(in.Requirements), MaxParallel: in.MaxParallel, RequireCI: in.RequireCI}
```

In the **PATCH** handler's `in` struct, add the pointer field after
`RequiredReviews`:

```go
			RequiredReviews *[]string         `json:"required_reviews"`
			Requirements    *[]db.Requirement `json:"requirements"`
```

And apply it beside the `RequiredReviews` block (absent = unchanged):

```go
		if in.RequiredReviews != nil {
			pr.RequiredReviews = *in.RequiredReviews
		}
		if in.Requirements != nil {
			pr.Requirements = normalizeRequirements(*in.Requirements)
		}
```

- [ ] **Step 6: Write the failing API round-trip test** (append to `hubd/internal/api/orchestrator_test.go`)

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
		t.Fatalf("create requirements = %+v", created.Requirements)
	}
	// Persisted for the board/list surfaces.
	got, _ := database.GetProject(ctx, created.ID)
	if len(got.Requirements) != 2 || got.Requirements[0].ID != "always-use-rls" {
		t.Fatalf("persisted requirements = %+v", got.Requirements)
	}

	// PATCH: editing a requirement's text must NOT change its id (stability).
	patch := `{"requirements":[{"id":"always-use-rls","text":"Always use row-level security"}]}`
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/"+created.ID, patch)
	r.SetPathValue("id", created.ID)
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	got, _ = database.GetProject(ctx, created.ID)
	if len(got.Requirements) != 1 || got.Requirements[0].ID != "always-use-rls" ||
		got.Requirements[0].Text != "Always use row-level security" {
		t.Fatalf("patch must keep id stable while updating text: %+v", got.Requirements)
	}
}
```

- [ ] **Step 7: Run the API tests to verify PASS**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ 2>&1 | tail -5`
Expected: `ok  	agentmon/hubd/internal/api`.

- [ ] **Step 8: Run the full Go gate**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...`
Expected: all `ok`.

- [ ] **Step 9: Commit**

```bash
git add hubd/internal/api/requirements.go hubd/internal/api/requirements_test.go hubd/internal/api/orchestrator.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(api): accept + return project requirements, derive stable ids (epic #1)"
```

---

## CHECKPOINT 1 — backend seam (data/schema + API + slug logic)

The highest-judgment code (stable-id derivation) and the whole DB → API data flow
land here. Reviewed before any frontend is built.

- [ ] **Step 1:** `agentmon report --epic 1 --stage reviewing`
- [ ] **Step 2:** segment base = `git merge-base HEAD origin/main`.
- [ ] **Step 3:** run `/multi-review <segment-base>..HEAD --codex` in this session.
- [ ] **Step 4:** route outcomes — FIX already applied+committed by the review
  (verify suite green); DISCUSS → escalate with the item as the note; NITPICKs →
  record in the report file only.
- [ ] **Step 5:** write the consolidated report to `docs/reviews/epic-1-cp1.md`,
  tick this checkpoint appending the reviewed SHA, commit both:
  `docs: epic #1 checkpoint 1 review`.
- [ ] **Step 6:** `agentmon report --epic 1 --stage implementing` and continue.

---

## Task 3: Web contract mirror

**Files:**
- Modify: `web/src/lib/contracts.ts`

**Interfaces:**
- Produces (consumed by Task 4): `Requirement` interface;
  `ProjectDTO.requirements?`; `ProjectCreateRequest.requirements?`;
  `ProjectPatchRequest.requirements?`.

- [ ] **Step 1: Add the `Requirement` interface + wire it into the project types**

In `web/src/lib/contracts.ts`, add the interface just above `ProjectDTO`:

```ts
// One platform-invariant requirement (mirrors Go db.Requirement). `id` is a
// stable kebab slug the epic-02 gate matches on; `text` doubles as the review
// lens; `check_cmd` optionally certifies it via a shell exit code.
export interface Requirement { id: string; text: string; check_cmd?: string; }
```

Add `requirements?` to `ProjectDTO` (optional — see design note; the API always
sends it, but an older hub in the rollout window may not):

```ts
export interface ProjectDTO {
  id: string; name: string; repo: string; server_id: string; target: string;
  workdir: string; base_branch: string; provider: string;
  required_reviews: string[] | null; max_parallel: number; paused: boolean;
  require_ci: boolean; pinned: boolean; requirements?: Requirement[];
  counts?: Record<string, number>;
}
```

Add `requirements?` to both request types:

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

- [ ] **Step 2: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/contracts.ts
git commit -m "feat(web): mirror Requirement contract (epic #1)"
```

---

## Task 4: Web UI — ProjectForm requirements editor + tests

**Files:**
- Modify: `web/src/components/board/ProjectForm.tsx`
- Test: `web/src/components/board/ProjectForm.test.tsx`

**Interfaces:**
- Consumes (from Task 3): `Requirement`, `ProjectDTO.requirements`,
  `ProjectCreateRequest.requirements`, `ProjectPatchRequest.requirements`.

- [ ] **Step 1: Write the failing component tests** (append to `ProjectForm.test.tsx`, and extend the mocks)

First, extend the hoisted mocks + the `api-client` mock so `patchProject` is
stubbed. Change the top of the file:

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

Append this describe block:
```tsx
describe("ProjectForm requirements", () => {
  beforeEach(() => { h.createProject.mockReset(); h.patchProject.mockReset(); });

  it("sends added requirement rows on create (id blank — server derives it)", async () => {
    h.createProject.mockResolvedValue({ id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, requirements: [] });
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
    const project: ProjectDTO = { id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, requirements: [{ id: "rls", text: "Always use RLS" }, { id: "wcag", text: "WCAG 2.2 AA" }] };
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

- [ ] **Step 2: Run to verify FAIL**

Run: `cd web && npm run test:run -- ProjectForm 2>&1 | tail -20`
Expected: the two new tests FAIL (no "Add requirement" button / no requirement inputs).

- [ ] **Step 3: Implement the requirements editor** (`web/src/components/board/ProjectForm.tsx`)

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

Render the editor between the `pf-reviews` field and the create-only
`max_parallel` block (so it shows in BOTH modes, like Required reviews). Insert
right after the `{field("pf-reviews", ...)}` line:
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

- [ ] **Step 4: Run the component tests to verify PASS**

Run: `cd web && npm run test:run -- ProjectForm 2>&1 | tail -20`
Expected: all ProjectForm tests PASS.

- [ ] **Step 5: Run the full web gate**

Run: `cd web && npm run typecheck && npm run test:run`
Expected: typecheck clean; all tests pass.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/board/ProjectForm.tsx web/src/components/board/ProjectForm.test.tsx
git commit -m "feat(web): edit project requirements in settings form (epic #1)"
```

---

## Finish (runner Step 7 — not a task)

After Task 4: rebase onto `origin/main`, run the final whole-branch
`/multi-review <merge-base>..HEAD --codex` (covers the frontend seam + everything),
write learnings back into `CLAUDE.md` if any, run the full gate one last time,
push, open the PR with the verdict block, report `pr_open`.

## Self-review notes

- **Spec coverage:** every acceptance criterion maps to a task (table above).
- **Placeholder scan:** no TBD/TODO; all code shown in full.
- **Type consistency:** `Requirement{ID,Text,CheckCmd}` (Go) ↔
  `Requirement{id,text,check_cmd?}` (TS); `normalizeRequirements`/`slugify` names
  match across Task 2 and its tests; `projectOut` positional literal updated in
  lockstep with the `projectDTO` field (compile-checked).
