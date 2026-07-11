# Orchestrator Board UI — Implementation Plan (Sub-project 3 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The orchestrator's web face — an All-projects/per-project board (kanban + timeline), epic drawer with plan review, live terminal preview and actions, full project registration/edit/delete UI with a doctor-verify step, and escalation alerts that deep-link into the drawer.

**Architecture:** Four small hub API additions (all-projects board snapshot endpoint, GitHub-contents plan proxy, project PATCH/DELETE, `epic_id` in the board push payload); everything else is web SPA. Data flow is Query-centric (spec D7): TanStack Query owns server state via `GET /orchestrator/board` + the existing per-project board endpoint; the existing SSE stream (`/orchestrator/events`) seeds a zustand attention store (header badge, in-app toasts) and *invalidates* queries on deltas — the client never patches board state itself.
Design doc (authoritative for WHY): `docs/superpowers/specs/2026-07-11-orchestrator-board-ui-design.md`. "Spec §N" below refers to it. Visual reference: `docs/superpowers/specs/2026-07-10-orchestrator-board-mockup.html`.

**Tech Stack:** Go 1.26 stdlib (hub); React 18 + TanStack Router/Query + zustand + Tailwind + vitest (web). **One new web dependency: `react-markdown` + `remark-gfm`** (plan panel, Task 16). No new Go dependencies.

## Global Constraints

- Branch: create `feat/board-ui` from `main` at Task 1 Step 1; all work stays on it. **Never push. Never add a Co-Authored-By or any other trailer.** Commit messages exactly as given per task.
- Go gate before EVERY commit that touches Go (require green):
  `cd /root/agentmon/shared && go build ./... && go test ./... && cd /root/agentmon/agent && go build ./... && go test ./... && cd /root/agentmon/hubd && go build ./... && go test ./...`
  (If the Go build cache is read-only in your sandbox: `export GOCACHE=/tmp/agentmon-go-cache` first.)
- Web gate before EVERY commit that touches `web/` (require green):
  `cd /root/agentmon/web && npm run typecheck && npm run test:run`
- Touch only files the current task lists. Do not reformat unrelated code.
- Where a task anchors to existing code ("mirror X at file:line"), open that file first — this plan is authoritative for WHERE to look, the code on disk for exact names.
- **Stop-don't-improvise:** any mismatch between this plan and the repo — a signature, a path, a test that fails for an unexplainable reason — STOP the task, record the mismatch, and report. Trivial mechanical fixes (missing import, gofmt, a tsc-only type annotation) excepted.
- **Checkpoint stops are hard stops:** Tasks 6, 12, 17, and 22 end with a CHECKPOINT step. STOP there, report (tasks completed, suite status), and WAIT for explicit fix instructions or an explicit "continue". Do not begin the next task on your own.
- Hub JSON errors: `{"error":"…"}` via `writeJSONError` (`hubd/internal/api/servers.go:168`). User-facing state errors are 409; infra errors are logged and returned generic (mirror `writeActionError`, `hubd/internal/api/orchestrator.go:362`).
- Web: status colors are Tailwind palette classes via meta maps (the `STATE_META` convention, `web/src/lib/state.ts:29`) — never hex in JSX. Components take callbacks, not `<Link>`, so they test without a router harness. Toasts via `sonner` (`toast(msg, {description})`, `toast.error`). All new UI is dark-theme (the app is dark-only).
- Web: every new query key comes from a builder in `api-client.ts` (house rule; only `serversKey`/`sessionsKey` exist today).

## Shared type registry (single source of truth for cross-task names)

### Go (hub)

| Name | Defined in | Shape |
|---|---|---|
| `db.UpdateProject` | Task 1 | `func (d *DB) UpdateProject(ctx context.Context, p Project) (bool, error)` — rewrites name/workdir/target/base_branch/provider/required_reviews by `p.ID` |
| `db.DeleteProject` | Task 1 | `func (d *DB) DeleteProject(ctx context.Context, id string) (found bool, active int, err error)` — refuses (active>0) while non-terminal epics exist |
| `audit.ProjectUpdate` | Task 2 | `func (r *Recorder) ProjectUpdate(ctx context.Context, principalID, resource, ip, ua string)` |
| `audit.ProjectDelete` | Task 2 | `func (r *Recorder) ProjectDelete(ctx context.Context, principalID, resource, repo, ip, ua string)` |
| `api.OrchestratorProjectPatchHandler` | Task 2 | `func (d Deps) OrchestratorProjectPatchHandler() http.HandlerFunc` — `PATCH /api/v1/orchestrator/projects/{id}` |
| `api.OrchestratorProjectDeleteHandler` | Task 2 | `func (d Deps) OrchestratorProjectDeleteHandler() http.HandlerFunc` — `DELETE /api/v1/orchestrator/projects/{id}` |
| `api.(Deps).boardSnapshot` | Task 3 | `func (d Deps) boardSnapshot(ctx context.Context) ([]projectDTO, []epicDTO, error)` — non-nil slices, shared by events + all-board handlers |
| `api.OrchestratorAllBoardHandler` | Task 3 | `func (d Deps) OrchestratorAllBoardHandler() http.HandlerFunc` — `GET /api/v1/orchestrator/board` → `{orchestrator_enabled, projects, epics}` |
| `github.GetContents` | Task 4 | `func (c *Client) GetContents(ctx context.Context, repo, path, ref string) ([]byte, error)` |
| `github.ErrTooLarge` | Task 4 | `errors.New("github: file too large")` |
| `api.ContentsFetcher` | Task 5 | `interface { GetContents(ctx context.Context, repo, path, ref string) ([]byte, error) }`; new Deps field `Contents ContentsFetcher` |
| `api.planDocPath` | Task 5 | `func planDocPath(needs string, issue int) string` (pure; sanitizing fallback) |
| `api.OrchestratorEpicPlanHandler` | Task 5 | `GET /api/v1/orchestrator/projects/{id}/epics/{epicID}/plan` → `{path, ref, markdown}` |
| push payload `epic_id` | Task 6 | added to `dispatchBoardPush` payload (`hubd/internal/orchestrator/push.go:42`) |

### Web (TypeScript)

| Name | Defined in | Shape |
|---|---|---|
| `EpicStage`, `ProjectDTO`, `EpicDTO`, `EpicEventDTO`, `AllBoardResponse`, `ProjectBoardResponse`, `EpicPlanResponse`, `BoardDeltaFrame`, `ProjectCreateRequest`, `ProjectPatchRequest`, `EpicActionRequest` | Task 7 | `lib/contracts.ts` — mirrors of the hub DTOs (see Task 7 for exact fields) |
| `allBoardKey()`, `projectBoardKey(id)`, `epicPlanKey(pid, eid)` | Task 7 | query-key builders; ALL board keys share the `["board", …]` prefix so one `invalidateQueries({queryKey:["board"]})` covers every board query |
| `getAllBoard`, `getProjectBoard`, `getEpicPlan`, `epicAction`, `createProject`, `patchProject`, `deleteProject` | Task 7 | `lib/api-client.ts` fetchers |
| `lib/board.ts`: `PLAN_GATE_PREFIX`, `BoardColumn`, `COLUMN_ORDER`, `COLUMN_META`, `STAGE_META`, `stageMeta(stage)`, `groupByColumn(epics)`, `boardStats(epics)`, `isPlanGate(needs)`, `parseVerdict(raw?)`, `cardProvider(labels, fallback)`, `mergeMode(labels)`, `sessionSlug(prefix, name)`, `fmtElapsed(iso, now)` | Task 7 | pure board logic (see Task 7 code) |
| `useBoardAttention`, `useNeedsTotal()`, `needsByProject(map)` | Task 8 | `store/board.ts` — `attention: Map<epicId, projectId>` |
| `BoardStream` | Task 8 | `lib/board-stream.ts` — mirrors `StateStream`; events `board-snapshot` + `board` |
| `useBoardStream(deps?, onAttention?)` | Task 8 | `hooks/useBoardStream.ts` — snapshot→store+invalidate, delta→store+debounced invalidate(300ms)+attention callback |
| `useEpicAttentionAlerts()` | Task 8 | `hooks/useEpicAttentionAlerts.ts` — returns `(f: BoardDeltaFrame) => void` (toast + audio + hidden-tab Notification) |
| `ConfirmButton` | Task 9 | `components/board/ConfirmButton.tsx` — `{label, confirmLabel?, variant?, size?, disabled?, className?, onConfirm}` two-step confirm |
| `useEpicActions(projectId)` | Task 9 | `hooks/useEpicActions.ts` — `{act(body, success?): Promise<boolean>, busy: string|null}` |
| `StageChip` | Task 9 | `components/board/StageChip.tsx` — `{stage: string, className?}` dot+label chip |
| `EpicCard` | Task 10 | `components/board/EpicCard.tsx` — `{epic, project?, showProject?, liveState?, onOpen}` |
| `BoardView`, `BoardStatsStrip` | Task 11 | `components/board/BoardView.tsx` — `{epics, projects: Map<string,ProjectDTO>, showProject, liveStateOf(e), onOpenEpic(id)}` |
| `prefs.projectsBoardLayout` | Task 11 | `"stack" \| "columns"` + `setProjectsBoardLayout` (persisted) |
| `ProjectsSearch`, `validateProjectsSearch` | Task 12 | `routes/projects.tsx` — `{tab: "board"\|"timeline", epic: string}` |
| `ProjectsIndexRoute`, `ProjectDetailRoute`, `ProjectSwitcher` | Task 12 | `routes/projects.tsx` + `components/board/ProjectSwitcher.tsx` |
| `lib/gantt.ts`: `GanttRange`, `GanttWindow`, `ganttWindow(epics, now, range)`, `GanttTick`, `ganttTicks(w)`, `GanttBar`, `ganttBar(e, w, now)`, `fmtDur(ms)`, `arrowPath(from, to)` | Task 13 | pure gantt math (see Task 13 code) |
| `TimelineView` | Task 14 | `components/board/TimelineView.tsx` — `{epics, projects, groupByProject, onOpenEpic}` |
| `EpicDrawer` | Task 15 | `components/board/EpicDrawer.tsx` — `{epic, project, onClose}` |
| `PlanPanel` | Task 16 | `components/board/PlanPanel.tsx` — `{epic}` |
| `TerminalPreview` | Task 17 | `components/board/TerminalPreview.tsx` — `{project, epic, onOpenFull}` |
| `openOrFocusSession` | Task 18 | `components/board/open-session.ts` — `(opts: {serverId, serverName, target, name, cwd?, command?}, isDesktop: boolean, navigate) => Promise<void>` |
| `ProjectHeader` | Task 18 | `components/board/ProjectHeader.tsx` — `{project, epics, onEdit}` |
| `ProjectForm`, `DoctorVerify` | Task 19 | `components/board/ProjectForm.tsx` — create mode + verify card |
| edit/delete mode of `ProjectForm` | Task 20 | `{mode: "edit", project, onDone}` |
| `lib/push-payload.ts`: `EpicPush`, `isEpicPush(d)`, `epicNotification(d)` | Task 21 | pure payload→notification builders (unit-testable; `sw.ts` consumes) |
| `registerSwNavigation()` | Task 21 | `lib/sw-navigate.ts` — service-worker → router deep-link bridge |

---

### Task 1: DB — UpdateProject + DeleteProject

**Files:**
- Create: branch `feat/board-ui`
- Modify: `hubd/internal/db/projects.go` (append)
- Test: `hubd/internal/db/projects_test.go` (append; create if absent — check first)

**Interfaces:**
- Consumes: `execFound` (`hubd/internal/db/epics.go:196`), `marshalStrings` (`projects.go:92`), `terminalStagesSQL` (`epics.go:56`).
- Produces: `UpdateProject(ctx, p Project) (bool, error)`; `DeleteProject(ctx, id string) (found bool, active int, err error)` for Task 2.

- [ ] **Step 1: Create the branch**

```bash
cd /root/agentmon && git checkout -b feat/board-ui
```

- [ ] **Step 2: Write the failing tests**

Check whether `hubd/internal/db/projects_test.go` exists; append to it (or create with package header `package db` and imports `context`, `path/filepath`, `testing`). If an existing project-fixture helper exists in the db test files, reuse it; otherwise use this inline setup:

```go
func projDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "h1", Name: "h1", Hostname: "h1", URL: "http://a", Status: "active", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProject(ctx, Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	return d, ctx
}

func TestUpdateProject(t *testing.T) {
	d, ctx := projDB(t)
	ok, err := d.UpdateProject(ctx, Project{ID: "p1", Name: "p2", Workdir: "/w2", Target: "tgt", BaseBranch: "dev", Provider: "codex", RequiredReviews: []string{"cross-model"}})
	if err != nil || !ok {
		t.Fatalf("update: ok=%v err=%v", ok, err)
	}
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "p2" || p.Workdir != "/w2" || p.Target != "tgt" || p.BaseBranch != "dev" || p.Provider != "codex" || len(p.RequiredReviews) != 1 || p.RequiredReviews[0] != "cross-model" {
		t.Fatalf("got %+v", p)
	}
	if p.Repo != "o/r" || p.ServerID != "h1" {
		t.Fatalf("immutable fields changed: %+v", p)
	}
	if ok, err := d.UpdateProject(ctx, Project{ID: "nope", Name: "x", Workdir: "/w", BaseBranch: "main", Provider: "claude"}); err != nil || ok {
		t.Fatalf("missing project: ok=%v err=%v", ok, err)
	}
}

func TestDeleteProjectRefusesActiveEpics(t *testing.T) {
	d, ctx := projDB(t)
	e, err := d.UpsertEpicIssue(ctx, Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	if err != nil {
		t.Fatal(err)
	}
	found, active, err := d.DeleteProject(ctx, "p1")
	if err != nil || !found || active != 1 {
		t.Fatalf("active refuse: found=%v active=%d err=%v", found, active, err)
	}
	if _, err := d.GetProject(ctx, "p1"); err != nil {
		t.Fatalf("project must survive a refused delete: %v", err)
	}
	// Terminal epic (with an event row) → delete succeeds and cascades.
	if ok, err := d.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "n", "t2"); err != nil || !ok {
		t.Fatalf("transition: %v", err)
	}
	if err := d.AppendEpicEvent(ctx, EpicEvent{ID: "ev1", EpicID: e.ID, FromStage: "queued", ToStage: "canceled", Source: "user:u1", Ts: "t2"}); err != nil {
		t.Fatal(err)
	}
	found, active, err = d.DeleteProject(ctx, "p1")
	if err != nil || !found || active != 0 {
		t.Fatalf("delete: found=%v active=%d err=%v", found, active, err)
	}
	if _, err := d.GetProject(ctx, "p1"); err == nil {
		t.Fatal("project must be gone")
	}
	if _, _, err := d.DeleteProject(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if found, _, _ := d.DeleteProject(ctx, "p1"); found {
		t.Fatal("second delete must report not-found")
	}
}
```

Note: `TransitionEpic`'s signature is `(ctx, id, from, to, source, note, now string) (bool, error)` (`epics.go:158`) — if the on-disk signature differs, STOP (plan mismatch).

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run 'TestUpdateProject|TestDeleteProject' -v`
Expected: FAIL — `d.UpdateProject undefined` / `d.DeleteProject undefined`.

- [ ] **Step 4: Implement**

Append to `hubd/internal/db/projects.go`:

```go
// UpdateProject rewrites the editable registration fields (typo repair from the
// board UI). repo and server_id are deliberately NOT updatable: existing epics
// belong to the repo, and moving hosts mid-flight would orphan runner sessions
// (spec §5.3). paused/max_parallel/require_ci keep their action verbs.
func (d *DB) UpdateProject(ctx context.Context, p Project) (bool, error) {
	return d.execFound(ctx,
		`UPDATE projects SET name = ?, workdir = ?, target = ?, base_branch = ?, provider = ?, required_reviews = ?, updated_at = datetime('now') WHERE id = ?`,
		p.Name, p.Workdir, p.Target, p.BaseBranch, p.Provider, marshalStrings(p.RequiredReviews), p.ID)
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/db/ -run 'TestUpdateProject|TestDeleteProject' -v`
Expected: PASS.

- [ ] **Step 6: Full gate + commit**

Run the Go gate (Global Constraints). Then:

```bash
git add hubd/internal/db/projects.go hubd/internal/db/projects_test.go
git commit -m "feat(hub): db UpdateProject + transactional DeleteProject with active-epic guard"
```

---

### Task 2: API — PATCH/DELETE project handlers, audit, routes

**Files:**
- Modify: `hubd/internal/audit/audit.go` (append two methods after `ProjectRegister`, ~line 88)
- Modify: `hubd/internal/api/orchestrator.go` (append two handlers)
- Modify: `hubd/internal/api/router.go` (two routes after line 59)
- Test: `hubd/internal/api/orchestrator_test.go` (append)

**Interfaces:**
- Consumes: Task 1's `db.UpdateProject`/`db.DeleteProject`; `projectOut` (`orchestrator.go:76`); `authorizeOr403`, `writeJSON`, `writeJSONError`; `maxOrchestratorBody` (`orchestrator.go:23`); `github.IsValidRepo`.
- Produces: `OrchestratorProjectPatchHandler`, `OrchestratorProjectDeleteHandler`; `audit.ProjectUpdate`, `audit.ProjectDelete`.

- [ ] **Step 1: Write the failing tests**

Append to `hubd/internal/api/orchestrator_test.go` (reuses `orchDB`, `orchReq`, `fakeOrch`, `captureSink` from this file / `sessions_test.go` / `orchestrator_webhook_test.go`):

```go
func TestProjectPatch(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	r, w := orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"name":"p2","workdir":"/w2","provider":"codex","required_reviews":["cross-model"]}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	p, _ := database.GetProject(ctx, "p1")
	if p.Name != "p2" || p.Workdir != "/w2" || p.Provider != "codex" || len(p.RequiredReviews) != 1 {
		t.Fatalf("got %+v", p)
	}
	if p.BaseBranch != "main" {
		t.Fatalf("absent field must be unchanged, got %q", p.BaseBranch)
	}
	if _, ok := sink.find("project.update"); !ok {
		t.Fatal("missing audit entry")
	}

	for body, why := range map[string]string{
		`{"repo":"o/x"}`:        "repo immutable",
		`{"server_id":"h2"}`:    "server_id immutable",
		`{"provider":"gpt"}`:    "bad provider",
		`{"name":""}`:           "empty name",
		`{"workdir":""}`:        "empty workdir",
		`{"base_branch":""}`:    "empty base_branch",
	} {
		r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", body)
		r.SetPathValue("id", "p1")
		d.OrchestratorProjectPatchHandler()(w, r)
		if w.Code != 400 {
			t.Fatalf("%s: want 400, got %d", why, w.Code)
		}
	}

	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/nope", `{"name":"x"}`)
	r.SetPathValue("id", "nope")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("missing project: want 404, got %d", w.Code)
	}

	d.Orch = nil
	r, w = orchReq("PATCH", "/api/v1/orchestrator/projects/p1", `{"name":"x"}`)
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectPatchHandler()(w, r)
	if w.Code != 503 {
		t.Fatalf("dormant: want 503, got %d", w.Code)
	}
}

func TestProjectDelete(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 1, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	sink := &captureSink{}
	d := Deps{DB: database, Orch: &fakeOrch{}, Audit: audit.NewRecorder(sink)}

	r, w := orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "1 active epic") {
		t.Fatalf("active refuse = %d %s", w.Code, w.Body.String())
	}

	database.TransitionEpic(ctx, e.ID, "queued", "canceled", "user:u1", "", "t2")
	r, w = orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if _, ok := sink.find("project.delete"); !ok {
		t.Fatal("missing audit entry")
	}

	r, w = orchReq("DELETE", "/api/v1/orchestrator/projects/p1", "")
	r.SetPathValue("id", "p1")
	d.OrchestratorProjectDeleteHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("gone: want 404, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestProjectPatch|TestProjectDelete' -v`
Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement audit methods**

In `hubd/internal/audit/audit.go`, after `ProjectRegister` (~line 88):

```go
func (r *Recorder) ProjectUpdate(ctx context.Context, principalID, resource, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "project.update",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: "{}"})
}

func (r *Recorder) ProjectDelete(ctx context.Context, principalID, resource, repo, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"repo": repo})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "project.delete",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}
```

- [ ] **Step 4: Implement handlers**

Append to `hubd/internal/api/orchestrator.go`:

```go
// OrchestratorProjectPatchHandler edits the registration fields. Partial
// semantics via pointer fields: absent = unchanged. repo/server_id are
// immutable (spec §5.3) — rejecting them loudly beats silently ignoring.
func (d Deps) OrchestratorProjectPatchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		var in struct {
			Name            *string   `json:"name"`
			Repo            *string   `json:"repo"`
			ServerID        *string   `json:"server_id"`
			Workdir         *string   `json:"workdir"`
			Target          *string   `json:"target"`
			BaseBranch      *string   `json:"base_branch"`
			Provider        *string   `json:"provider"`
			RequiredReviews *[]string `json:"required_reviews"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOrchestratorBody)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if in.Repo != nil {
			writeJSONError(w, http.StatusBadRequest, "repo cannot be changed — register a new project")
			return
		}
		if in.ServerID != nil {
			writeJSONError(w, http.StatusBadRequest, "server cannot be changed")
			return
		}
		pr, err := d.DB.GetProject(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if in.Name != nil {
			pr.Name = *in.Name
		}
		if in.Workdir != nil {
			pr.Workdir = *in.Workdir
		}
		if in.Target != nil {
			pr.Target = *in.Target
		}
		if in.BaseBranch != nil {
			pr.BaseBranch = *in.BaseBranch
		}
		if in.Provider != nil {
			pr.Provider = *in.Provider
		}
		if in.RequiredReviews != nil {
			pr.RequiredReviews = *in.RequiredReviews
		}
		if pr.Name == "" || pr.Workdir == "" || pr.BaseBranch == "" {
			writeJSONError(w, http.StatusBadRequest, "missing required field")
			return
		}
		if pr.Provider != "claude" && pr.Provider != "codex" {
			writeJSONError(w, http.StatusBadRequest, "provider must be claude or codex")
			return
		}
		found, err := d.DB.UpdateProject(r.Context(), pr)
		if err != nil {
			// Most likely the UNIQUE(name) constraint; the DB error text is not
			// for browsers.
			writeJSONError(w, http.StatusBadRequest, "update failed (name already in use?)")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectUpdate(r.Context(), p.ID, "project:"+id, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusOK, projectOut(pr, nil))
	}
}

// OrchestratorProjectDeleteHandler removes a project once nothing is running:
// the DB layer refuses (found, active>0) while non-terminal epics exist.
func (d Deps) OrchestratorProjectDeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorControl, "project:"+id)
		if !ok {
			return
		}
		if d.Orch == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		pr, err := d.DB.GetProject(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		found, active, err := d.DB.DeleteProject(r.Context(), id)
		if err != nil {
			log.Printf("api: project delete: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if active > 0 {
			plural := "s"
			if active == 1 {
				plural = ""
			}
			writeJSONError(w, http.StatusConflict, fmt.Sprintf("project has %d active epic%s — cancel or finish them first", active, plural))
			return
		}
		if d.Audit != nil {
			d.Audit.ProjectDelete(r.Context(), p.ID, "project:"+id, pr.Repo, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
```

Add `"fmt"` to the imports of `orchestrator.go` if not present.

- [ ] **Step 5: Register routes**

In `hubd/internal/api/router.go`, after the existing orchestrator routes (line 59):

```go
	mux.Handle("PATCH /api/v1/orchestrator/projects/{id}", rd.Auth.RequireAuth(rd.API.OrchestratorProjectPatchHandler()))
	mux.Handle("DELETE /api/v1/orchestrator/projects/{id}", rd.Auth.RequireAuth(rd.API.OrchestratorProjectDeleteHandler()))
```

(`RequireAuth` already enforces CSRF on PATCH/DELETE — `hubd/internal/authn/middleware.go:66-72`.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestProjectPatch|TestProjectDelete' -v`
Expected: PASS.

- [ ] **Step 7: Full gate + commit**

```bash
git add hubd/internal/audit/audit.go hubd/internal/api/orchestrator.go hubd/internal/api/router.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(hub): project PATCH/DELETE API with audit + immutable repo/server guards"
```

---

### Task 3: API — shared board snapshot + `GET /orchestrator/board`

**Files:**
- Modify: `hubd/internal/api/orchestrator.go` (append `boardSnapshot` + handler)
- Modify: `hubd/internal/api/orchestrator_events.go` (use the helper; lines 29-48)
- Modify: `hubd/internal/api/router.go` (one route)
- Test: `hubd/internal/api/orchestrator_test.go` (append)

**Interfaces:**
- Consumes: `projectOut`, `toEpicDTO`, `d.DB.ListProjects`, `d.DB.ListBoardEpics`.
- Produces: `boardSnapshot(ctx) ([]projectDTO, []epicDTO, error)` (non-nil slices); `OrchestratorAllBoardHandler` → `{"orchestrator_enabled": bool, "projects": [...], "epics": [...]}`.

- [ ] **Step 1: Write the failing test**

Append to `hubd/internal/api/orchestrator_test.go`:

```go
func TestAllBoard(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, Title: "T", IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	d := Deps{DB: database, Orch: &fakeOrch{}}
	r, w := orchReq("GET", "/api/v1/orchestrator/board", "")
	d.OrchestratorAllBoardHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("board = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Enabled  bool              `json:"orchestrator_enabled"`
		Projects []json.RawMessage `json:"projects"`
		Epics    []json.RawMessage `json:"epics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || len(out.Projects) != 1 || len(out.Epics) != 1 {
		t.Fatalf("got %+v", out)
	}

	// Dormant hub: enabled=false, and empty slices must be [] not null.
	empty, err := db.Open(filepath.Join(t.TempDir(), "e.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { empty.Close() })
	d = Deps{DB: empty}
	r, w = orchReq("GET", "/api/v1/orchestrator/board", "")
	d.OrchestratorAllBoardHandler()(w, r)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, `"orchestrator_enabled":true`) {
		t.Fatalf("dormant = %d %s", w.Code, body)
	}
	if !strings.Contains(body, `"projects":[]`) || !strings.Contains(body, `"epics":[]`) {
		t.Fatalf("empty slices must marshal as [], got %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestAllBoard -v`
Expected: FAIL — `OrchestratorAllBoardHandler` undefined.

- [ ] **Step 3: Implement**

Append to `hubd/internal/api/orchestrator.go`:

```go
// boardSnapshot assembles the cross-project board (projects + bounded epics).
// One source of truth for both the SSE board-snapshot event and GET /board —
// the two must never drift. Slices are always non-nil so they marshal as [].
func (d Deps) boardSnapshot(ctx context.Context) ([]projectDTO, []epicDTO, error) {
	projects, err := d.DB.ListProjects(ctx)
	if err != nil {
		return nil, nil, err
	}
	projDTOs := make([]projectDTO, 0, len(projects))
	epics := make([]epicDTO, 0, 64)
	for _, pr := range projects {
		projDTOs = append(projDTOs, projectOut(pr, nil))
		es, err := d.DB.ListBoardEpics(ctx, pr.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range es {
			epics = append(epics, toEpicDTO(e))
		}
	}
	return projDTOs, epics, nil
}

// OrchestratorAllBoardHandler is the All-projects board query (spec §5.1).
// orchestrator_enabled tells the web "dormant hub" apart from "no projects".
func (d Deps) OrchestratorAllBoardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*"); !ok {
			return
		}
		projects, epics, err := d.boardSnapshot(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"orchestrator_enabled": d.Orch != nil,
			"projects":             projects,
			"epics":                epics,
		})
	}
}
```

In `hubd/internal/api/orchestrator_events.go`, replace the inline assembly (lines 30-48, from `projects, err := d.DB.ListProjects(r.Context())` through the closing brace of the epics loop) with:

```go
		projDTOs, epics, err := d.boardSnapshot(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
```

(The later `writeSSE(w, "board-snapshot", map[string]any{"projects": projDTOs, "epics": epics})` keeps its variable names — keep `projDTOs`/`epics` as above.)

**Dormant-hub fix (Finding 13):** the board stream is mounted app-wide in the web (Task 8), but this handler currently 503s when `d.BoardBcast == nil` (dormant hub, no github.token) — native EventSource then reconnect-loops every ~3s forever for every authenticated user. Replace the early `if d.BoardBcast == nil { writeJSONError(503) …}` (orchestrator_events.go:22-25) with an **idle stream**: set the SSE headers, send one empty `board-snapshot`, and heartbeat until the client disconnects (no Subscribe, no deltas). Add this near the top of the handler, BEFORE the `d.BoardBcast.Subscribe()` block:

```go
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "stream unsupported")
			return
		}
		if d.BoardBcast == nil {
			// Dormant hub: keep the stream OPEN and idle rather than 503, so the
			// app-wide EventSource doesn't reconnect-loop. No projects exist yet,
			// so the snapshot is empty and no deltas ever arrive.
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeSSE(w, "board-snapshot", map[string]any{"projects": []projectDTO{}, "epics": []epicDTO{}})
			flusher.Flush()
			hb := d.SSEHeartbeat
			if hb <= 0 {
				hb = 25 * time.Second
			}
			t := time.NewTicker(hb)
			defer t.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-t.C:
					fmt.Fprint(w, ": ping\n\n")
					flusher.Flush()
				}
			}
		}
```

(The existing handler already fetches `flusher` further down; if this early block adds a second `flusher, ok :=`, rename the later one or hoist this one — the executor should reconcile to a single declaration. The existing handler already imports `fmt` and `time`.)

Add a test `TestBoardEventsDormant`: `Deps{DB: database}` (no BoardBcast) → the handler writes a `board-snapshot` with empty arrays and does NOT 503. Reuse the `pipeResponseWriter` harness from `orchestrator_events_test.go`.

In `hubd/internal/api/router.go`, after line 59:

```go
	mux.Handle("GET /api/v1/orchestrator/board", rd.Auth.RequireAuth(rd.API.OrchestratorAllBoardHandler()))
```

- [ ] **Step 4: Run tests (new + existing events tests must stay green)**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestAllBoard|TestBoardEvents' -v`
Expected: PASS (the non-dormant SSE snapshot behavior is unchanged by the refactor).

- [ ] **Step 5: Full gate + commit**

```bash
git add hubd/internal/api/orchestrator.go hubd/internal/api/orchestrator_events.go hubd/internal/api/router.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(hub): GET /orchestrator/board with shared snapshot assembly + orchestrator_enabled flag"
```

---

### Task 4: GitHub client — GetContents

**Files:**
- Modify: `hubd/internal/github/client.go` (append)
- Test: `hubd/internal/github/client_test.go` (append)

**Interfaces:**
- Consumes: `do()` (`client.go:108` — JSON-only by design), `validRepo`, `validRef`, `refRe`, `ErrNotFound`.
- Produces: `GetContents(ctx, repo, path, ref string) ([]byte, error)`; `ErrTooLarge`; `validPath`.

- [ ] **Step 1: Write the failing test**

Append to `hubd/internal/github/client_test.go` (uses the `fakeGH` harness at `client_test.go:13`):

```go
func TestGetContents(t *testing.T) {
	plan := "# Plan\n\ndo the thing\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(plan))
	// GitHub wraps base64 at 60 cols; make sure we strip embedded newlines.
	wrapped := b64[:4] + "\n" + b64[4:]
	var seen []*http.Request
	srv := fakeGH(t, map[string]any{
		"GET /repos/o/r/contents/docs/plans/epic-7.md": map[string]any{
			"type": "file", "encoding": "base64", "size": len(plan), "content": wrapped,
		},
		"GET /repos/o/r/contents/big.md": map[string]any{
			"type": "file", "encoding": "base64", "size": 300 << 10, "content": "",
		},
		"GET /repos/o/r/contents/huge.md": map[string]any{
			"type": "file", "encoding": "none", "size": 2 << 20, "content": "",
		},
	}, nil, &seen)
	defer srv.Close()
	c := NewClient("tok")
	c.Base = srv.URL

	got, err := c.GetContents(context.Background(), "o/r", "docs/plans/epic-7.md", "epic/7-x")
	if err != nil || string(got) != plan {
		t.Fatalf("got %q err=%v", got, err)
	}
	if q := seen[0].URL.RawQuery; q != "ref=epic%2F7-x" {
		t.Fatalf("ref query = %q", q)
	}
	if _, err := c.GetContents(context.Background(), "o/r", "big.md", "main"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size guard: %v", err)
	}
	if _, err := c.GetContents(context.Background(), "o/r", "huge.md", "main"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("encoding=none guard: %v", err)
	}
	if _, err := c.GetContents(context.Background(), "o/r", "nope.md", "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestGetContentsRejectsBadInputs(t *testing.T) {
	c := NewClient("tok")
	c.Base = "http://127.0.0.1:1" // must never be dialed
	for _, tc := range []struct{ repo, path, ref string }{
		{"o/r", "../secrets", "main"},
		{"o/r", "/abs/path", "main"},
		{"o/r", "a b.md", "main"},
		{"o/r", "", "main"},
		{"o/r", "ok.md", "bad..ref"},
		{"bad repo", "ok.md", "main"},
	} {
		if _, err := c.GetContents(context.Background(), tc.repo, tc.path, tc.ref); err == nil {
			t.Fatalf("want reject for %+v", tc)
		}
	}
}
```

Add `"encoding/base64"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -run TestGetContents -v`
Expected: FAIL — `GetContents` undefined.

- [ ] **Step 3: Implement**

Append to `hubd/internal/github/client.go` (add only `"encoding/base64"` — `"net/url"` is ALREADY imported at client.go:13; adding it again is a compile error):

```go
// ErrTooLarge marks a contents fetch rejected by the size cap.
var ErrTooLarge = errors.New("github: file too large")

// maxContentsBytes caps plan-doc fetches (spec §5.2). GitHub's JSON contents
// API itself omits content above 1 MiB; we cap far below that.
const maxContentsBytes = 256 << 10 // 256 KiB

// validPath guards a repo-relative file path interpolated into the URL —
// same defense-in-depth role as validRef, plus traversal/absolute rejection.
func validPath(p string) error {
	if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || !refRe.MatchString(p) {
		return fmt.Errorf("github: invalid path %q", p)
	}
	return nil
}

type wireContents struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Size     int    `json:"size"`
	Content  string `json:"content"`
}

// GetContents fetches one file's bytes at ref via the JSON contents API —
// do() is JSON-only by design, and this reuses its auth/error/status handling
// (raw-accept mode would need a parallel request path). Directories decode
// into a JSON array and fail loudly rather than returning garbage.
func (c *Client) GetContents(ctx context.Context, repo, path, ref string) ([]byte, error) {
	if err := validRepo(repo); err != nil {
		return nil, err
	}
	if err := validPath(path); err != nil {
		return nil, err
	}
	if err := validRef(ref); err != nil {
		return nil, err
	}
	var w wireContents
	if _, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, path, url.QueryEscape(ref)), nil, &w, 200); err != nil {
		return nil, err
	}
	if w.Type != "" && w.Type != "file" {
		return nil, fmt.Errorf("github: %s is not a file", path)
	}
	if w.Size > maxContentsBytes || w.Encoding == "none" {
		return nil, ErrTooLarge
	}
	if w.Encoding != "base64" {
		return nil, fmt.Errorf("github: unexpected contents encoding %q", w.Encoding)
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(w.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("github: decode contents: %w", err)
	}
	if len(b) > maxContentsBytes {
		return nil, ErrTooLarge
	}
	return b, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/github/ -v`
Expected: PASS (all, including pre-existing).

- [ ] **Step 5: Full gate + commit**

```bash
git add hubd/internal/github/client.go hubd/internal/github/client_test.go
git commit -m "feat(hub): github GetContents via JSON contents API with path validation + 256KiB cap"
```

---

### Task 5: API — epic plan proxy endpoint + Deps.Contents wiring

**Files:**
- Modify: `hubd/internal/api/servers.go` (Deps field, ~line 64)
- Modify: `hubd/internal/api/orchestrator.go` (append `planDocPath` + handler)
- Modify: `hubd/internal/api/router.go` (one route)
- Modify: `hubd/cmd/agentmon-hubd/main.go` (wire the shared GitHub client, lines 110-126)
- Test: `hubd/internal/api/orchestrator_test.go` (append)

**Interfaces:**
- Consumes: Task 4's `github.GetContents`/`ErrNotFound`/`ErrTooLarge`.
- Produces: `ContentsFetcher` interface + `Deps.Contents`; `planDocPath(needs, issue)`; `OrchestratorEpicPlanHandler` → 200 `{path, ref, markdown}` / 404 / 409 / 413 / 502.

- [ ] **Step 1: Write the failing tests**

Append to `hubd/internal/api/orchestrator_test.go`:

```go
type fakeContents struct {
	body []byte
	err  error
	repo, path, ref string
}

func (f *fakeContents) GetContents(_ context.Context, repo, path, ref string) ([]byte, error) {
	f.repo, f.path, f.ref = repo, path, ref
	return f.body, f.err
}

func TestPlanDocPath(t *testing.T) {
	for needs, want := range map[string]string{
		"":                                             "docs/plans/epic-7.md",
		"plan-gate: plan ready at docs/plans/epic-7.md": "docs/plans/epic-7.md",
		"plan-gate: plan ready at docs/x/plan.md":       "docs/x/plan.md",
		"plan-gate: plan ready at ../../etc/passwd":     "docs/plans/epic-7.md",
		"plan-gate: plan ready at /abs/path.md":         "docs/plans/epic-7.md",
		"something else entirely":                       "docs/plans/epic-7.md",
	} {
		if got := planDocPath(needs, 7); got != want {
			t.Fatalf("planDocPath(%q) = %q, want %q", needs, got, want)
		}
	}
}

func TestEpicPlanHandler(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	fc := &fakeContents{body: []byte("# Plan")}
	d := Deps{DB: database, Orch: &fakeOrch{}, Contents: fc}

	// No branch yet → 409.
	r, w := orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 409 {
		t.Fatalf("branchless = %d %s", w.Code, w.Body.String())
	}

	if ok, err := database.SetEpicPR(ctx, e.ID, 0, "epic/7-x"); err != nil || !ok {
		t.Fatalf("SetEpicPR: ok=%v err=%v", ok, err)
	}

	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"markdown":"# Plan"`) {
		t.Fatalf("plan = %d %s", w.Code, w.Body.String())
	}
	if fc.repo != "o/r" || fc.path != "docs/plans/epic-7.md" || fc.ref != "epic/7-x" {
		t.Fatalf("fetch args %+v", fc)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}

	// Wrong project → 404 (cross-project guard).
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p2/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p2")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 404 {
		t.Fatalf("cross-project = %d", w.Code)
	}

	// GitHub 404 → friendly 404; too large → 413; other errors → 502.
	for _, tc := range []struct {
		err  error
		want int
	}{{github.ErrNotFound, 404}, {github.ErrTooLarge, 413}, {errors.New("boom"), 502}} {
		fc.err = tc.err
		r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
		r.SetPathValue("id", "p1")
		r.SetPathValue("epicID", e.ID)
		d.OrchestratorEpicPlanHandler()(w, r)
		if w.Code != tc.want {
			t.Fatalf("err %v = %d, want %d", tc.err, w.Code, tc.want)
		}
	}

	// Contents unset (dormant) → 503.
	d.Contents = nil
	r, w = orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 503 {
		t.Fatalf("dormant = %d", w.Code)
	}
}
```

**Fixture note:** `SetEpicPR(ctx, id, pr, branch)` (`hubd/internal/db/epics.go:179`) is the branch setter — `pr=0` leaves the epic PR-less while giving it a branch.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestPlanDocPath|TestEpicPlanHandler' -v`
Expected: FAIL — `planDocPath`/`OrchestratorEpicPlanHandler`/`Deps.Contents` undefined.

- [ ] **Step 3: Implement**

In `hubd/internal/api/servers.go`, add to the `Deps` struct after `BoardBcast`:

```go
	Contents ContentsFetcher // sub-3: plan-doc proxy; nil until github.token is set
```

Append to `hubd/internal/api/orchestrator.go` (add imports `"regexp"` and the `github` package is already imported):

```go
// ContentsFetcher is the slice of the GitHub client the plan proxy needs.
type ContentsFetcher interface {
	GetContents(ctx context.Context, repo, path, ref string) ([]byte, error)
}

var (
	planNoteRe = regexp.MustCompile(`plan ready at (\S+)`)
	planPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// planDocPath resolves the plan document path from the escalation note
// (runner-skill convention: "plan-gate: plan ready at <path>"), falling back
// to the docs/plans convention. The note is runner-controlled text, so
// sanitization failure falls back silently — never an error, never a 500.
func planDocPath(needs string, issue int) string {
	def := fmt.Sprintf("docs/plans/epic-%d.md", issue)
	m := planNoteRe.FindStringSubmatch(needs)
	if m == nil {
		return def
	}
	p := m[1]
	if len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || !planPathRe.MatchString(p) {
		return def
	}
	return p
}

// OrchestratorEpicPlanHandler proxies the epic's committed plan doc off its
// branch (spec §5.2). Hub-side because the PAT never reaches the browser; it
// can only ever read from the project's registered repo.
func (d Deps) OrchestratorEpicPlanHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		if d.Contents == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if e.Branch == "" {
			writeJSONError(w, http.StatusConflict, "epic has no branch yet — the plan is committed once the runner starts")
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		path := planDocPath(e.Needs, e.IssueNumber)
		b, err := d.Contents.GetContents(r.Context(), p.Repo, path, e.Branch)
		switch {
		case errors.Is(err, github.ErrNotFound):
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no plan doc found at %s on %s", path, e.Branch))
		case errors.Is(err, github.ErrTooLarge):
			writeJSONError(w, http.StatusRequestEntityTooLarge, "plan doc exceeds 256 KiB — open it on GitHub")
		case err != nil:
			log.Printf("api: epic plan fetch: %v", err)
			writeJSONError(w, http.StatusBadGateway, "plan fetch failed")
		default:
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]string{"path": path, "ref": e.Branch, "markdown": string(b)})
		}
	}
}
```

In `hubd/internal/api/router.go`, after the board route from Task 3:

```go
	mux.Handle("GET /api/v1/orchestrator/projects/{id}/epics/{epicID}/plan", rd.Auth.RequireAuth(rd.API.OrchestratorEpicPlanHandler()))
```

In `hubd/cmd/agentmon-hubd/main.go` (lines 110-126): hoist the GitHub client so both the orchestrator and the API share it, and wire `Contents`:

```go
	var orch *orchestrator.Orchestrator
	var boardBcast *orchestrator.BoardBroadcaster
	var gh *github.Client
	if cfg.GitHub.Token != "" {
		gh = github.NewClient(cfg.GitHub.Token)
		boardBcast = orchestrator.NewBoardBroadcaster()
		orch = orchestrator.New(orchestrator.Deps{DB: database, GH: gh, Agents: agentClient, Reg: reg, Bcast: boardBcast, Audit: rec, Cfg: cfg.Orchestrator, Now: nowRFC3339})
		go orch.Run(ctx)
		go orchestrator.RunBoardPushDispatcher(ctx, orchestrator.BoardPushDeps{Bcast: boardBcast, Presence: presence, Store: database, Send: pushSender, Now: nowRFC3339})
	}
```

and in the `if orch != nil` block that populates `apiDeps`:

```go
	if orch != nil {
		apiDeps.Orch = orch
		apiDeps.WebhookSecret = cfg.GitHub.WebhookSecret
		apiDeps.BoardBcast = boardBcast
		apiDeps.Contents = gh
	}
```

(If main.go's exact shape differs, keep its structure and make the minimal equivalent change: one shared `gh` client, `apiDeps.Contents = gh` when the token is set.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestPlanDocPath|TestEpicPlanHandler' -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

```bash
git add hubd/internal/api/servers.go hubd/internal/api/orchestrator.go hubd/internal/api/router.go hubd/cmd/agentmon-hubd/main.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(hub): epic plan-doc proxy endpoint with note-derived path sanitization"
```

---

### Task 6: Push payload epic_id + CHECKPOINT 1

**Files:**
- Modify: `hubd/internal/orchestrator/push.go:42-45`
- Test: `hubd/internal/orchestrator/push_test.go` (extend the existing payload assertion)

**Interfaces:**
- Produces: board push payload gains `"epic_id"` (spec §5.5) — consumed by Task 21's service worker.

- [ ] **Step 1: Extend the existing push test**

Open `hubd/internal/orchestrator/push_test.go`, find the test that asserts the dispatched payload (search for `"type":"epic"` or `dispatchBoardPush`), and extend its assertion to also require the substring `"epic_id":"` with the fixture's epic ID. Follow the file's existing assertion style exactly. If no payload-content assertion exists, add one alongside the existing test using its fixtures.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestBoardPush -v` (adjust `-run` to the actual test name)
Expected: FAIL on the missing `epic_id` key.

- [ ] **Step 3: Implement**

In `hubd/internal/orchestrator/push.go`, the payload at line 42 becomes:

```go
	payload, err := json.Marshal(map[string]any{
		"type": "epic", "stage": string(c.Stage), "project": c.ProjectID,
		"epic": c.Issue, "epic_id": c.EpicID, "title": c.Title, "needs": c.Needs, "ts": d.Now(),
	})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

```bash
git add hubd/internal/orchestrator/push.go hubd/internal/orchestrator/push_test.go
git commit -m "feat(hub): board push payload carries epic_id for notification deep links"
```

- [ ] **Step 6: CHECKPOINT 1 — STOP**

The hub surface for sub-3 is complete (board endpoint, plan proxy, PATCH/DELETE, push epic_id). Report tasks 1-6 status + full Go suite result. WAIT for review/fixes/"continue" before Task 7.

---

### Task 7: Web — contracts, API client, pure board logic

**Files:**
- Modify: `web/src/lib/contracts.ts` (append)
- Modify: `web/src/lib/api-client.ts` (append)
- Create: `web/src/lib/board.ts`
- Test: `web/src/lib/board.test.ts`

**Interfaces:**
- Consumes: `request<T>` core + key-builder convention (`api-client.ts`), `Provider` (`lib/provider.ts:3`).
- Produces: everything in the Web registry rows for Task 7 — exact shapes below.

- [ ] **Step 1: Append contracts**

Append to `web/src/lib/contracts.ts` (hand-mirrors of the hub DTOs — `hubd/internal/api/orchestrator.go:29-86` + spec §5.1/5.2; Go `nil` slices marshal as `null`, hence the `| null`):

```ts
// ---- Orchestrator board (sub-3) ----
export type EpicStage =
  | "queued" | "starting" | "planning" | "implementing" | "reviewing"
  | "pr_open" | "merging" | "merged" | "escalated" | "stalled" | "failed" | "canceled";

export interface ProjectDTO {
  id: string; name: string; repo: string; server_id: string; target: string;
  workdir: string; base_branch: string; provider: string;
  required_reviews: string[] | null; max_parallel: number; paused: boolean;
  require_ci: boolean; counts?: Record<string, number>;
}

export interface EpicDTO {
  id: string; project_id: string; issue: number; title: string;
  labels: string[] | null; blocked_by: number[] | null; stage: EpicStage;
  attempt: number; session: string; branch: string; pr: number;
  verdict?: string; needs: string; issue_state: string;
  queued_at: string; started_at: string; stage_updated_at: string; merged_at: string;
}

export interface EpicEventDTO { from: string; to: string; source: string; note: string; ts: string; }

export interface AllBoardResponse { orchestrator_enabled: boolean; projects: ProjectDTO[]; epics: EpicDTO[]; }
export interface ProjectBoardResponse { project: ProjectDTO; epics: EpicDTO[]; events: Record<string, EpicEventDTO[]>; }
export interface EpicPlanResponse { path: string; ref: string; markdown: string; }

// SSE `board` delta — hubd/internal/api/orchestrator_events.go:74
export interface BoardDeltaFrame {
  project_id: string; epic_id: string; issue: number; stage: EpicStage; needs: string; title: string;
}

export interface ProjectCreateRequest {
  name: string; repo: string; server_id: string; target?: string; workdir: string;
  base_branch?: string; provider?: string; required_reviews?: string[];
  max_parallel?: number; require_ci?: boolean;
}
export interface ProjectPatchRequest {
  name?: string; workdir?: string; target?: string; base_branch?: string;
  provider?: string; required_reviews?: string[];
}
export interface EpicActionRequest {
  action: string; epic_id?: string; issue?: number; value?: number; on?: boolean; text?: string;
}
```

- [ ] **Step 2: Append API client fetchers + key builders**

Append to `web/src/lib/api-client.ts` (extend the type-only import from `./contracts` with the new names):

```ts
// ---- Orchestrator board (sub-3) ----
// All board query keys share the ["board", …] prefix: one
// invalidateQueries({ queryKey: ["board"] }) refreshes every board view.
export const allBoardKey = () => ["board"] as const;
export const projectBoardKey = (projectId: string) => ["board", projectId] as const;
export const epicPlanKey = (projectId: string, epicId: string) => ["epic-plan", projectId, epicId] as const;
// A runner session lives under the project's TARGET socket. Key by target so
// same-host projects on different targets don't collide; an empty target
// reuses the home screen's sessionsKey (identical default-target list).
export const boardSessionsKey = (serverId: string, target: string) =>
  target ? (["sessions", serverId, target] as const) : sessionsKey(serverId);

export const getAllBoard = () => request<AllBoardResponse>("GET", "/orchestrator/board");
export const getProjectBoard = (projectId: string) =>
  request<ProjectBoardResponse>("GET", `/orchestrator/projects/${encodeURIComponent(projectId)}/board`);
export const getEpicPlan = (projectId: string, epicId: string) =>
  request<EpicPlanResponse>(
    "GET",
    `/orchestrator/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}/plan`,
  );
export const epicAction = (projectId: string, body: EpicActionRequest) =>
  request<{ ok: boolean }>("POST", `/orchestrator/projects/${encodeURIComponent(projectId)}/actions`, body);
export const createProject = (body: ProjectCreateRequest) =>
  request<ProjectDTO>("POST", "/orchestrator/projects", body);
export const patchProject = (projectId: string, body: ProjectPatchRequest) =>
  request<ProjectDTO>("PATCH", `/orchestrator/projects/${encodeURIComponent(projectId)}`, body);
export const deleteProject = (projectId: string) =>
  request<{ ok: boolean }>("DELETE", `/orchestrator/projects/${encodeURIComponent(projectId)}`);
```

- [ ] **Step 3: Write the failing board-logic tests**

Create `web/src/lib/board.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import type { EpicDTO, EpicStage } from "@/lib/contracts";
import {
  boardStats, cardProvider, fmtElapsed, groupByColumn, isPlanGate,
  mergeMode, parseVerdict, sessionSlug, stageMeta, STAGE_META,
} from "@/lib/board";

const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [],
  stage: "queued", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
  ...over,
});

describe("stage → column mapping", () => {
  it("maps all 13 stages to a column", () => {
    const stages: EpicStage[] = ["queued", "starting", "planning", "implementing", "reviewing",
      "pr_open", "merging", "merged", "escalated", "stalled", "failed", "canceled"];
    for (const s of stages) expect(STAGE_META[s].column).toBeTruthy();
  });
  it("survives an unknown future stage without crashing", () => {
    const m = stageMeta("deploying");
    expect(m.column).toBe("working");
    expect(m.label).toBe("deploying");
  });
});

describe("groupByColumn ordering", () => {
  it("needs-you sorts oldest wait first, queued by issue, done newest first", () => {
    const cols = groupByColumn([
      epic({ id: "a", issue: 3, stage: "escalated", stage_updated_at: "2026-07-11T10:00:00Z" }),
      epic({ id: "b", issue: 1, stage: "stalled", stage_updated_at: "2026-07-11T08:00:00Z" }),
      epic({ id: "c", issue: 9, stage: "queued" }),
      epic({ id: "d", issue: 2, stage: "queued" }),
      epic({ id: "e", issue: 4, stage: "merged", stage_updated_at: "2026-07-11T09:00:00Z" }),
      epic({ id: "f", issue: 5, stage: "canceled", stage_updated_at: "2026-07-11T11:00:00Z" }),
    ]);
    expect(cols.needs.map((e) => e.id)).toEqual(["b", "a"]);
    expect(cols.queued.map((e) => e.issue)).toEqual([2, 9]);
    expect(cols.done.map((e) => e.id)).toEqual(["f", "e"]);
  });
});

describe("boardStats", () => {
  it("counts merged only in the merged tile; failed/canceled in no tile", () => {
    const s = boardStats([
      epic({ stage: "merged" }), epic({ stage: "failed" }), epic({ stage: "canceled" }),
      epic({ stage: "implementing" }), epic({ stage: "escalated" }), epic({ stage: "pr_open" }),
      epic({ stage: "queued" }),
    ]);
    expect(s).toEqual({ merged: 1, working: 1, needs: 1, prOpen: 1, queued: 1 });
  });
});

describe("verdict + plan-gate", () => {
  it("parses the hub's capitalized verdict JSON", () => {
    const v = parseVerdict(JSON.stringify({
      Findings: { Found: 5, Resolved: 3, Unresolved: 2 },
      Unresolved: ["a", "b"], Tests: { Passed: 47, Failed: 0 }, Uncertain: true,
    }));
    expect(v).toEqual({ unresolved: ["a", "b"], found: 5, resolved: 3, unresolvedCount: 2, passed: 47, failed: 0, uncertain: true });
  });
  it("returns null on absent or malformed verdicts", () => {
    expect(parseVerdict(undefined)).toBeNull();
    expect(parseVerdict("not json")).toBeNull();
  });
  it("detects plan-gate escalations by note prefix", () => {
    expect(isPlanGate("plan-gate: plan ready at docs/plans/epic-7.md")).toBe(true);
    expect(isPlanGate("2 findings need a decision")).toBe(false);
  });
});

describe("small helpers", () => {
  it("cardProvider prefers labels over the project default", () => {
    expect(cardProvider(["agent:codex"], "claude")).toBe("codex");
    expect(cardProvider([], "claude")).toBe("claude");
    expect(cardProvider(null, "nope")).toBeUndefined();
  });
  it("mergeMode reads the pr-gate label", () => {
    expect(mergeMode(["pr-gate"])).toContain("you merge");
    expect(mergeMode([])).toContain("auto-merge");
  });
  it("sessionSlug produces valid tmux session names", () => {
    expect(sessionSlug("plan", "school platform!")).toBe("plan-school-platform-");
    expect(sessionSlug("doctor", "")).toBe("doctor-project");
    expect(sessionSlug("plan", "x".repeat(80)).length).toBeLessThanOrEqual(64);
  });
  it("fmtElapsed formats minutes/hours/days", () => {
    const t0 = Date.parse("2026-07-11T10:00:00Z");
    expect(fmtElapsed("2026-07-11T09:20:00Z", t0)).toBe("40m");
    expect(fmtElapsed("2026-07-11T07:30:00Z", t0)).toBe("2h 30m");
    expect(fmtElapsed("2026-07-09T09:00:00Z", t0)).toBe("2d 1h");
  });
});
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/lib/board.test.ts`
Expected: FAIL — module `@/lib/board` not found.

- [ ] **Step 5: Implement `web/src/lib/board.ts`**

```ts
import type { EpicDTO, EpicStage } from "@/lib/contracts";
import type { Provider } from "@/lib/provider";

export const PLAN_GATE_PREFIX = "plan-gate:";

export type BoardColumn = "working" | "needs" | "pr" | "queued" | "done";
export const COLUMN_ORDER: BoardColumn[] = ["working", "needs", "pr", "queued", "done"];

export const COLUMN_META: Record<BoardColumn, { title: string; dotClass: string }> = {
  working: { title: "Working", dotClass: "bg-amber-600" },
  needs: { title: "Needs you", dotClass: "bg-red-500" },
  pr: { title: "PR open", dotClass: "bg-blue-500" },
  queued: { title: "Queued", dotClass: "bg-gray-500" },
  done: { title: "Done", dotClass: "bg-green-600" },
};

interface StageMeta { label: string; dotClass: string; barClass: string; column: BoardColumn; }

// Spec §8 stage colors — Tailwind palette classes (house convention, STATE_META style).
export const STAGE_META: Record<EpicStage, StageMeta> = {
  queued:       { label: "queued",       dotClass: "bg-gray-500",   barClass: "bg-gray-500",   column: "queued" },
  starting:     { label: "starting",     dotClass: "bg-violet-400", barClass: "bg-violet-400", column: "working" },
  planning:     { label: "planning",     dotClass: "bg-violet-500", barClass: "bg-violet-500", column: "working" },
  implementing: { label: "implementing", dotClass: "bg-amber-600",  barClass: "bg-amber-600",  column: "working" },
  reviewing:    { label: "reviewing",    dotClass: "bg-sky-600",    barClass: "bg-sky-600",    column: "working" },
  pr_open:      { label: "PR open",      dotClass: "bg-blue-500",   barClass: "bg-blue-500",   column: "pr" },
  merging:      { label: "merging",      dotClass: "bg-blue-400",   barClass: "bg-blue-400",   column: "pr" },
  merged:       { label: "merged",       dotClass: "bg-green-600",  barClass: "bg-green-600",  column: "done" },
  escalated:    { label: "escalated",    dotClass: "bg-red-500",    barClass: "bg-red-500",    column: "needs" },
  stalled:      { label: "stalled",      dotClass: "bg-red-400",    barClass: "bg-red-400",    column: "needs" },
  failed:       { label: "failed",       dotClass: "bg-red-900",    barClass: "bg-red-900",    column: "done" },
  canceled:     { label: "canceled",     dotClass: "bg-zinc-500",   barClass: "bg-zinc-500",   column: "done" },
};

// A stage this build doesn't know (newer hub) must stay VISIBLE — an unknown
// active stage parked in "working" beats vanishing from the board.
export function stageMeta(stage: string): StageMeta {
  return STAGE_META[stage as EpicStage] ?? { label: stage, dotClass: "bg-zinc-400", barClass: "bg-zinc-400", column: "working" };
}

const ts = (s: string) => (s ? Date.parse(s) : 0);

export function groupByColumn(epics: EpicDTO[]): Record<BoardColumn, EpicDTO[]> {
  const out: Record<BoardColumn, EpicDTO[]> = { working: [], needs: [], pr: [], queued: [], done: [] };
  for (const e of epics) out[stageMeta(e.stage).column].push(e);
  out.needs.sort((a, b) => ts(a.stage_updated_at) - ts(b.stage_updated_at)); // longest-waiting first
  out.working.sort((a, b) => ts(a.started_at) - ts(b.started_at));
  out.pr.sort((a, b) => ts(a.stage_updated_at) - ts(b.stage_updated_at));
  out.queued.sort((a, b) => a.issue - b.issue);
  out.done.sort((a, b) => ts(b.stage_updated_at) - ts(a.stage_updated_at)); // newest first
  return out;
}

export interface BoardStats { merged: number; working: number; needs: number; prOpen: number; queued: number; }

// The Merged tile counts merged only — failed/canceled live in the Done
// column but in no tile (spec §6).
export function boardStats(epics: EpicDTO[]): BoardStats {
  const s: BoardStats = { merged: 0, working: 0, needs: 0, prOpen: 0, queued: 0 };
  for (const e of epics) {
    if (e.stage === "merged") { s.merged++; continue; }
    if (e.stage === "failed" || e.stage === "canceled") continue;
    const col = stageMeta(e.stage).column;
    if (col === "working") s.working++;
    else if (col === "needs") s.needs++;
    else if (col === "pr") s.prOpen++;
    else if (col === "queued") s.queued++;
  }
  return s;
}

export const isPlanGate = (needs: string): boolean => needs.startsWith(PLAN_GATE_PREFIX);

export interface VerdictSummary {
  unresolved: string[]; found: number; resolved: number; unresolvedCount: number;
  passed: number; failed: number; uncertain: boolean;
}

// The hub stores json.Marshal of the Go Verdict struct, which has yaml tags
// only — so the JSON keys are the CAPITALIZED Go field names
// (hubd/internal/orchestrator/orchestrator.go:616 + verdict.go).
export function parseVerdict(raw?: string): VerdictSummary | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as {
      Findings?: { Found?: number; Resolved?: number; Unresolved?: number };
      Unresolved?: unknown; Tests?: { Passed?: number; Failed?: number }; Uncertain?: boolean;
    };
    return {
      unresolved: Array.isArray(v.Unresolved) ? v.Unresolved.filter((s): s is string => typeof s === "string") : [],
      found: v.Findings?.Found ?? 0,
      resolved: v.Findings?.Resolved ?? 0,
      unresolvedCount: v.Findings?.Unresolved ?? 0,
      passed: v.Tests?.Passed ?? 0,
      failed: v.Tests?.Failed ?? 0,
      uncertain: v.Uncertain === true,
    };
  } catch {
    return null;
  }
}

export function cardProvider(labels: string[] | null | undefined, fallback: string): Provider | undefined {
  const ls = labels ?? [];
  if (ls.includes("agent:codex")) return "codex";
  if (ls.includes("agent:claude")) return "claude";
  return fallback === "claude" || fallback === "codex" ? fallback : undefined;
}

export function mergeMode(labels: string[] | null | undefined): string {
  return (labels ?? []).includes("pr-gate") ? "pr-gate — you merge" : "auto-merge on green";
}

// Session names must satisfy NAME_RE (lib/session-name.ts): start alnum,
// then [A-Za-z0-9_-], max 64. The prefix starts with a letter, so the result
// always begins legally.
export function sessionSlug(prefix: string, name: string): string {
  const core = name.replace(/[^A-Za-z0-9_-]+/g, "-");
  return `${prefix}-${core || "project"}`.slice(0, 64);
}

export function fmtElapsed(fromIso: string, now: number): string {
  const ms = now - Date.parse(fromIso);
  if (!Number.isFinite(ms) || ms < 0) return "";
  const m = Math.floor(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/lib/board.test.ts`
Expected: PASS. (If the `sessionSlug` expectation `"plan-school-platform-"` mismatches by the trailing dash, fix the TEST to the implementation's actual output — the invariant that matters is `NAME_RE.test(result)`; add `expect(/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(sessionSlug("plan", "school platform!"))).toBe(true)` regardless.)

- [ ] **Step 7: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/lib/contracts.ts web/src/lib/api-client.ts web/src/lib/board.ts web/src/lib/board.test.ts
git commit -m "feat(web): board contracts, api fetchers, and pure board logic (columns, verdict, stats)"
```

---

### Task 8: Web — board SSE stream, attention store, alert hook, app-wide mount

**Files:**
- Create: `web/src/lib/board-stream.ts`
- Create: `web/src/store/board.ts`
- Create: `web/src/hooks/useBoardStream.ts`
- Create: `web/src/hooks/useEpicAttentionAlerts.ts`
- Modify: `web/src/components/AuthLayout.tsx`
- Test: `web/src/hooks/useBoardStream.test.tsx`, `web/src/store/board.test.ts`

**Interfaces:**
- Consumes: `queryClient` singleton, `sonner` toast, `audioCue` (`lib/audio-cue`), Task 7 types/keys. Mirrors: `StateStream` (`lib/sse-state.ts`), `useStateStream` (`hooks/useStateStream.ts`), `useAttentionAlerts`.
- Produces: `BoardStream`, `useBoardAttention` + `useNeedsTotal()` + `needsByProject()`, `useBoardStream(deps?, onAttention?)`, `useEpicAttentionAlerts()`.

- [ ] **Step 1: Write the failing store test**

Create `web/src/store/board.test.ts`:

```ts
import { beforeEach, describe, expect, it } from "vitest";
import { needsByProject, useBoardAttention } from "@/store/board";
import type { BoardDeltaFrame, EpicDTO } from "@/lib/contracts";

const epic = (id: string, project: string, stage: string): EpicDTO => ({
  id, project_id: project, issue: 1, title: "t", labels: [], blocked_by: [],
  stage: stage as EpicDTO["stage"], attempt: 1, session: "", branch: "", pr: 0,
  needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
});
const delta = (id: string, project: string, stage: string): BoardDeltaFrame =>
  ({ project_id: project, epic_id: id, issue: 1, stage: stage as BoardDeltaFrame["stage"], needs: "", title: "t" });

describe("board attention store", () => {
  beforeEach(() => useBoardAttention.getState().reset());

  it("snapshot rebuilds the attention set", () => {
    useBoardAttention.getState().applySnapshot([
      epic("a", "p1", "escalated"), epic("b", "p1", "stalled"), epic("c", "p2", "implementing"),
    ]);
    const at = useBoardAttention.getState().attention;
    expect(at.size).toBe(2);
    expect(needsByProject(at).get("p1")).toBe(2);
  });

  it("deltas add and clear attention", () => {
    useBoardAttention.getState().applyDelta(delta("a", "p1", "escalated"));
    expect(useBoardAttention.getState().attention.size).toBe(1);
    useBoardAttention.getState().applyDelta(delta("a", "p1", "implementing"));
    expect(useBoardAttention.getState().attention.size).toBe(0);
  });
});
```

- [ ] **Step 2: Write the failing stream-hook test**

Create `web/src/hooks/useBoardStream.test.tsx` (mirrors the `FakeES` harness in `useStateStream.test.tsx:9-22` — open that file and copy its FakeES class if this sketch drifts from it):

```tsx
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ invalidateQueries: vi.fn() }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));

import { useBoardStream } from "@/hooks/useBoardStream";
import { useBoardAttention } from "@/store/board";

class FakeES {
  static instances: FakeES[] = [];
  listeners = new Map<string, (ev: MessageEvent) => void>();
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string, public opts?: EventSourceInit) { FakeES.instances.push(this); }
  addEventListener(type: string, fn: (ev: MessageEvent) => void) { this.listeners.set(type, fn); }
  close() {}
  emit(type: string, data: string) { this.listeners.get(type)?.({ data } as MessageEvent); }
}

function Harness({ onAttention }: { onAttention?: (f: unknown) => void }) {
  useBoardStream({ EventSourceCtor: FakeES as unknown as typeof EventSource }, onAttention);
  return null;
}

describe("useBoardStream", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeES.instances = [];
    h.invalidateQueries.mockReset();
    useBoardAttention.getState().reset();
  });
  afterEach(() => vi.useRealTimers());

  it("seeds the store from board-snapshot and invalidates once", () => {
    render(<Harness />);
    const es = FakeES.instances[0];
    act(() => es.emit("board-snapshot", JSON.stringify({
      projects: [], epics: [{ id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [], stage: "escalated", attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" }],
    })));
    expect(useBoardAttention.getState().attention.size).toBe(1);
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["board"] });
  });

  it("debounces delta invalidations and fires attention callback", () => {
    const onAttention = vi.fn();
    render(<Harness onAttention={onAttention} />);
    const es = FakeES.instances[0];
    const d = (stage: string) => JSON.stringify({ project_id: "p1", epic_id: "e1", issue: 1, stage, needs: "", title: "t" });
    act(() => {
      es.emit("board", d("implementing"));
      es.emit("board", d("reviewing"));
      es.emit("board", d("escalated"));
    });
    expect(h.invalidateQueries).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(350); });
    expect(h.invalidateQueries).toHaveBeenCalledTimes(1);
    expect(onAttention).toHaveBeenCalledTimes(1);
    expect(onAttention.mock.calls[0][0]).toMatchObject({ stage: "escalated", epic_id: "e1" });
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/store/board.test.ts src/hooks/useBoardStream.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 4: Implement the four modules + mount**

`web/src/lib/board-stream.ts` (mirror `sse-state.ts` exactly — native EventSource reconnect, each reconnect replays the snapshot):

```ts
import type { BoardDeltaFrame, EpicDTO, ProjectDTO } from "@/lib/contracts";

const BOARD_EVENTS_URL = "/api/v1/orchestrator/events";

export interface BoardSnapshotFrame { projects: ProjectDTO[]; epics: EpicDTO[]; }

export interface BoardStreamHandlers {
  onSnapshot(s: BoardSnapshotFrame): void;
  onDelta(f: BoardDeltaFrame): void;
  onOpen?(): void;
  onError?(): void; // EventSource self-reconnects; connection indicator only
}

export interface BoardStreamDeps { EventSourceCtor?: typeof EventSource; url?: string; }

export class BoardStream {
  private es: EventSource | null = null;
  private disposed = false;
  private readonly ES: typeof EventSource | undefined;
  private readonly url: string;

  constructor(private handlers: BoardStreamHandlers, deps?: BoardStreamDeps) {
    this.ES = deps?.EventSourceCtor ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
    this.url = deps?.url ?? BOARD_EVENTS_URL;
  }

  open(): void {
    if (this.disposed || this.es || !this.ES) return;
    const es = new this.ES(this.url, { withCredentials: true });
    this.es = es;
    es.addEventListener("board-snapshot", (ev: MessageEvent) => {
      try { this.handlers.onSnapshot(JSON.parse(ev.data as string) as BoardSnapshotFrame); } catch { /* malformed frame — wait for the next */ }
    });
    es.addEventListener("board", (ev: MessageEvent) => {
      try { this.handlers.onDelta(JSON.parse(ev.data as string) as BoardDeltaFrame); } catch { /* ditto */ }
    });
    es.onopen = () => this.handlers.onOpen?.();
    es.onerror = () => this.handlers.onError?.();
  }

  dispose(): void {
    this.disposed = true;
    if (this.es) { this.es.close(); this.es = null; }
  }
}
```

`web/src/store/board.ts`:

```ts
import { create } from "zustand";
import type { BoardDeltaFrame, EpicDTO } from "@/lib/contracts";

const needsAttention = (stage: string) => stage === "escalated" || stage === "stalled";

interface BoardAttentionStore {
  attention: Map<string, string>; // epicId → projectId, for escalated/stalled epics
  connected: boolean;
  applySnapshot(epics: EpicDTO[]): void;
  applyDelta(f: BoardDeltaFrame): void;
  setConnected(v: boolean): void;
  reset(): void;
}

export const useBoardAttention = create<BoardAttentionStore>((set) => ({
  attention: new Map(),
  connected: false,
  applySnapshot(epics) {
    const m = new Map<string, string>();
    for (const e of epics) if (needsAttention(e.stage)) m.set(e.id, e.project_id);
    set({ attention: m, connected: true });
  },
  applyDelta(f) {
    set((s) => {
      const has = s.attention.has(f.epic_id);
      if (needsAttention(f.stage) === has) return s;
      const m = new Map(s.attention);
      if (needsAttention(f.stage)) m.set(f.epic_id, f.project_id);
      else m.delete(f.epic_id);
      return { attention: m };
    });
  },
  setConnected(connected) { set({ connected }); },
  reset() { set({ attention: new Map(), connected: false }); },
}));

export const useNeedsTotal = (): number => useBoardAttention((s) => s.attention.size);

export function needsByProject(attention: Map<string, string>): Map<string, number> {
  const out = new Map<string, number>();
  for (const pid of attention.values()) out.set(pid, (out.get(pid) ?? 0) + 1);
  return out;
}
```

`web/src/hooks/useBoardStream.ts`:

```ts
import * as React from "react";
import { BoardStream, type BoardStreamDeps } from "@/lib/board-stream";
import type { BoardDeltaFrame } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";
import { useBoardAttention } from "@/store/board";

const INVALIDATE_DEBOUNCE_MS = 300;

// One app-wide subscription (mounted in AuthLayout, like useStateStream).
// Approach A (spec D7): the stream never patches board state — the snapshot
// seeds the attention store, and deltas just invalidate the ["board"] queries.
export function useBoardStream(deps?: BoardStreamDeps, onAttention?: (f: BoardDeltaFrame) => void): void {
  const onAttentionRef = React.useRef(onAttention);
  onAttentionRef.current = onAttention;

  React.useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const invalidateSoon = () => {
      if (timer) return;
      timer = setTimeout(() => {
        timer = null;
        void queryClient.invalidateQueries({ queryKey: ["board"] });
      }, INVALIDATE_DEBOUNCE_MS);
    };
    const stream = new BoardStream(
      {
        onSnapshot: (s) => {
          useBoardAttention.getState().applySnapshot(s.epics);
          // Reconnect catch-up: deltas may have been missed while down.
          void queryClient.invalidateQueries({ queryKey: ["board"] });
        },
        onDelta: (f) => {
          useBoardAttention.getState().applyDelta(f);
          invalidateSoon();
          if (f.stage === "escalated" || f.stage === "stalled") onAttentionRef.current?.(f);
        },
        onOpen: () => useBoardAttention.getState().setConnected(true),
        onError: () => useBoardAttention.getState().setConnected(false),
      },
      deps,
    );
    stream.open();
    // Visibility-resume (parity with ws-terminal.ts:72): a backgrounded PWA may
    // have missed deltas even if the EventSource never fully dropped. On resume,
    // refetch the board so the UI can't sit on stale state. (When the browser
    // DID drop the connection, native reconnect also replays the snapshot.)
    const onVisible = () => {
      if (document.visibilityState === "visible") {
        void queryClient.invalidateQueries({ queryKey: ["board"] });
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisible);
      stream.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
```

`web/src/hooks/useEpicAttentionAlerts.ts` (mirrors `useAttentionAlerts.ts` — every Web-API touch guarded so it never throws into the stream pump):

```ts
import * as React from "react";
import { toast } from "sonner";
import { useNavigate } from "@tanstack/react-router";
import { audioCue } from "@/lib/audio-cue";
import type { BoardDeltaFrame } from "@/lib/contracts";

export function useEpicAttentionAlerts(): (f: BoardDeltaFrame) => void {
  const navigate = useNavigate();
  return React.useCallback(
    (f) => {
      const title = `Epic #${f.issue} needs you`;
      try {
        toast(title, {
          description: f.needs || f.title,
          action: {
            label: "View",
            onClick: () =>
              void navigate({
                to: "/projects/$projectId",
                params: { projectId: f.project_id },
                search: { tab: "board", epic: f.epic_id },
              }),
          },
        });
      } catch { /* toast failure must not break sound/notification */ }
      audioCue.play();
      try { navigator.vibrate?.([120, 60, 120]); } catch { /* unsupported */ }
      try {
        if (document.visibilityState === "hidden" && "Notification" in window && Notification.permission === "granted") {
          new Notification(title, { body: f.title, tag: `epic:${f.epic_id}` });
        }
      } catch { /* unsupported */ }
    },
    [navigate],
  );
}
```

(Check `lib/audio-cue.ts`'s actual export name — `useAttentionAlerts.ts` calls `audioCue.play()`; match it.)

`web/src/components/AuthLayout.tsx` becomes:

```tsx
import { Outlet } from "@tanstack/react-router";
import { useStateStream } from "@/hooks/useStateStream";
import { useAttentionAlerts } from "@/hooks/useAttentionAlerts";
import { useBoardStream } from "@/hooks/useBoardStream";
import { useEpicAttentionAlerts } from "@/hooks/useEpicAttentionAlerts";

export function AuthLayout() {
  const onAttention = useAttentionAlerts();
  useStateStream(undefined, onAttention);
  const onEpicAttention = useEpicAttentionAlerts();
  useBoardStream(undefined, onEpicAttention);
  return <Outlet />;
}
```

(Keep the file's existing import style/order; add only the two new hook calls.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/store/board.test.ts src/hooks/useBoardStream.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/lib/board-stream.ts web/src/store/board.ts web/src/hooks/useBoardStream.ts web/src/hooks/useEpicAttentionAlerts.ts web/src/components/AuthLayout.tsx web/src/store/board.test.ts web/src/hooks/useBoardStream.test.tsx
git commit -m "feat(web): board SSE stream with debounced query invalidation, attention store, escalation alerts"
```

---

### Task 9: Web — ConfirmButton, useEpicActions, StageChip

**Files:**
- Create: `web/src/components/board/ConfirmButton.tsx`
- Create: `web/src/hooks/useEpicActions.ts`
- Create: `web/src/components/board/StageChip.tsx`
- Test: `web/src/components/board/ConfirmButton.test.tsx`, `web/src/hooks/useEpicActions.test.tsx`

**Interfaces:**
- Consumes: `Button` (`components/ui/button`), `epicAction` + `ApiError` (Task 7), `queryClient`, `toast`, `stageMeta` (Task 7), `cn` (`lib/utils`).
- Produces: `ConfirmButton {label, confirmLabel?, variant?, size?, disabled?, className?, onConfirm}`; `useEpicActions(projectId) → {act, busy}`; `StageChip {stage, className?}`.

- [ ] **Step 1: Write the failing tests**

`web/src/components/board/ConfirmButton.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmButton } from "@/components/board/ConfirmButton";

describe("ConfirmButton", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("requires two clicks and disarms after 3s", () => {
    const onConfirm = vi.fn();
    render(<ConfirmButton label="Approve & merge" confirmLabel="Merge?" onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Merge?" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    // Disarm path
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    vi.advanceTimersByTime(3100);
    expect(screen.getByRole("button", { name: "Approve & merge" })).toBeInTheDocument();
  });
});
```

`web/src/hooks/useEpicActions.test.tsx`:

```tsx
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  epicAction: vi.fn(),
  invalidateQueries: vi.fn(),
  toast: Object.assign(vi.fn(), { error: vi.fn() }),
}));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: h.toast }));

import { useEpicActions } from "@/hooks/useEpicActions";
import { ApiError } from "@/lib/api-client";

describe("useEpicActions", () => {
  beforeEach(() => { h.epicAction.mockReset(); h.invalidateQueries.mockReset(); h.toast.mockClear(); h.toast.error.mockReset(); });

  it("posts, toasts success, and invalidates board queries", async () => {
    h.epicAction.mockResolvedValue({ ok: true });
    const { result } = renderHook(() => useEpicActions("p1"));
    let ok = false;
    await act(async () => { ok = await result.current.act({ action: "approve", epic_id: "e1" }, "Approving #7"); });
    expect(ok).toBe(true);
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "approve", epic_id: "e1" });
    expect(h.toast).toHaveBeenCalledWith("Approving #7");
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["board"] });
  });

  it("surfaces typed 409 user errors verbatim", async () => {
    h.epicAction.mockRejectedValue(new ApiError(409, "epic is not escalated"));
    const { result } = renderHook(() => useEpicActions("p1"));
    let ok = true;
    await act(async () => { ok = await result.current.act({ action: "approve", epic_id: "e1" }); });
    expect(ok).toBe(false);
    expect(h.toast.error).toHaveBeenCalledWith("epic is not escalated");
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/ConfirmButton.test.tsx src/hooks/useEpicActions.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

`web/src/components/board/ConfirmButton.tsx`:

```tsx
import * as React from "react";
import { Button, type ButtonProps } from "@/components/ui/button";

// Two-step inline confirm: first click arms for 3s ("Merge?"), second fires.
// Deliberate friction before merging/pausing from a phone (spec §6) without a
// modal round-trip. stopPropagation so cards don't open their drawer.
export function ConfirmButton({
  label, confirmLabel = "Confirm?", onConfirm, variant = "outline", size = "sm", disabled, className,
}: {
  label: string; confirmLabel?: string; onConfirm(): void;
  variant?: ButtonProps["variant"]; size?: ButtonProps["size"]; disabled?: boolean; className?: string;
}) {
  const [armed, setArmed] = React.useState(false);
  React.useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <Button
      variant={armed ? "destructive" : variant}
      size={size}
      disabled={disabled}
      className={className}
      onClick={(e) => {
        e.stopPropagation();
        if (armed) { setArmed(false); onConfirm(); } else { setArmed(true); }
      }}
    >
      {armed ? confirmLabel : label}
    </Button>
  );
}
```

(If `ButtonProps` is not exported from `ui/button.tsx`, type `variant`/`size` as the literal unions from that file instead — do not modify the primitive.)

`web/src/hooks/useEpicActions.ts`:

```ts
import * as React from "react";
import { toast } from "sonner";
import { ApiError, epicAction } from "@/lib/api-client";
import type { EpicActionRequest } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";

// One mutation helper for every board action (epic- and project-scoped —
// same hub endpoint). Success invalidates ["board"] immediately rather than
// waiting on the SSE delta; typed 409s carry human-readable hub messages.
export function useEpicActions(projectId: string) {
  const [busy, setBusy] = React.useState<string | null>(null);
  const act = React.useCallback(
    async (body: EpicActionRequest, success?: string): Promise<boolean> => {
      setBusy(body.action);
      try {
        await epicAction(projectId, body);
        if (success) toast(success);
        void queryClient.invalidateQueries({ queryKey: ["board"] });
        return true;
      } catch (err) {
        toast.error(err instanceof ApiError ? err.message : "Action failed — check hub logs");
        return false;
      } finally {
        setBusy(null);
      }
    },
    [projectId],
  );
  return { act, busy };
}
```

`web/src/components/board/StageChip.tsx`:

```tsx
import { cn } from "@/lib/utils";
import { stageMeta } from "@/lib/board";

export function StageChip({ stage, className }: { stage: string; className?: string }) {
  const meta = stageMeta(stage);
  return (
    <span className={cn("inline-flex flex-none items-center gap-1.5 rounded-full border border-border bg-card px-2 py-0.5 text-[11px] font-medium", className)}>
      <span className={cn("inline-block size-1.5 rounded-full", meta.dotClass)} />
      {meta.label}
    </span>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/ConfirmButton.test.tsx src/hooks/useEpicActions.test.tsx`
Expected: PASS.

- [ ] **Step 5: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/ConfirmButton.tsx web/src/hooks/useEpicActions.ts web/src/components/board/StageChip.tsx web/src/components/board/ConfirmButton.test.tsx web/src/hooks/useEpicActions.test.tsx
git commit -m "feat(web): ConfirmButton, useEpicActions mutation helper, StageChip"
```

---

### Task 10: Web — EpicCard

**Files:**
- Create: `web/src/components/board/EpicCard.tsx`
- Test: `web/src/components/board/EpicCard.test.tsx`

**Interfaces:**
- Consumes: Task 7 (`stageMeta`, `parseVerdict`, `isPlanGate`, `cardProvider`, `mergeMode`, `fmtElapsed`), Task 9 (`ConfirmButton`, `useEpicActions`), `ProviderTag`, `Button`, `cn`, `SessionState` type.
- Produces: `EpicCard {epic: EpicDTO; project?: ProjectDTO; showProject?: boolean; liveState?: SessionState; onOpen(): void}`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/board/EpicCard.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ epicAction: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { EpicCard } from "@/components/board/EpicCard";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true,
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 15, title: "GDPR consent", labels: [], blocked_by: [],
  stage: "queued", attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open",
  queued_at: "", started_at: "", stage_updated_at: "", merged_at: "", ...over,
});

describe("EpicCard", () => {
  beforeEach(() => { h.epicAction.mockReset(); h.epicAction.mockResolvedValue({ ok: true }); });

  it("opens the drawer on click", () => {
    const onOpen = vi.fn();
    render(<EpicCard epic={epic({})} project={project} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: /#15/ }));
    expect(onOpen).toHaveBeenCalled();
  });

  it("escalated card shows needs + verdict facts and confirms approve without opening", () => {
    const onOpen = vi.fn();
    render(
      <EpicCard
        epic={epic({
          stage: "escalated", pr: 58, needs: "2 findings need a decision",
          verdict: JSON.stringify({ Findings: { Unresolved: 2 }, Tests: { Passed: 47, Failed: 0 } }),
        })}
        project={project}
        onOpen={onOpen}
      />,
    );
    expect(screen.getByText("2 findings need a decision")).toBeInTheDocument();
    expect(screen.getByText(/2 unresolved/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    fireEvent.click(screen.getByRole("button", { name: "Merge?" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "approve", epic_id: "e1" });
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("plan-gate card swaps the primary action to Review plan (opens drawer)", () => {
    const onOpen = vi.fn();
    render(
      <EpicCard epic={epic({ stage: "escalated", needs: "plan-gate: plan ready at docs/plans/epic-15.md" })} project={project} onOpen={onOpen} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Review plan" }));
    expect(onOpen).toHaveBeenCalled();
    expect(h.epicAction).not.toHaveBeenCalled();
  });

  it("queued card lists blockers; working card shows session + live state", () => {
    render(<EpicCard epic={epic({ stage: "queued", blocked_by: [13, 14] })} project={project} onOpen={() => {}} />);
    expect(screen.getByText("#13")).toBeInTheDocument();
    render(
      <EpicCard
        epic={epic({ id: "e2", stage: "implementing", session: "epic-15-x", started_at: "2026-07-11T08:00:00Z" })}
        project={project}
        liveState="blocked"
        onOpen={() => {}}
      />,
    );
    expect(screen.getByText("epic-15-x")).toBeInTheDocument();
    expect(screen.getByText(/blocked/)).toBeInTheDocument();
  });

  it("shows the project chip only in All view", () => {
    render(<EpicCard epic={epic({ id: "e3" })} project={project} showProject onOpen={() => {}} />);
    expect(screen.getByText("school")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/EpicCard.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/EpicCard.tsx`**

```tsx
import * as React from "react";
import { Button } from "@/components/ui/button";
import { ProviderTag } from "@/components/ProviderTag";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { useEpicActions } from "@/hooks/useEpicActions";
import {
  cardProvider, fmtElapsed, isPlanGate, mergeMode, parseVerdict, stageMeta,
} from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import type { SessionState } from "@/lib/contracts";
import { cn } from "@/lib/utils";

export function EpicCard({ epic, project, showProject = false, liveState, onOpen }: {
  epic: EpicDTO; project?: ProjectDTO; showProject?: boolean;
  liveState?: SessionState; onOpen(): void;
}) {
  const meta = stageMeta(epic.stage);
  const col = meta.column;
  const { act, busy } = useEpicActions(epic.project_id);
  const verdict = parseVerdict(epic.verdict);
  const planGate = col === "needs" && isPlanGate(epic.needs);
  const provider = cardProvider(epic.labels, project?.provider ?? "");
  const prUrl = project && epic.pr > 0 ? `https://github.com/${project.repo}/pull/${epic.pr}` : "";
  const compact = col === "done";

  const verdictFacts = verdict && (
    <div className="text-xs text-muted-foreground">
      {verdict.unresolvedCount} unresolved · tests {verdict.passed}✓{verdict.failed > 0 ? ` ${verdict.failed}✗` : ""}
    </div>
  );

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}
      className={cn(
        "flex cursor-pointer flex-col gap-2 rounded-lg border border-border bg-card p-3 text-left hover:border-muted-foreground/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        col === "needs" && "border-red-500/50",
        compact && "gap-1 py-2",
      )}
    >
      <div className="flex items-center gap-1.5 text-xs">
        <span className={cn("inline-block size-2 flex-none rounded-full", meta.dotClass, liveState === "working" && "animate-pulse")} />
        <span className="font-semibold">{meta.label}</span>
        {liveState === "blocked" && <span className="text-red-400">· blocked</span>}
        <span className="ml-auto flex items-center gap-1.5">
          {showProject && project && (
            <span className="rounded border border-border px-1 text-[10px] text-muted-foreground">{project.name}</span>
          )}
          <ProviderTag provider={provider} />
        </span>
      </div>

      <div className={cn("text-sm font-medium leading-snug", compact && "truncate")}>
        <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
      </div>

      {!compact && project && (
        <div className="truncate font-mono text-[11px] text-muted-foreground">
          {project.repo}
          {epic.branch ? ` · ${epic.branch}` : ""}
        </div>
      )}

      {col === "working" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          {epic.session && <div className="truncate font-mono">{epic.session}</div>}
          {epic.started_at && <div>{fmtElapsed(epic.started_at, Date.now())} elapsed{epic.pr > 0 ? "" : " · no PR yet"}</div>}
        </div>
      )}

      {col === "needs" && (
        <div className="border-t border-border pt-2">
          <div className="text-[10px] font-bold uppercase tracking-wider text-red-400">Needs attention</div>
          <div className="mt-1 text-xs">{epic.needs || meta.label}</div>
          {verdictFacts && <div className="mt-1">{verdictFacts}</div>}
          {epic.pr > 0 && <div className="mt-1 text-xs text-muted-foreground">PR #{epic.pr}</div>}
          {/* Actions must never bubble into the drawer-open click. */}
          <div className="mt-2 flex flex-wrap gap-1.5" onClick={(e) => e.stopPropagation()}>
            {planGate ? (
              <Button size="sm" onClick={onOpen}>Review plan</Button>
            ) : (
              <ConfirmButton
                label={epic.pr > 0 ? "Approve & merge" : "Approve"}
                confirmLabel={epic.pr > 0 ? "Merge?" : "Approve?"}
                variant="default"
                disabled={busy !== null}
                onConfirm={() => void act({ action: "approve", epic_id: epic.id }, `Approving #${epic.issue}`)}
              />
            )}
            <ConfirmButton
              label="Retry"
              confirmLabel="Retry?"
              disabled={busy !== null}
              onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Retrying #${epic.issue}`)}
            />
            {prUrl && (
              <Button variant="outline" size="sm" asChild>
                <a href={prUrl} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>PR ↗</a>
              </Button>
            )}
          </div>
        </div>
      )}

      {col === "pr" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          <div>PR <span className="text-foreground">#{epic.pr}</span> · {mergeMode(epic.labels)}</div>
          {verdictFacts}
        </div>
      )}

      {col === "queued" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          {(epic.blocked_by ?? []).length > 0 ? (
            <div className="flex flex-wrap items-center gap-1">
              blocked by
              {(epic.blocked_by ?? []).map((n) => (
                <span key={n} className="rounded border border-border px-1 font-mono text-[10px]">#{n}</span>
              ))}
            </div>
          ) : (
            <div>ready — waiting for a slot</div>
          )}
          {project?.paused && <div className="mt-1 text-amber-500">held — project paused</div>}
        </div>
      )}

      {compact && (
        <div className="text-xs text-muted-foreground">
          {epic.stage === "merged"
            ? <>✓ {epic.started_at && epic.merged_at ? fmtElapsed(epic.started_at, Date.parse(epic.merged_at)) : "merged"}{epic.pr > 0 ? <> · PR #{epic.pr}</> : null}</>
            : <span className="italic">{meta.label}</span>}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/EpicCard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/EpicCard.tsx web/src/components/board/EpicCard.test.tsx
git commit -m "feat(web): EpicCard with column-specific bodies and inline confirm actions"
```

---

### Task 11: Web — BoardView (kanban + mobile stack + stats) and the layout pref

**Files:**
- Modify: `web/src/store/prefs.ts` (new persisted field)
- Create: `web/src/components/board/BoardView.tsx`
- Test: `web/src/components/board/BoardView.test.tsx`

**Interfaces:**
- Consumes: Task 7 (`groupByColumn`, `boardStats`, `COLUMN_ORDER`, `COLUMN_META`), Task 10 (`EpicCard`), `useMediaQuery` (`lib/use-media-query`), `usePrefs`.
- Produces: `BoardView {epics, projects: Map<string, ProjectDTO>, showProject: boolean, liveStateOf(e: EpicDTO): SessionState | undefined, onOpenEpic(id: string): void}`; pref `projectsBoardLayout: "stack" | "columns"` + `setProjectsBoardLayout`.

- [ ] **Step 1: Add the pref**

In `web/src/store/prefs.ts`: add to `PrefsState` —

```ts
  projectsBoardLayout: "stack" | "columns";
  setProjectsBoardLayout(v: "stack" | "columns"): void;
```

default `projectsBoardLayout: "stack"`, setter `setProjectsBoardLayout: (v) => set({ projectsBoardLayout: v })`, and add `projectsBoardLayout: s.projectsBoardLayout` to `partialize`. (No `version`/`migrate` exists in this store; a missing key simply falls back to the default — fine.)

- [ ] **Step 2: Write the failing tests**

Create `web/src/components/board/BoardView.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ desktop: true }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => h.desktop }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), epicAction: vi.fn() }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { BoardView } from "@/components/board/BoardView";
import { usePrefs } from "@/store/prefs";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true,
};
const epic = (id: string, issue: number, stage: string): EpicDTO => ({
  id, project_id: "p1", issue, title: `t${issue}`, labels: [], blocked_by: [],
  stage: stage as EpicDTO["stage"], attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "", stage_updated_at: `2026-07-11T0${issue}:00:00Z`, merged_at: "",
});
const projects = new Map([["p1", project]]);
const base = { projects, showProject: false, liveStateOf: () => undefined, onOpenEpic: () => {} };

describe("BoardView", () => {
  beforeEach(() => { h.desktop = true; usePrefs.setState({ projectsBoardLayout: "stack" }); });

  it("desktop renders all five columns with counts and the stat strip", () => {
    render(<BoardView {...base} epics={[epic("a", 1, "implementing"), epic("b", 2, "escalated"), epic("c", 3, "merged")]} />);
    for (const title of ["Working", "Needs you", "PR open", "Queued", "Done"]) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }
    expect(screen.getByText("Merged")).toBeInTheDocument(); // stat tile
  });

  it("mobile stacked hides empty sections, collapses Done, and can toggle to columns", () => {
    h.desktop = false;
    render(<BoardView {...base} epics={[epic("a", 1, "escalated"), epic("b", 2, "merged")]} />);
    expect(screen.getByText("Needs you")).toBeInTheDocument();
    expect(screen.queryByText("PR open")).not.toBeInTheDocument(); // empty section hidden
    // Done starts collapsed: the merged card is not visible until expanded.
    expect(screen.queryByText(/t2/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByText(/Done/));
    expect(screen.getByText(/t2/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Columns" }));
    expect(usePrefs.getState().projectsBoardLayout).toBe("columns");
  });

  it("Done column truncates to 10 with a show-all expander", () => {
    const many = Array.from({ length: 14 }, (_, i) => epic(`m${i}`, i + 1, "merged"));
    render(<BoardView {...base} epics={many} />);
    fireEvent.click(screen.getByRole("button", { name: /Show all \(14\)/ }));
    expect(screen.getByText(/t14/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/BoardView.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement `web/src/components/board/BoardView.tsx`**

```tsx
import * as React from "react";
import { Button } from "@/components/ui/button";
import { EpicCard } from "@/components/board/EpicCard";
import {
  boardStats, COLUMN_META, COLUMN_ORDER, groupByColumn, type BoardColumn,
} from "@/lib/board";
import type { EpicDTO, ProjectDTO, SessionState } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { usePrefs } from "@/store/prefs";
import { cn } from "@/lib/utils";

const DONE_VISIBLE = 10;

export function BoardView({ epics, projects, showProject, liveStateOf, onOpenEpic }: {
  epics: EpicDTO[]; projects: Map<string, ProjectDTO>; showProject: boolean;
  liveStateOf(e: EpicDTO): SessionState | undefined; onOpenEpic(id: string): void;
}) {
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const layout = usePrefs((s) => s.projectsBoardLayout);
  const setLayout = usePrefs((s) => s.setProjectsBoardLayout);
  const [allDone, setAllDone] = React.useState(false);
  const cols = React.useMemo(() => groupByColumn(epics), [epics]);
  const stats = React.useMemo(() => boardStats(epics), [epics]);
  const stacked = !isDesktop && layout === "stack";

  const cards = (col: BoardColumn) => {
    const list = col === "done" && !allDone ? cols.done.slice(0, DONE_VISIBLE) : cols[col];
    return (
      <>
        {list.map((e) => (
          <EpicCard key={e.id} epic={e} project={projects.get(e.project_id)} showProject={showProject}
            liveState={liveStateOf(e)} onOpen={() => onOpenEpic(e.id)} />
        ))}
        {col === "done" && !allDone && cols.done.length > DONE_VISIBLE && (
          <Button variant="ghost" size="sm" onClick={() => setAllDone(true)}>
            Show all ({cols.done.length})
          </Button>
        )}
      </>
    );
  };

  const header = (col: BoardColumn) => (
    <div className="flex items-center gap-2 px-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
      <span className={cn("inline-block size-2 rounded-full", COLUMN_META[col].dotClass)} />
      {COLUMN_META[col].title}
      <span className="ml-auto font-semibold">{cols[col].length}</span>
    </div>
  );

  return (
    <div className="flex flex-col gap-3">
      <BoardStatsStrip stats={stats} />
      {!isDesktop && (
        <div className="flex gap-1 self-end">
          <Button variant={layout === "stack" ? "default" : "outline"} size="sm" onClick={() => setLayout("stack")}>List</Button>
          <Button variant={layout === "columns" ? "default" : "outline"} size="sm" onClick={() => setLayout("columns")}>Columns</Button>
        </div>
      )}
      {stacked ? (
        <div className="flex flex-col gap-4">
          {COLUMN_ORDER.filter((c) => cols[c].length > 0).map((col) =>
            col === "done" ? (
              <details key={col} className="flex flex-col gap-2">
                <summary className="cursor-pointer list-none">{header(col)}</summary>
                <div className="mt-2 flex flex-col gap-2">{cards(col)}</div>
              </details>
            ) : (
              <section key={col} className="flex flex-col gap-2">
                {header(col)}
                {cards(col)}
              </section>
            ),
          )}
        </div>
      ) : (
        <div className="overflow-x-auto pb-2">
          <div className="grid min-w-[1100px] grid-cols-5 items-start gap-3">
            {COLUMN_ORDER.map((col) => (
              <div key={col} className={cn("flex flex-col gap-2 rounded-xl border border-border bg-background/40 p-2", col === "needs" && cols.needs.length > 0 && "border-red-500/40")}>
                {header(col)}
                {cards(col)}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

export function BoardStatsStrip({ stats }: { stats: ReturnType<typeof boardStats> }) {
  const tile = (label: string, value: number, attn = false) => (
    <div className={cn("rounded-lg border border-border bg-card px-3 py-2", attn && value > 0 && "border-red-500/50")}>
      <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className={cn("text-lg font-semibold tabular-nums", attn && value > 0 && "text-red-400")}>{value}</div>
    </div>
  );
  return (
    <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
      {tile("Merged", stats.merged)}
      {tile("Working", stats.working)}
      {tile("Needs you", stats.needs, true)}
      {tile("PRs open", stats.prOpen)}
      {tile("Queued", stats.queued)}
    </div>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/BoardView.test.tsx`
Expected: PASS. (jsdom quirk: if the `<details>` collapse assertion fails because jsdom renders children regardless, switch the Done section to a state-driven `<section>` with a chevron button — keep the same visible text so the test stands.)

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/store/prefs.ts web/src/components/board/BoardView.tsx web/src/components/board/BoardView.test.tsx
git commit -m "feat(web): BoardView kanban + attention-first mobile stack with persisted layout pref"
```

---

### Task 12: Web — routes, header entry, switcher, page states + CHECKPOINT 2

**Files:**
- Create: `web/src/routes/projects.tsx`
- Create: `web/src/components/board/ProjectSwitcher.tsx`
- Modify: `web/src/router.tsx`
- Modify: `web/src/routes/index.tsx` (Projects button in the header cluster, lines ~154-171)
- Test: `web/src/components/board/ProjectSwitcher.test.tsx`

**Interfaces:**
- Consumes: Tasks 7-11; `useStateSnapshot` + `effectiveSessionState` (`store/session-state.ts`, `lib/state.ts`), `useNeedsTotal`/`needsByProject` (Task 8).
- Produces: routes `/projects` + `/projects/$projectId` with `ProjectsSearch {tab, epic}`; `ProjectsIndexRoute`, `ProjectDetailRoute`, `ProjectSwitcher {projects, needs: Map<string,number>, current?: string, onSelect(id: string | null): void}`.

**Testing note:** the route components use `useNavigate`/`useSearch` and are exercised by the toy-stack acceptance pass, not unit tests (no router harness exists in this repo — house convention). Unit-test the pure pieces (`ProjectSwitcher`).

- [ ] **Step 1: Write the failing switcher test**

Create `web/src/components/board/ProjectSwitcher.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProjectSwitcher } from "@/components/board/ProjectSwitcher";
import type { ProjectDTO } from "@/lib/contracts";

const p = (id: string, name: string): ProjectDTO => ({
  id, name, repo: "o/r", server_id: "h1", target: "", workdir: "/w", base_branch: "main",
  provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: false,
});

describe("ProjectSwitcher", () => {
  it("lists All + projects with needs counts and fires onSelect", () => {
    const onSelect = vi.fn();
    render(
      <ProjectSwitcher projects={[p("p1", "school"), p("p2", "dnsmon")]}
        needs={new Map([["p1", 2]])} current="p1" onSelect={onSelect} />,
    );
    const sel = screen.getByRole("combobox");
    expect(screen.getByText("All projects")).toBeInTheDocument();
    expect(screen.getByText("school (2!)")).toBeInTheDocument();
    fireEvent.change(sel, { target: { value: "" } });
    expect(onSelect).toHaveBeenCalledWith(null);
    fireEvent.change(sel, { target: { value: "p2" } });
    expect(onSelect).toHaveBeenCalledWith("p2");
  });
});
```

- [ ] **Step 2: Implement `ProjectSwitcher`**

`web/src/components/board/ProjectSwitcher.tsx` (raw `<select>` — the house convention; no select primitive exists):

```tsx
import type { ProjectDTO } from "@/lib/contracts";

export function ProjectSwitcher({ projects, needs, current, onSelect }: {
  projects: ProjectDTO[]; needs: Map<string, number>; current?: string;
  onSelect(id: string | null): void;
}) {
  return (
    <select
      aria-label="Project"
      value={current ?? ""}
      onChange={(e) => onSelect(e.target.value || null)}
      className="h-8 rounded-md border border-input bg-background px-2 text-sm"
    >
      <option value="">All projects</option>
      {projects.map((p) => {
        const n = needs.get(p.id) ?? 0;
        return (
          <option key={p.id} value={p.id}>
            {p.name}{n > 0 ? ` (${n}!)` : ""}
          </option>
        );
      })}
    </select>
  );
}
```

- [ ] **Step 3: Implement the routes file**

Create `web/src/routes/projects.tsx`. `TimelineView` (Task 14), `EpicDrawer` (Task 15), `ProjectHeader` (Task 18), and the New/Edit project flows (Tasks 19/20) do not exist yet — this file ships with clearly-marked mount points that later tasks fill in (each later task's Files section names this file). v1 of this file renders: shell, states, tabs (Timeline tab shows "Timeline coming in this branch — Task 14" placeholder text), BoardView, and drawer-open wiring that no-ops until Task 15.

```tsx
import * as React from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { BoardView } from "@/components/board/BoardView";
import { ProjectSwitcher } from "@/components/board/ProjectSwitcher";
import { allBoardKey, getAllBoard } from "@/lib/api-client";
import type { EpicDTO, SessionState } from "@/lib/contracts";
import { effectiveSessionState } from "@/lib/state";
import { needsByProject, useBoardAttention } from "@/store/board";
import { useStateSnapshot } from "@/store/session-state";

export interface ProjectsSearch { tab: "board" | "timeline"; epic: string; }

export const validateProjectsSearch = (s: Record<string, unknown>): ProjectsSearch => ({
  tab: s.tab === "timeline" ? "timeline" : "board",
  epic: typeof s.epic === "string" ? s.epic : "",
});

export function ProjectsIndexRoute() {
  return <ProjectsShell projectId={null} />;
}

export function ProjectDetailRoute() {
  // strict:false reads params without binding to a generated route id — the
  // repo's own terminal.tsx does exactly this (routes/terminal.tsx:23), which
  // sidesteps guessing the pathless-authRoute id.
  const { projectId } = useParams({ strict: false }) as { projectId: string };
  return <ProjectsShell projectId={projectId} />;
}

function ProjectsShell({ projectId }: { projectId: string | null }) {
  const navigate = useNavigate();
  // Both routes share the search schema; read it loosely to avoid binding to
  // one route id.
  const search = useSearch({ strict: false }) as Partial<ProjectsSearch>;
  const tab = search.tab === "timeline" ? "timeline" : "board";
  const epicId = search.epic ?? "";

  const boardQ = useQuery({ queryKey: allBoardKey(), queryFn: getAllBoard });
  const attention = useBoardAttention((s) => s.attention);
  const needs = React.useMemo(() => needsByProject(attention), [attention]);
  const snap = useStateSnapshot();

  const data = boardQ.data;
  const projects = React.useMemo(() => new Map((data?.projects ?? []).map((p) => [p.id, p])), [data]);
  const project = projectId ? projects.get(projectId) : undefined;
  const epics = React.useMemo(
    () => (data?.epics ?? []).filter((e) => !projectId || e.project_id === projectId),
    [data, projectId],
  );

  // Live session state for Working cards: hook-fed state keyed by the
  // project's server/target + the epic's session name. An empty project
  // target means "agent default", whose state frames label is "default".
  const liveStateOf = React.useCallback(
    (e: EpicDTO): SessionState | undefined => {
      const p = projects.get(e.project_id);
      if (!p || !e.session) return undefined;
      return effectiveSessionState(snap, p.server_id, p.target || "default", e.session, undefined);
    },
    [projects, snap],
  );

  const setSearch = (next: Partial<ProjectsSearch>) =>
    void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...validateProjectsSearch(prev), ...next }) });

  const openProject = (id: string | null) =>
    void navigate(
      id
        ? { to: "/projects/$projectId", params: { projectId: id }, search: { tab, epic: "" } }
        : { to: "/projects", search: { tab, epic: "" } },
    );

  return (
    <div className="flex h-full flex-col">
      <header
        className="flex flex-wrap items-center gap-3 border-b border-border bg-background px-4 py-2"
        style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}
      >
        <button className="font-semibold" onClick={() => void navigate({ to: "/" })}>AgentMon</button>
        <span className="text-sm text-muted-foreground">
          / Projects{project ? <> / <span className="text-foreground">{project.name}</span></> : null}
        </span>
        {data && data.projects.length > 0 && (
          <ProjectSwitcher projects={data.projects} needs={needs} current={projectId ?? undefined} onSelect={openProject} />
        )}
        <span className="ml-auto" />
        {/* Task 18 mounts <ProjectHeader project={project} …/> here for the narrowed view. */}
        {/* Task 19 mounts the New-project button here for the All view. */}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {boardQ.isLoading ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">Loading…</div>
        ) : boardQ.isError || !data ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm">
            <span className="text-destructive">Failed to load the board.</span>
            <Button variant="outline" size="sm" onClick={() => void boardQ.refetch()}>Retry</Button>
          </div>
        ) : !data.orchestrator_enabled ? (
          <DormantNotice />
        ) : projectId && !project ? (
          <div className="p-4 text-sm text-muted-foreground">Project not found — it may have been deleted.</div>
        ) : data.projects.length === 0 ? (
          <ZeroProjects />
        ) : (
          <>
            <div className="mb-3 flex items-center gap-1 border-b border-border">
              {(["board", "timeline"] as const).map((t) => (
                <button
                  key={t}
                  role="tab"
                  aria-selected={tab === t}
                  onClick={() => setSearch({ tab: t })}
                  className={
                    tab === t
                      ? "-mb-px border-b-2 border-primary px-3 py-2 text-sm font-semibold"
                      : "px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground"
                  }
                >
                  {t === "board" ? "Board" : "Timeline"}
                </button>
              ))}
            </div>
            {tab === "board" ? (
              <BoardView
                epics={epics}
                projects={projects}
                showProject={!projectId}
                liveStateOf={liveStateOf}
                onOpenEpic={(id) => setSearch({ epic: id })}
              />
            ) : (
              <div className="p-4 text-sm text-muted-foreground">Timeline coming in this branch — Task 14.</div>
            )}
            {/* Task 15 replaces this with <EpicDrawer …> resolution on epicId. */}
            {epicId ? null : null}
          </>
        )}
      </div>
    </div>
  );
}

function DormantNotice() {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">The orchestrator is dormant</div>
      <p className="mt-2 text-muted-foreground">
        It needs a GitHub token: add <code className="rounded bg-background px-1">github.token</code> to the hub
        config (<code className="rounded bg-background px-1">deploy/data/config.yaml</code> on the hub host) and
        restart the hub. See the README's orchestrator section for the config keys.
      </p>
    </div>
  );
}

// onNew is optional so Task 12 can render this before the create flow exists;
// Task 19 passes `onNew={() => setCreating(true)}` from ProjectsShell (setCreating
// lives in that scope — it must be passed IN as a prop, never referenced inside
// this standalone component).
function ZeroProjects({ onNew }: { onNew?: () => void }) {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">No projects yet</div>
      <p className="mt-2 text-muted-foreground">
        A project binds a GitHub repo to a host: the orchestrator turns issues into epics, runs them in tmux
        sessions on the host, and opens PRs — summoning you only at decision points.
      </p>
      {onNew ? (
        <Button size="sm" className="mt-3" onClick={onNew}>New project</Button>
      ) : (
        <p className="mt-2 text-muted-foreground">Registration UI lands later in this branch (Task 19).</p>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Register the routes**

In `web/src/router.tsx`, import and add under `authRoute`:

```tsx
import { ProjectsIndexRoute, ProjectDetailRoute, validateProjectsSearch } from "./routes/projects";

const projectsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/projects",
  validateSearch: validateProjectsSearch,
  component: ProjectsIndexRoute,
});

const projectRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/projects/$projectId",
  validateSearch: validateProjectsSearch,
  component: ProjectDetailRoute,
});
```

and extend the tree: `authRoute.addChildren([indexRoute, terminalRoute, projectsRoute, projectRoute])`.

**Route-id note:** `ProjectDetailRoute` uses `useParams({ strict: false })` — the repo idiom (routes/terminal.tsx:23 does the same), avoiding any dependence on the pathless authRoute's generated id.

- [ ] **Step 5: Add the header Projects button**

In `web/src/routes/index.tsx`, inside the header's right-side cluster (before the "New session" button):

```tsx
          <Button variant="outline" size="sm" className="relative" onClick={() => navigate({ to: "/projects", search: { tab: "board", epic: "" } })}>
            Projects
            {needsTotal > 0 && (
              <span className="absolute -right-1.5 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-bold text-destructive-foreground">
                {needsTotal}
              </span>
            )}
          </Button>
```

with `const needsTotal = useNeedsTotal();` near the other hooks and `import { useNeedsTotal } from "@/store/board";`.

- [ ] **Step 6: Tests + manual smoke**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/ProjectSwitcher.test.tsx && npm run typecheck && npm run test:run`
Expected: all PASS.

Manual smoke (no hub needed): `npm run dev`, log-in screen appears; typecheck + existing tests green is the gate here — full visual check happens against the toy stack at the final checkpoint.

- [ ] **Step 7: Commit**

```bash
cd /root/agentmon && git add web/src/routes/projects.tsx web/src/components/board/ProjectSwitcher.tsx web/src/components/board/ProjectSwitcher.test.tsx web/src/router.tsx web/src/routes/index.tsx
git commit -m "feat(web): /projects routes with All-view board, switcher, dormant/zero states, header entry"
```

- [ ] **Step 8: CHECKPOINT 2 — STOP**

Board core is browsable end-to-end (routes + kanban + live data). Report tasks 7-12 status + web suite result. WAIT for review/fixes/"continue" before Task 13.

---

### Task 13: Web — pure gantt math

**Files:**
- Create: `web/src/lib/gantt.ts`
- Test: `web/src/lib/gantt.test.ts`

**Interfaces:**
- Consumes: `EpicDTO`, `stageMeta` (Task 7).
- Produces: `GanttRange`, `GanttWindow`, `ganttWindow(epics, now, range)`, `GanttTick`, `ganttTicks(w)`, `GanttBar`, `ganttBar(e, w, now)`, `fmtDur(ms)`, `arrowPath(from, to)` — exact shapes below.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/gantt.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import type { EpicDTO } from "@/lib/contracts";
import { arrowPath, fmtDur, ganttBar, ganttTicks, ganttWindow } from "@/lib/gantt";

const NOW = Date.parse("2026-07-11T12:00:00Z");
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [],
  stage: "implementing", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T10:00:00Z", merged_at: "", ...over,
});

describe("ganttWindow", () => {
  it("spans earliest start to a bit past now; null when nothing started", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    expect(w.t0).toBe(Date.parse("2026-07-11T08:00:00Z"));
    expect(w.t1).toBeGreaterThan(NOW);
    expect(ganttWindow([epic({ started_at: "" })], NOW, "all")).toBeNull();
  });
  it("clamps to the range", () => {
    const w = ganttWindow([epic({ started_at: "2026-07-01T00:00:00Z" })], NOW, "24h")!;
    expect(w.t0).toBe(NOW - 86400000);
  });
});

describe("ganttBar", () => {
  it("running bar grows to now with a live edge", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({}), w, NOW)!;
    expect(b.live).toBe(true);
    expect(b.waitTailPct).toBe(0);
    expect(b.leftPct).toBe(0);
    expect(b.leftPct + b.widthPct).toBeGreaterThan(95);
  });
  it("escalated bar stops at stage_updated_at and grows a wait tail", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({ stage: "escalated" }), w, NOW)!;
    expect(b.live).toBe(false);
    expect(b.waitTailPct).toBeGreaterThan(0);
  });
  it("merged bar ends at merged_at; queued epics have no bar", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({ stage: "merged", merged_at: "2026-07-11T11:00:00Z" }), w, NOW)!;
    expect(b.live).toBe(false);
    expect(b.endMs).toBe(Date.parse("2026-07-11T11:00:00Z"));
    expect(ganttBar(epic({ stage: "queued", started_at: "" }), w, NOW)).toBeNull();
  });
});

describe("ticks + helpers", () => {
  it("short windows tick by hour, long windows by day, all within bounds", () => {
    const short = ganttTicks({ t0: NOW - 6 * 3600000, t1: NOW });
    const long = ganttTicks({ t0: NOW - 5 * 86400000, t1: NOW });
    expect(short.length).toBeGreaterThan(2);
    expect(long.length).toBeGreaterThanOrEqual(4);
    for (const t of [...short, ...long]) {
      expect(t.pct).toBeGreaterThanOrEqual(0);
      expect(t.pct).toBeLessThanOrEqual(100);
    }
  });
  it("fmtDur", () => {
    expect(fmtDur(40 * 60000)).toBe("40m");
    expect(fmtDur(5 * 3600000)).toBe("5h");
    expect(fmtDur(30 * 3600000)).toBe("1d 6h");
  });
  it("arrowPath draws an elbow", () => {
    expect(arrowPath({ x: 10, y: 5 }, { x: 100, y: 50 })).toBe("M10,5 H86 V50 H98");
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/lib/gantt.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/lib/gantt.ts`**

```ts
import { stageMeta } from "@/lib/board";
import type { EpicDTO } from "@/lib/contracts";

export type GanttRange = "24h" | "7d" | "all";
export interface GanttWindow { t0: number; t1: number; }
export interface GanttTick { pct: number; label: string; }
export interface GanttBar {
  leftPct: number; widthPct: number; waitTailPct: number; live: boolean;
  startMs: number; endMs: number;
}

const DAY = 86400000;
const HOUR = 3600000;

// Window: earliest visible start → now (+2% pad), clamped by the range.
// Null when nothing has started — the Timeline renders an empty-state note.
export function ganttWindow(epics: EpicDTO[], now: number, range: GanttRange): GanttWindow | null {
  const starts = epics.filter((e) => e.started_at).map((e) => Date.parse(e.started_at));
  if (starts.length === 0) return null;
  let t0 = Math.min(...starts);
  if (range === "24h") t0 = Math.max(t0, now - DAY);
  if (range === "7d") t0 = Math.max(t0, now - 7 * DAY);
  const span = Math.max(now - t0, HOUR); // ≥1h so fresh boards still have width
  return { t0, t1: now + span * 0.02 };
}

export function ganttTicks(w: GanttWindow): GanttTick[] {
  const span = w.t1 - w.t0;
  const ticks: GanttTick[] = [];
  if (span >= 2 * DAY) {
    const d = new Date(w.t0);
    d.setHours(0, 0, 0, 0);
    let ms = d.getTime();
    if (ms < w.t0) ms += DAY;
    for (; ms < w.t1; ms += DAY) {
      ticks.push({ pct: ((ms - w.t0) / span) * 100, label: new Date(ms).toLocaleDateString(undefined, { weekday: "short", day: "numeric" }) });
    }
  } else {
    const step = Math.max(1, Math.ceil(span / HOUR / 12)) * HOUR;
    for (let ms = Math.ceil(w.t0 / step) * step; ms < w.t1; ms += step) {
      ticks.push({ pct: ((ms - w.t0) / span) * 100, label: new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) });
    }
  }
  return ticks;
}

const clampPct = (n: number) => Math.min(Math.max(n, 0), 100);

// Actuals only (spec D5). Bar = started_at → (terminal end | waiting-entry |
// now). Waiting epics (escalated/stalled) freeze the bar at stage_updated_at
// and grow a hatched wait-tail to now — "how long has this waited on me".
export function ganttBar(e: EpicDTO, w: GanttWindow, now: number): GanttBar | null {
  if (!e.started_at) return null;
  const startMs = Date.parse(e.started_at);
  const col = stageMeta(e.stage).column;
  const terminal = e.stage === "merged" || e.stage === "failed" || e.stage === "canceled";
  const waiting = col === "needs";
  const endMs = terminal
    ? Date.parse(e.merged_at || e.stage_updated_at || e.started_at)
    : waiting
      ? (e.stage_updated_at ? Date.parse(e.stage_updated_at) : now)
      : now;
  const span = w.t1 - w.t0;
  const leftPct = clampPct(((startMs - w.t0) / span) * 100);
  const rightPct = clampPct(((endMs - w.t0) / span) * 100);
  const nowPct = clampPct(((now - w.t0) / span) * 100);
  return {
    leftPct,
    widthPct: Math.max(rightPct - leftPct, 0.8),
    waitTailPct: waiting ? Math.max(nowPct - rightPct, 0) : 0,
    live: !terminal && !waiting,
    startMs,
    endMs,
  };
}

export function fmtDur(ms: number): string {
  const m = Math.round(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// Elbow connector (mockup style): out of the source bar, over, down/up, into
// the target bar. Pixel domain — the component measures its container.
export function arrowPath(from: { x: number; y: number }, to: { x: number; y: number }): string {
  const mx = Math.max(from.x + 10, to.x - 14);
  return `M${from.x},${from.y} H${mx} V${to.y} H${to.x - 2}`;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/lib/gantt.test.ts`
Expected: PASS.

- [ ] **Step 5: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/lib/gantt.ts web/src/lib/gantt.test.ts
git commit -m "feat(web): pure gantt math (window, ticks, bars, wait-tails, arrows)"
```

---

### Task 14: Web — TimelineView

**Files:**
- Create: `web/src/components/board/TimelineView.tsx`
- Modify: `web/src/routes/projects.tsx` (replace the Timeline placeholder)
- Test: `web/src/components/board/TimelineView.test.tsx`

**Interfaces:**
- Consumes: Task 13 (`ganttWindow`, `ganttTicks`, `ganttBar`, `fmtDur`, `arrowPath`, `GanttRange`), Task 7 (`stageMeta`), Task 9 (`StageChip`), `ProviderTag`, `cn`.
- Produces: `TimelineView {epics, projects: Map<string, ProjectDTO>, groupByProject: boolean, onOpenEpic(id: string): void}`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/board/TimelineView.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TimelineView } from "@/components/board/TimelineView";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: false,
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "one", labels: [], blocked_by: [],
  stage: "implementing", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T09:00:00Z", merged_at: "", ...over,
});
const projects = new Map([["p1", project]]);

describe("TimelineView", () => {
  it("renders group header, bar rows, and barless queued rows", () => {
    render(
      <TimelineView groupByProject epics={[
        epic({}),
        epic({ id: "e2", issue: 2, title: "two", stage: "queued", started_at: "", blocked_by: [1] }),
      ]} projects={projects} onOpenEpic={() => {}} />,
    );
    expect(screen.getByText("school")).toBeInTheDocument();
    expect(screen.getByText(/one/)).toBeInTheDocument();
    expect(screen.getByText(/blocked by #1/)).toBeInTheDocument();
  });

  it("row click opens the drawer; range picker switches", () => {
    const onOpen = vi.fn();
    render(<TimelineView groupByProject={false} epics={[epic({})]} projects={projects} onOpenEpic={onOpen} />);
    fireEvent.click(screen.getByText(/one/));
    expect(onOpen).toHaveBeenCalledWith("e1");
    fireEvent.click(screen.getByRole("button", { name: "24h" }));
  });

  it("shows the empty note when nothing has started", () => {
    render(<TimelineView groupByProject={false} epics={[epic({ started_at: "", stage: "queued" })]} projects={projects} onOpenEpic={() => {}} />);
    expect(screen.getByText(/Nothing has started yet/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/TimelineView.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/TimelineView.tsx`**

```tsx
import * as React from "react";
import { Button } from "@/components/ui/button";
import { ProviderTag } from "@/components/ProviderTag";
import { StageChip } from "@/components/board/StageChip";
import { cardProvider, stageMeta } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import {
  arrowPath, fmtDur, ganttBar, ganttTicks, ganttWindow, type GanttRange,
} from "@/lib/gantt";
import { cn } from "@/lib/utils";

const RANGES: GanttRange[] = ["24h", "7d", "all"];
const LEFT_RAIL = 260; // px — title rail width, matches the grid template below

export function TimelineView({ epics, projects, groupByProject, onOpenEpic }: {
  epics: EpicDTO[]; projects: Map<string, ProjectDTO>; groupByProject: boolean;
  onOpenEpic(id: string): void;
}) {
  const [range, setRange] = React.useState<GanttRange>("all");
  const now = Date.now();
  const w = ganttWindow(epics, now, range);
  const bodyRef = React.useRef<HTMLDivElement>(null);
  const [arrows, setArrows] = React.useState<string[]>([]);

  // Rows: optionally grouped under project headers, started-first inside a group.
  const rows = React.useMemo(() => {
    const byStart = (a: EpicDTO, b: EpicDTO) =>
      (a.started_at ? Date.parse(a.started_at) : Infinity) - (b.started_at ? Date.parse(b.started_at) : Infinity) || a.issue - b.issue;
    if (!groupByProject) return epics.slice().sort(byStart).map((e) => ({ kind: "epic" as const, epic: e }));
    const out: Array<{ kind: "header"; name: string } | { kind: "epic"; epic: EpicDTO }> = [];
    for (const p of projects.values()) {
      const es = epics.filter((e) => e.project_id === p.id).sort(byStart);
      if (es.length === 0) continue;
      out.push({ kind: "header", name: p.name });
      for (const e of es) out.push({ kind: "epic", epic: e });
    }
    return out;
  }, [epics, projects, groupByProject]);

  // Dependency arrows: measured from the rendered bars (positions are % of an
  // unknown track width), recomputed after paint and on resize.
  React.useEffect(() => {
    const el = bodyRef.current;
    if (!el || !w) { setArrows([]); return; }
    const compute = () => {
      const bodyRect = el.getBoundingClientRect();
      const paths: string[] = [];
      for (const r of rows) {
        if (r.kind !== "epic") continue;
        for (const dep of r.epic.blocked_by ?? []) {
          const from = el.querySelector<HTMLElement>(`[data-bar="${r.epic.project_id}:${dep}"]`);
          const to = el.querySelector<HTMLElement>(`[data-bar="${r.epic.project_id}:${r.epic.issue}"]`);
          if (!from || !to) continue;
          const fb = from.getBoundingClientRect();
          const tb = to.getBoundingClientRect();
          paths.push(arrowPath(
            { x: fb.right - bodyRect.left, y: fb.top + fb.height / 2 - bodyRect.top },
            { x: tb.left - bodyRect.left, y: tb.top + tb.height / 2 - bodyRect.top },
          ));
        }
      }
      setArrows(paths);
    };
    const raf = requestAnimationFrame(compute);
    window.addEventListener("resize", compute);
    return () => { cancelAnimationFrame(raf); window.removeEventListener("resize", compute); };
  }, [rows, w]);

  if (!w) {
    return <div className="p-4 text-sm text-muted-foreground">Nothing has started yet — the Timeline draws real bars only.</div>;
  }
  const ticks = ganttTicks(w);
  const nowPct = Math.min(((now - w.t0) / (w.t1 - w.t0)) * 100, 100);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1 self-end">
        {RANGES.map((r) => (
          <Button key={r} variant={range === r ? "default" : "outline"} size="sm" onClick={() => setRange(r)}>{r}</Button>
        ))}
      </div>
      <div className="overflow-x-auto rounded-xl border border-border bg-card">
        <div className="relative min-w-[900px]" ref={bodyRef}>
          {/* axis */}
          <div className="grid border-b border-border" style={{ gridTemplateColumns: `${LEFT_RAIL}px 1fr` }}>
            <div className="px-3 py-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Epic</div>
            <div className="relative h-8">
              {ticks.map((t, i) => (
                <span key={i} className="absolute top-0 flex h-full items-center border-l border-border pl-1.5 text-[11px] text-muted-foreground" style={{ left: `${t.pct}%` }}>
                  {t.label}
                </span>
              ))}
            </div>
          </div>
          {/* rows */}
          {rows.map((r, i) =>
            r.kind === "header" ? (
              <div key={`h${i}`} className="border-t border-border px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                {r.name}
              </div>
            ) : (
              <TimelineRow key={r.epic.id} epic={r.epic} project={projects.get(r.epic.project_id)} w={w} now={now}
                onOpen={() => onOpenEpic(r.epic.id)} />
            ),
          )}
          {/* now line + arrows overlays */}
          <div className="pointer-events-none absolute inset-y-0" style={{ left: `calc(${LEFT_RAIL}px + (100% - ${LEFT_RAIL}px) * ${nowPct / 100})` }}>
            <div className="h-full w-px bg-primary/80" />
            <span className="absolute left-1 top-9 rounded bg-primary px-1 text-[10px] font-semibold text-primary-foreground">now</span>
          </div>
          <svg className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
            {arrows.map((d, i) => (
              <path key={i} d={d} fill="none" className="stroke-muted-foreground/50" strokeWidth={1.3} />
            ))}
          </svg>
        </div>
      </div>
    </div>
  );
}

function TimelineRow({ epic, project, w, now, onOpen }: {
  epic: EpicDTO; project?: ProjectDTO; w: { t0: number; t1: number }; now: number; onOpen(): void;
}) {
  const meta = stageMeta(epic.stage);
  const bar = ganttBar(epic, w, now);
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}
      className={cn("grid min-h-[46px] cursor-pointer border-t border-border/60 hover:bg-card/60", meta.column === "needs" && "bg-red-500/5")}
      style={{ gridTemplateColumns: `260px 1fr` }}
    >
      <div className="flex min-w-0 flex-col justify-center gap-0.5 px-3 py-1.5">
        <div className="truncate text-[13px] font-medium">
          <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
        </div>
        <div className="flex items-center gap-2">
          <StageChip stage={epic.stage} />
          <ProviderTag provider={cardProvider(epic.labels, project?.provider ?? "")} />
        </div>
      </div>
      <div className="relative">
        {bar ? (
          <div
            data-bar={`${epic.project_id}:${epic.issue}`}
            title={`#${epic.issue} ${epic.title} · ${meta.label} · ${fmtDur(bar.endMs - bar.startMs)}`}
            className={cn("absolute top-1/2 h-4 -translate-y-1/2 rounded", meta.barClass)}
            style={{ left: `${bar.leftPct}%`, width: `${bar.widthPct}%` }}
          >
            {bar.waitTailPct > 0 && (
              <div className="absolute left-full top-0 h-full rounded-r bg-red-500/30 [background-image:repeating-linear-gradient(135deg,transparent_0_4px,rgba(239,68,68,.5)_4px_8px)]"
                style={{ width: `${(bar.waitTailPct / bar.widthPct) * 100}%` }} />
            )}
            {bar.live && <div className="absolute -right-0.5 -top-0.5 h-5 w-1 animate-pulse rounded bg-amber-500 motion-reduce:animate-none" />}
            <span className="absolute left-full top-1/2 ml-2 -translate-y-1/2 whitespace-nowrap text-[11px] text-muted-foreground">
              {epic.stage === "merged" ? `✓ ${fmtDur(bar.endMs - bar.startMs)}` : fmtDur(bar.endMs - bar.startMs)}
            </span>
          </div>
        ) : (
          <div className="flex h-full items-center px-2 text-[11px] text-muted-foreground">
            queued{(epic.blocked_by ?? []).length > 0 ? ` · blocked by ${(epic.blocked_by ?? []).map((n) => `#${n}`).join(" ")}` : ""}
          </div>
        )}
      </div>
    </div>
  );
}
```

Wait-tail width note: the tail div lives INSIDE the bar (its `%` width is relative to the bar), hence the `(waitTailPct / widthPct) * 100` conversion — keep it, it's deliberate.

- [ ] **Step 4: Wire into the route**

In `web/src/routes/projects.tsx`: import `TimelineView` and replace the Task-12 placeholder with:

```tsx
              <TimelineView epics={epics} projects={projects} groupByProject={!projectId} onOpenEpic={(id) => setSearch({ epic: id })} />
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/TimelineView.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/TimelineView.tsx web/src/components/board/TimelineView.test.tsx web/src/routes/projects.tsx
git commit -m "feat(web): actuals-only Timeline with wait-tails, dependency arrows, range picker"
```

---

### Task 15: Web — EpicDrawer (needs/verdict, stages, details, actions, guidance)

**Files:**
- Create: `web/src/components/board/EpicDrawer.tsx`
- Modify: `web/src/routes/projects.tsx` (resolve `?epic=` and mount)
- Test: `web/src/components/board/EpicDrawer.test.tsx`

**Interfaces:**
- Consumes: Tasks 7/9 (`parseVerdict`, `isPlanGate`, `stageMeta`, `mergeMode`, `ConfirmButton`, `useEpicActions`, `StageChip`), `getProjectBoard`/`projectBoardKey`, `useMediaQuery`, `KillSessionModal` pattern (`components/KillSessionModal.tsx:30`) for the Cancel confirm.
- Produces: `EpicDrawer {epic: EpicDTO; project: ProjectDTO; onClose(): void}`. Placeholder mount points for `PlanPanel` (Task 16) and `TerminalPreview` (Task 17).

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/board/EpicDrawer.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  epicAction: vi.fn(),
  getProjectBoard: vi.fn(),
  invalidateQueries: vi.fn(),
}));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, epicAction: h.epicAction, getProjectBoard: h.getProjectBoard };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));

import { EpicDrawer } from "@/components/board/EpicDrawer";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true,
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 15, title: "GDPR", labels: [], blocked_by: [],
  stage: "escalated", attempt: 1, session: "epic-15-x", branch: "epic/15-x", pr: 58,
  needs: "2 findings", issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T10:00:00Z", merged_at: "",
  verdict: JSON.stringify({ Findings: { Unresolved: 2 }, Unresolved: ["retention default?"], Tests: { Passed: 4, Failed: 0 } }),
  ...over,
});

describe("EpicDrawer", () => {
  beforeEach(() => {
    qc.clear();
    h.epicAction.mockReset().mockResolvedValue({ ok: true });
    h.getProjectBoard.mockReset().mockResolvedValue({
      project, epics: [],
      events: { e1: [{ from: "planning", to: "implementing", source: "report", note: "", ts: "2026-07-11T08:30:00Z" }] },
    });
  });

  it("renders verdict block, stage history, details, and GitHub links", async () => {
    render(<EpicDrawer epic={epic({})} project={project} onClose={() => {}} />, { wrapper });
    expect(screen.getByText(/2 findings/)).toBeInTheDocument();
    expect(screen.getByText(/retention default\?/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/planning → implementing/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /PR #58/ })).toHaveAttribute("href", "https://github.com/o/r/pull/58");
    expect(screen.getByRole("link", { name: /Issue #15/ })).toHaveAttribute("href", "https://github.com/o/r/issues/15");
    expect(screen.getByText("epic/15-x")).toBeInTheDocument();
  });

  it("cancel requires the modal confirm and posts the action", async () => {
    render(<EpicDrawer epic={epic({})} project={project} onClose={() => {}} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Cancel epic" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, cancel it" }));
    await waitFor(() => expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "cancel", epic_id: "e1" }));
  });

  it("sends guidance from the textarea", async () => {
    render(<EpicDrawer epic={epic({ stage: "implementing" })} project={project} onClose={() => {}} />, { wrapper });
    fireEvent.change(screen.getByPlaceholderText(/guidance/i), { target: { value: "focus on RLS" } });
    fireEvent.click(screen.getByRole("button", { name: "Send guidance" }));
    await waitFor(() =>
      expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "guidance", epic_id: "e1", text: "focus on RLS" }));
  });

  it("escape closes", () => {
    const onClose = vi.fn();
    render(<EpicDrawer epic={epic({})} project={project} onClose={onClose} />, { wrapper });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/EpicDrawer.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/EpicDrawer.tsx`**

```tsx
import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { StageChip } from "@/components/board/StageChip";
import { useEpicActions } from "@/hooks/useEpicActions";
import { getProjectBoard, projectBoardKey } from "@/lib/api-client";
import { isPlanGate, mergeMode, parseVerdict, stageMeta } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import { cn } from "@/lib/utils";

export function EpicDrawer({ epic, project, onClose }: {
  epic: EpicDTO; project: ProjectDTO; onClose(): void;
}) {
  const meta = stageMeta(epic.stage);
  const { act, busy } = useEpicActions(epic.project_id);
  const verdict = parseVerdict(epic.verdict);
  const planGate = isPlanGate(epic.needs);
  const running = meta.column === "working";
  const waiting = meta.column === "needs";
  const terminal = epic.stage === "merged" || epic.stage === "failed" || epic.stage === "canceled";
  const [confirmCancel, setConfirmCancel] = React.useState(false);
  const [guidance, setGuidance] = React.useState("");

  // Lazy detail fetch: the per-project board carries each epic's last-20
  // events (spec §5.1 keeps them out of the all-board payload).
  const detailQ = useQuery({ queryKey: projectBoardKey(epic.project_id), queryFn: () => getProjectBoard(epic.project_id) });
  const events = detailQ.data?.events[epic.id] ?? [];

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const gh = `https://github.com/${project.repo}`;

  const section = (title: string, body: React.ReactNode) => (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</div>
      {body}
    </section>
  );

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label={`Epic ${epic.issue} detail`}>
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <aside className="absolute inset-y-0 right-0 flex w-full flex-col border-l border-border bg-background sm:max-w-[560px]">
        <div className="flex items-start gap-2 border-b border-border p-4">
          <h2 className="text-[15px] font-semibold leading-snug">
            <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
          </h2>
          <Button variant="ghost" size="sm" className="ml-auto flex-none" onClick={onClose} aria-label="close">✕</Button>
        </div>
        <div className="flex items-center gap-2 border-b border-border px-4 py-2 text-xs text-muted-foreground">
          <StageChip stage={epic.stage} />
          <span className="font-mono">{project.repo}</span>
          <span>attempt {epic.attempt}</span>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
          {waiting && (
            <div className={cn("rounded-lg border border-red-500/40 bg-card p-3 text-sm")}>
              <div className="font-semibold text-red-400">⚠ {epic.needs || meta.label}</div>
              {verdict && (
                <>
                  <div className="mt-1 text-xs text-muted-foreground">
                    findings {verdict.found} found · {verdict.resolved} resolved · {verdict.unresolvedCount} unresolved
                    · tests {verdict.passed}✓{verdict.failed > 0 ? ` ${verdict.failed}✗` : ""}
                    {verdict.uncertain ? " · runner uncertain" : ""}
                  </div>
                  {verdict.unresolved.length > 0 && (
                    <ul className="mt-2 list-disc pl-5 text-xs">
                      {verdict.unresolved.map((u, i) => <li key={i}>{u}</li>)}
                    </ul>
                  )}
                </>
              )}
            </div>
          )}

          {/* Task 16 mounts <PlanPanel epic={epic} project={project}/> here when planGate. */}
          {planGate && null}

          {/* Task 17 mounts <TerminalPreview project={project} epic={epic} …/> here when running. */}
          {running && null}

          {section("Actions", (
            <div className="flex flex-wrap gap-1.5">
              {waiting && !planGate && (
                <ConfirmButton label={epic.pr > 0 ? `Approve & merge PR #${epic.pr}` : "Approve"} confirmLabel="Sure?"
                  variant="default" disabled={busy !== null}
                  onConfirm={() => void act({ action: "approve", epic_id: epic.id }, `Approving #${epic.issue}`)} />
              )}
              {waiting && (
                <ConfirmButton label="Retry epic" confirmLabel="Retry?" disabled={busy !== null}
                  onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Retrying #${epic.issue}`)} />
              )}
              {!terminal && (
                <Button variant="outline" size="sm" className="text-red-400" onClick={() => setConfirmCancel(true)}>
                  Cancel epic
                </Button>
              )}
              {epic.pr > 0 && (
                <Button variant="outline" size="sm" asChild>
                  <a href={`${gh}/pull/${epic.pr}`} target="_blank" rel="noreferrer">PR #{epic.pr} ↗</a>
                </Button>
              )}
              <Button variant="outline" size="sm" asChild>
                <a href={`${gh}/issues/${epic.issue}`} target="_blank" rel="noreferrer">Issue #{epic.issue} ↗</a>
              </Button>
            </div>
          ))}

          {(running || waiting) && epic.session && section("Guidance", (
            <div className="flex flex-col gap-2">
              <textarea
                value={guidance}
                onChange={(e) => setGuidance(e.target.value)}
                placeholder="Type guidance for the runner session…"
                rows={3}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              <Button size="sm" className="self-end" disabled={!guidance.trim() || busy !== null}
                onClick={() => {
                  const text = guidance.trim();
                  void act({ action: "guidance", epic_id: epic.id, text }, "Guidance sent").then((ok) => { if (ok) setGuidance(""); });
                }}>
                Send guidance
              </Button>
              <p className="text-xs text-muted-foreground">Delivered into the runner's terminal as a submitted message.</p>
            </div>
          ))}

          {section("Pipeline stages", (
            events.length === 0 ? (
              <div className="text-xs text-muted-foreground">{detailQ.isLoading ? "Loading…" : "No transitions recorded."}</div>
            ) : (
              <div className="flex flex-col gap-1.5 text-xs">
                {events.map((ev, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <span className={cn("inline-block size-1.5 flex-none rounded-full", stageMeta(ev.to).dotClass)} />
                    <span>{ev.from} → {ev.to}</span>
                    {ev.note && <span className="truncate text-muted-foreground" title={ev.note}>· {ev.note}</span>}
                    <span className="ml-auto flex-none text-muted-foreground">{ev.source} · {ev.ts.slice(11, 16)}</span>
                  </div>
                ))}
              </div>
            )
          ))}

          {section("Details", (
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Branch</span><span className="font-mono">{epic.branch || "—"}</span>
              <span className="text-muted-foreground">Blocked by</span>
              <span>{(epic.blocked_by ?? []).length > 0 ? (epic.blocked_by ?? []).map((n) => `#${n}`).join(", ") : "—"}</span>
              <span className="text-muted-foreground">Session</span><span className="font-mono">{epic.session || "—"}</span>
              <span className="text-muted-foreground">Host</span><span className="font-mono">{project.server_id}{project.target ? ` · ${project.target}` : ""}</span>
              <span className="text-muted-foreground">Autonomy</span><span>{mergeMode(epic.labels)}</span>
              <span className="text-muted-foreground">Queued</span><span>{epic.queued_at || "—"}</span>
              <span className="text-muted-foreground">Started</span><span>{epic.started_at || "—"}</span>
              <span className="text-muted-foreground">Merged</span><span>{epic.merged_at || "—"}</span>
            </div>
          ))}
        </div>
      </aside>

      {confirmCancel && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-black/50 p-4" onClick={() => setConfirmCancel(false)}>
          <div className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-base font-semibold">Cancel epic #{epic.issue}?</h3>
            <p className="mt-2 text-sm text-muted-foreground">
              Kills the runner session and closes this attempt. The issue stays open on GitHub; Retry starts a fresh attempt later.
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setConfirmCancel(false)}>Keep running</Button>
              <Button variant="destructive" disabled={busy !== null}
                onClick={() => void act({ action: "cancel", epic_id: epic.id }, `Canceled #${epic.issue}`).then(() => setConfirmCancel(false))}>
                Yes, cancel it
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Mount in the route**

In `web/src/routes/projects.tsx`, replace the Task-12 drawer placeholder (`{epicId ? null : null}`) with:

```tsx
            {epicId && (() => {
              const e = (data.epics ?? []).find((x) => x.id === epicId);
              const p = e ? projects.get(e.project_id) : undefined;
              if (!e || !p) {
                return (
                  <div className="fixed inset-0 z-50" role="dialog" aria-modal="true">
                    <div className="absolute inset-0 bg-black/50" onClick={() => setSearch({ epic: "" })} />
                    <div className="absolute right-4 top-4 rounded-lg border border-border bg-background p-4 text-sm">
                      Epic not found — it may have aged out of the board.
                      <Button variant="outline" size="sm" className="ml-3" onClick={() => setSearch({ epic: "" })}>Close</Button>
                    </div>
                  </div>
                );
              }
              return <EpicDrawer epic={e} project={p} onClose={() => setSearch({ epic: "" })} />;
            })()}
```

with `import { EpicDrawer } from "@/components/board/EpicDrawer";`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/EpicDrawer.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/EpicDrawer.tsx web/src/components/board/EpicDrawer.test.tsx web/src/routes/projects.tsx
git commit -m "feat(web): epic drawer with verdict block, stage history, actions, guidance, cancel confirm"
```

---

### Task 16: Web — PlanPanel (react-markdown)

**Files:**
- Modify: `web/package.json` + `web/package-lock.json` (via npm install)
- Modify: `web/src/index.css` (markdown styles)
- Create: `web/src/components/board/PlanPanel.tsx`
- Modify: `web/src/components/board/EpicDrawer.tsx` (mount at the plan-gate mount point)
- Test: `web/src/components/board/PlanPanel.test.tsx`

**Interfaces:**
- Consumes: `getEpicPlan`/`epicPlanKey` (Task 7), `ApiError`, `useEpicActions`, `ConfirmButton`.
- Produces: `PlanPanel {epic: EpicDTO}` — fetches + renders the plan markdown with Approve-plan action.

- [ ] **Step 1: Install the dependency**

```bash
cd /root/agentmon/web && npm install react-markdown remark-gfm
```

(react-markdown escapes raw HTML by default — no sanitizer needed for our own plan docs.)

- [ ] **Step 2: Write the failing tests**

Create `web/src/components/board/PlanPanel.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ getEpicPlan: vi.fn(), epicAction: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, getEpicPlan: h.getEpicPlan, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { PlanPanel } from "@/components/board/PlanPanel";
import { ApiError } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);
const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true,
};
const epic: EpicDTO = {
  id: "e1", project_id: "p1", issue: 7, title: "t", labels: [], blocked_by: [],
  stage: "escalated", attempt: 1, session: "", branch: "epic/7-x", pr: 0,
  needs: "plan-gate: plan ready at docs/plans/epic-7.md", issue_state: "open",
  queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
};

describe("PlanPanel", () => {
  beforeEach(() => { qc.clear(); h.getEpicPlan.mockReset(); h.epicAction.mockReset().mockResolvedValue({ ok: true }); });

  it("renders the plan markdown with path/ref and an approve action", async () => {
    h.getEpicPlan.mockResolvedValue({ path: "docs/plans/epic-7.md", ref: "epic/7-x", markdown: "# The Plan\n\n- step one" });
    render(<PlanPanel epic={epic} project={project} />, { wrapper });
    await waitFor(() => expect(screen.getByRole("heading", { name: "The Plan" })).toBeInTheDocument());
    expect(screen.getByText(/docs\/plans\/epic-7.md @ epic\/7-x/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve plan" })).toBeInTheDocument();
  });

  it("shows the hub's 404 message verbatim with a GitHub fallback link", async () => {
    h.getEpicPlan.mockRejectedValue(new ApiError(404, "no plan doc found at docs/plans/epic-7.md on epic/7-x"));
    render(<PlanPanel epic={epic} project={project} />, { wrapper });
    await waitFor(() => expect(screen.getByText(/no plan doc found/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /View the branch on GitHub/ })).toHaveAttribute("href", "https://github.com/o/r/tree/epic/7-x");
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/PlanPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement**

Append to `web/src/index.css` (after the existing layers — minimal markdown typography; Tailwind's base reset strips element styles):

```css
/* Plan-panel markdown (react-markdown output) */
.markdown { font-size: 0.85rem; line-height: 1.55; }
.markdown h1, .markdown h2, .markdown h3, .markdown h4 { font-weight: 600; margin: 0.9em 0 0.35em; }
.markdown h1 { font-size: 1.15rem; } .markdown h2 { font-size: 1.05rem; } .markdown h3 { font-size: 0.95rem; }
.markdown p, .markdown ul, .markdown ol, .markdown pre, .markdown table { margin: 0.4em 0; }
.markdown ul { list-style: disc; padding-left: 1.4em; } .markdown ol { list-style: decimal; padding-left: 1.4em; }
.markdown code { background: hsl(var(--card)); border: 1px solid hsl(var(--border)); border-radius: 4px; padding: 0 0.25em; font-size: 0.9em; }
.markdown pre { background: hsl(var(--card)); border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 0.6em 0.8em; overflow-x: auto; }
.markdown pre code { background: none; border: 0; padding: 0; }
.markdown table { border-collapse: collapse; display: block; overflow-x: auto; }
.markdown th, .markdown td { border: 1px solid hsl(var(--border)); padding: 0.25em 0.6em; }
.markdown a { color: hsl(var(--primary)); text-decoration: underline; }
.markdown blockquote { border-left: 3px solid hsl(var(--border)); padding-left: 0.8em; color: hsl(var(--muted-foreground)); }
```

Create `web/src/components/board/PlanPanel.tsx`:

```tsx
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useQuery } from "@tanstack/react-query";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { useEpicActions } from "@/hooks/useEpicActions";
import { ApiError, epicPlanKey, getEpicPlan } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

// Plan review "plan mode" (spec §8.2): render the plan committed on the epic
// branch. Reviewing a plan from a phone is the whole point — real markdown,
// not a <pre>.
//
// APPROVAL MECHANISM (verified against the runner skill + Orchestrator):
// a plan-gate epic is `escalated` with NO PR, so `Approve()` — which requires
// PRNumber>0 and merges — returns "no PR to merge" (orchestrator.go:793-805).
// The runner's epic-pipeline skill resumes past a plan gate on RETRY: a fresh
// session's assess-artifacts step finds the committed plan and continues
// (agent/internal/runnerfiles/files/claude/epic-pipeline.md:43,116-117;
// Retry() transitions escalated→queued + kills the session, orchestrator.go:849).
// So "Approve plan" fires the RETRY action, not approve.
export function PlanPanel({ epic, project }: { epic: EpicDTO; project: ProjectDTO }) {
  const { act, busy } = useEpicActions(epic.project_id);
  const q = useQuery({
    queryKey: epicPlanKey(epic.project_id, epic.id),
    queryFn: () => getEpicPlan(epic.project_id, epic.id),
    staleTime: 30_000,
    retry: false,
  });
  // Fallback when the proxy can't return the doc (missing / >256 KiB): a link
  // to the branch on GitHub so the human can still read the plan (spec §11).
  const branchUrl = epic.branch ? `https://github.com/${project.repo}/tree/${epic.branch}` : `https://github.com/${project.repo}`;

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Plan review</div>
      {q.isLoading ? (
        <div className="text-xs text-muted-foreground">Loading plan…</div>
      ) : q.isError ? (
        <div className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
          <div>{q.error instanceof ApiError ? q.error.message : "Couldn't load the plan."}</div>
          <a href={branchUrl} target="_blank" rel="noreferrer" className="mt-1 inline-block text-primary underline">
            View the branch on GitHub ↗
          </a>
        </div>
      ) : q.data ? (
        <>
          <div className="font-mono text-[11px] text-muted-foreground">{q.data.path} @ {q.data.ref}</div>
          <div className="markdown max-h-[50vh] overflow-y-auto rounded-md border border-border bg-background p-3">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{q.data.markdown}</ReactMarkdown>
          </div>
          <ConfirmButton label="Approve plan" confirmLabel="Approve — runner resumes?" variant="default"
            className="self-start" disabled={busy !== null}
            onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Plan approved — #${epic.issue} resumes`)} />
        </>
      ) : null}
    </section>
  );
}
```

In `web/src/components/board/EpicDrawer.tsx`, replace the plan-gate mount point (`{planGate && null}`) with:

```tsx
          {planGate && <PlanPanel epic={epic} project={project} />}
```

plus `import { PlanPanel } from "@/components/board/PlanPanel";`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/PlanPanel.test.tsx src/components/board/EpicDrawer.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/package.json web/package-lock.json web/src/index.css web/src/components/board/PlanPanel.tsx web/src/components/board/PlanPanel.test.tsx web/src/components/board/EpicDrawer.tsx
git commit -m "feat(web): plan-review panel with markdown rendering and approve-plan action"
```

---

### Task 17: Web — TerminalPreview + Open full session + CHECKPOINT 3

**Files:**
- Create: `web/src/components/board/TerminalPreview.tsx`
- Modify: `web/src/components/board/EpicDrawer.tsx` (mount + open-full-session handler)
- Test: `web/src/components/board/TerminalPreview.test.tsx`

**Interfaces:**
- Consumes: `TerminalView` (`components/TerminalView.tsx:7` — props `{serverId, paneId, target, active?, fontSize?, theme?}`), `listSessions`/`sessionsKey`/`listServers`/`serversKey`, `usePanes.openPane` (`store/panes.ts:29`), `useMediaQuery`, `themeOf` (`lib/terminal-themes` — check the actual export name against GridView's usage), `useNavigate`.
- Produces: `TerminalPreview {project: ProjectDTO; epic: EpicDTO; onOpenFull(): void}`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/board/TerminalPreview.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ listSessions: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, listSessions: h.listSessions };
});
vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: { paneId: string }) => <div data-testid="terminal" data-pane={p.paneId} />,
}));

import { TerminalPreview } from "@/components/board/TerminalPreview";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);
const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: false,
};
const epic = { id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [], stage: "implementing", attempt: 1, session: "epic-1-x", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" } as EpicDTO;

describe("TerminalPreview", () => {
  beforeEach(() => { qc.clear(); h.listSessions.mockReset(); });

  it("mounts a read-only preview of the matching session's first pane", async () => {
    h.listSessions.mockResolvedValue([
      { name: "epic-1-x", server: "h1", target: "default", cwd: "/w", command: "claude", windows: [{ id: "w1", index: "0", name: "", panes: [{ id: "pane1", command: "claude", cwd: "/w" }] }] },
    ]);
    const onOpenFull = vi.fn();
    render(<TerminalPreview project={project} epic={epic} onOpenFull={onOpenFull} />, { wrapper });
    await waitFor(() => expect(screen.getByTestId("terminal")).toHaveAttribute("data-pane", "pane1"));
    fireEvent.click(screen.getByRole("button", { name: "Open full session" }));
    expect(onOpenFull).toHaveBeenCalled();
  });

  it("shows session-ended when no session matches", async () => {
    h.listSessions.mockResolvedValue([]);
    render(<TerminalPreview project={project} epic={epic} onOpenFull={() => {}} />, { wrapper });
    await waitFor(() => expect(screen.getByText(/session ended/i)).toBeInTheDocument());
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/TerminalPreview.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/TerminalPreview.tsx`**

```tsx
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { TerminalView } from "@/components/TerminalView";
import { boardSessionsKey, listSessions } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO, Session } from "@/lib/contracts";
import { themeOf } from "@/lib/terminal-themes";
import { usePrefs } from "@/store/prefs";

// Watch-only live preview (spec §8.3): the same TerminalView the grid uses,
// with an input-blocking overlay — keystrokes require Open full session, so a
// phone scroll can't type into a runner. active={false} keeps xterm from
// stealing focus while the drawer is open.
export function TerminalPreview({ project, epic, onOpenFull }: {
  project: ProjectDTO; epic: EpicDTO; onOpenFull(): void;
}) {
  const theme = usePrefs((s) => s.terminalTheme);
  // Query the project's TARGET, not just its server — the runner session lives
  // under that socket (Finding: non-default target would show "session ended").
  const q = useQuery({
    queryKey: boardSessionsKey(project.server_id, project.target),
    queryFn: () => listSessions(project.server_id, project.target || undefined),
  });
  const session: Session | undefined = q.data?.find(
    (s) => s.name === epic.session && (project.target === "" || s.target === project.target),
  );
  const pane = session?.windows[0]?.panes[0];

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        Live session{epic.session ? <span className="ml-1 font-mono normal-case">— {epic.session}</span> : null}
      </div>
      {q.isLoading ? (
        <div className="text-xs text-muted-foreground">Looking for the session…</div>
      ) : !session || !pane ? (
        <div className="text-xs text-muted-foreground">Session ended — nothing to preview.</div>
      ) : (
        <div className="relative h-56 overflow-hidden rounded-md border border-border">
          <TerminalView serverId={project.server_id} paneId={pane.id} target={session.target} active={false}
            fontSize={11} theme={themeOf(theme)} />
          <div className="absolute inset-0 z-10" aria-hidden onClick={onOpenFull} />
          <Button size="sm" className="absolute bottom-2 right-2 z-20" onClick={onOpenFull}>
            Open full session
          </Button>
        </div>
      )}
    </section>
  );
}
```

(`themeOf` name: confirm against `GridView.tsx`'s import from `@/lib/terminal-themes`; use the actual export.)

- [ ] **Step 4: Mount + open-full-session in the drawer**

In `web/src/components/board/EpicDrawer.tsx`, replace the running mount point (`{running && null}`) with:

```tsx
          {running && epic.session && (
            <TerminalPreview project={project} epic={epic} onOpenFull={openFullSession} />
          )}
```

and add inside the component (with imports `useNavigate` from `@tanstack/react-router`, `useQueries`-free — use the two queries below, `usePanes` from `@/store/panes`, `useMediaQuery`, `listServers, serversKey, listSessions, sessionsKey` from api-client, `toast` from sonner):

```tsx
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers });
  // Pass the project's TARGET (Finding: a non-default-target project's runner
  // lives under that socket, not the agent default). Key by target too so two
  // projects on the same host under different targets don't collide in cache;
  // an empty target reuses the home screen's sessionsKey (same default list).
  const sessKey = boardSessionsKey(project.server_id, project.target);
  const sessionsQ = useQuery({ queryKey: sessKey, queryFn: () => listSessions(project.server_id, project.target || undefined) });

  // Open the runner session exactly as today's UI would (spec §8.3): desktop
  // grid tile via the pane store + home, mobile the /t terminal route.
  const openFullSession = React.useCallback(() => {
    const session = sessionsQ.data?.find(
      (s) => s.name === epic.session && (project.target === "" || s.target === project.target),
    );
    const pane = session?.windows[0]?.panes[0];
    if (!session || !pane) {
      toast.error("Session ended — nothing to attach to.");
      return;
    }
    if (isDesktop) {
      const serverName = serversQ.data?.find((s) => s.id === project.server_id)?.name ?? project.server_id;
      const res = usePanes.getState().openPane({
        serverId: project.server_id, paneId: pane.id, target: session.target,
        session: session.name, serverName, state: session.state,
      });
      if (!res.ok && res.reason === "cap") {
        toast("Close a terminal tile first (6 open max).");
        return;
      }
      usePanes.getState().focus(`${project.server_id}:${session.target}:${session.name}:${pane.id}`);
      void navigate({ to: "/" });
    } else {
      void navigate({
        to: "/t/$serverId/$paneId",
        params: { serverId: project.server_id, paneId: pane.id },
        search: { target: session.target, session: session.name },
      });
    }
  }, [sessionsQ.data, serversQ.data, isDesktop, epic.session, project, navigate]);
```

(Use `paneKey` from `@/store/panes` for the focus id instead of hand-joining if it's exported — it is: `paneKey(serverId, target, session, paneId)`.)

**Test fix (REQUIRED — Task 17 adds `useNavigate` to the drawer):** `useNavigate()` runs during render, so the Task-15 `EpicDrawer.test.tsx` (which has only `QueryClientProvider`, no router) will now throw. Add a router mock to that test file — the repo precedent is `routes/terminal.test.tsx:21`:

```tsx
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
```

Put it beside the existing `vi.mock("@/lib/use-media-query", …)` in `EpicDrawer.test.tsx`. The tests don't exercise navigation; this only satisfies the hook at render. (openFullSession's real navigation is covered by the toy-stack pass.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/TerminalPreview.test.tsx src/components/board/EpicDrawer.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/TerminalPreview.tsx web/src/components/board/TerminalPreview.test.tsx web/src/components/board/EpicDrawer.tsx
git commit -m "feat(web): live terminal preview in the drawer with open-full-session"
```

- [ ] **Step 7: CHECKPOINT 3 — STOP**

Timeline + drawer complete (both tabs, plan review, preview, all epic actions). Report tasks 13-17 status + web suite result. WAIT for review/fixes/"continue" before Task 18.

---

### Task 18: Web — ProjectHeader (run pill, controls) + shared open-session helper

**Files:**
- Create: `web/src/components/board/open-session.ts`
- Create: `web/src/components/board/ProjectHeader.tsx`
- Modify: `web/src/routes/projects.tsx` (mount ProjectHeader in the narrowed view)
- Test: `web/src/components/board/open-session.test.ts`, `web/src/components/board/ProjectHeader.test.tsx`

**Interfaces:**
- Consumes: Task 9 (`useEpicActions`, `ConfirmButton`), Task 7 (`boardStats`, `sessionSlug`), `createSession`/`sessionsKey`/`listSessions`, `usePanes`/`paneKey`, `useMediaQuery`, `useNavigate`, `queryClient`.
- Produces: `openOrFocusSession(opts, isDesktop, navigate): Promise<void>` — creates-or-opens a session and navigates to it; `ProjectHeader {project: ProjectDTO; epics: EpicDTO[]; onEdit(): void}`.

**Design note:** Task 17 hand-rolled "open an EXISTING runner session" inside the drawer. This task adds `openOrFocusSession`, which is the *create-a-new* session path (for Plan epics… and doctor-verify), keeping the two concerns separate: the drawer attaches to a session the runner already made; the header/onboarding creates one.

- [ ] **Step 1: Write the failing open-session test**

Create `web/src/components/board/open-session.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ createSession: vi.fn(), listSessions: vi.fn(), setQueryData: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, createSession: h.createSession, listSessions: h.listSessions };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { setQueryData: h.setQueryData, invalidateQueries: h.invalidateQueries } }));

import { openOrFocusSession } from "@/components/board/open-session";
import { ApiError } from "@/lib/api-client";

const session = { name: "plan-school", server: "h1", target: "default", cwd: "/w", command: "", windows: [{ id: "w1", index: "0", name: "", panes: [{ id: "pane1", command: "", cwd: "/w" }] }] };

describe("openOrFocusSession", () => {
  beforeEach(() => { h.createSession.mockReset(); h.listSessions.mockReset(); h.setQueryData.mockReset(); h.invalidateQueries.mockReset(); });

  it("mobile: creates then navigates to /t", async () => {
    h.createSession.mockResolvedValue(session);
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school", cwd: "/w", command: 'claude "/plan-epics"' }, false, navigate);
    expect(h.createSession).toHaveBeenCalledWith("h1", { name: "plan-school", cwd: "/w", command: 'claude "/plan-epics"' }, "");
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: "/t/$serverId/$paneId" }));
  });

  it("treats an existing-session 409 as success and opens the re-listed session", async () => {
    h.createSession.mockRejectedValue(new ApiError(409, "session already exists"));
    h.listSessions.mockResolvedValue([session]); // the 409 re-list finds it
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school" }, true, navigate);
    expect(h.listSessions).toHaveBeenCalledWith("h1", undefined);
    expect(navigate).toHaveBeenCalled();
  });

  it("a failed 409 re-list still navigates home rather than throwing", async () => {
    h.createSession.mockRejectedValue(new ApiError(409, "session already exists"));
    h.listSessions.mockRejectedValue(new Error("network"));
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school" }, true, navigate);
    expect(navigate).toHaveBeenCalledWith({ to: "/" });
  });
});
```

- [ ] **Step 2: Implement `web/src/components/board/open-session.ts`**

```ts
import { ApiError, createSession, listSessions, sessionsKey } from "@/lib/api-client";
import type { Session } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";
import { paneKey, usePanes } from "@/store/panes";

// `any` in PARAM position (not the return) is deliberate: under strict
// function types, TanStack's real navigate — `(opts: SpecificShape) => …` —
// is NOT assignable to `(opts: unknown) => …` (a fn taking a narrow arg
// can't stand in for one called with `unknown`). `any` disables that check so
// ProjectHeader/DoctorVerify can pass `useNavigate()`'s result verbatim.
type Navigate = (opts: any) => unknown;

interface OpenOpts {
  serverId: string; serverName: string; target: string; name: string; cwd?: string; command?: string;
}

// Create a session with an optional kickoff command and open its terminal the
// way today's UI does (spec §8/§10 — Plan epics… and doctor-verify). An
// existing-name 409 is treated as "already there": re-list and open it.
export async function openOrFocusSession(opts: OpenOpts, isDesktop: boolean, navigate: Navigate): Promise<void> {
  let session: Session | undefined;
  try {
    const body: { name: string; cwd?: string; command?: string } = { name: opts.name };
    if (opts.cwd) body.cwd = opts.cwd;
    if (opts.command) body.command = opts.command;
    session = await createSession(opts.serverId, body, opts.target);
    queryClient.setQueryData<Session[]>(sessionsKey(opts.serverId), (old) => [
      ...(old ?? []).filter((s) => !(s.name === session!.name && s.target === session!.target)),
      session!,
    ]);
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 409) throw err;
    // Already exists — find it in a fresh list. A failed re-list must NOT
    // throw out of this helper: fall through to the home navigation below so
    // the button click always resolves.
    try {
      const list = await listSessions(opts.serverId, opts.target || undefined);
      session = list.find((s) => s.name === opts.name);
    } catch {
      /* re-list failed — session stays undefined → navigate home */
    }
  }
  void queryClient.invalidateQueries({ queryKey: sessionsKey(opts.serverId) });
  const pane = session?.windows[0]?.panes[0];
  if (!session || !pane) {
    void navigate({ to: "/" }); // created but not yet observable — home will show it
    return;
  }
  if (isDesktop) {
    const res = usePanes.getState().openPane({
      serverId: opts.serverId, paneId: pane.id, target: session.target,
      session: session.name, serverName: opts.serverName, state: session.state,
    });
    if (res.ok) usePanes.getState().focus(paneKey(opts.serverId, session.target, session.name, pane.id));
    void navigate({ to: "/" });
  } else {
    void navigate({
      to: "/t/$serverId/$paneId",
      params: { serverId: opts.serverId, paneId: pane.id },
      search: { target: session.target, session: session.name },
    });
  }
}
```

- [ ] **Step 3: Write the failing ProjectHeader test**

Create `web/src/components/board/ProjectHeader.test.tsx`:

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ epicAction: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), epicAction: h.epicAction }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));

import { ProjectHeader } from "@/components/board/ProjectHeader";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 2,
  paused: false, require_ci: true,
};

describe("ProjectHeader", () => {
  beforeEach(() => { h.epicAction.mockReset().mockResolvedValue({ ok: true }); h.openOrFocusSession.mockReset(); });

  it("shows slot usage and steps max_parallel", async () => {
    const epics: EpicDTO[] = [{ ...({} as EpicDTO), id: "e1", project_id: "p1", stage: "implementing", issue: 1, title: "t", labels: [], blocked_by: [], attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" }];
    render(<ProjectHeader project={project} epics={epics} onEdit={() => {}} />);
    expect(screen.getByText(/1\/2 slots/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "increase max parallel" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "set_max_parallel", value: 3 });
  });

  it("toggles require-CI via set_require_ci", () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /CI gate/ }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "set_require_ci", on: false });
  });

  it("pause confirms, and Plan epics spawns an interactive session", async () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Pause project" }));
    fireEvent.click(screen.getByRole("button", { name: "Pause?" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "pause" });
    fireEvent.click(screen.getByRole("button", { name: "Plan epics…" }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", command: 'claude "/plan-epics"', cwd: "/w" }),
      true, h.navigate,
    );
  });

  it("Run issue parses a number or a GitHub URL", async () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Run issue…" }));
    fireEvent.change(screen.getByPlaceholderText(/issue number or URL/i), { target: { value: "https://github.com/o/r/issues/47" } });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "run_issue", issue: 47 });
  });
});
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/open-session.test.ts src/components/board/ProjectHeader.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 5: Implement `web/src/components/board/ProjectHeader.tsx`**

```tsx
import * as React from "react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { openOrFocusSession } from "@/components/board/open-session";
import { useEpicActions } from "@/hooks/useEpicActions";
import { boardStats, sessionSlug } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

const MAX_PARALLEL_CEILING = 32;

// Parse "47", "#47", or a GitHub issue/PR URL → issue number (0 = invalid).
function parseIssue(input: string): number {
  const t = input.trim();
  const m = t.match(/(?:issues|pull)\/(\d+)/) ?? t.match(/^#?(\d+)$/);
  return m ? Number(m[1]) : 0;
}

export function ProjectHeader({ project, epics, onEdit }: {
  project: ProjectDTO; epics: EpicDTO[]; onEdit(): void;
}) {
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const { act, busy } = useEpicActions(project.id);
  const [showRun, setShowRun] = React.useState(false);
  const [issue, setIssue] = React.useState("");
  const working = boardStats(epics).working;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className={cn(
        "inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-xs font-semibold",
        project.paused && "text-muted-foreground",
      )}>
        <span className={cn("size-2 rounded-full", project.paused ? "bg-zinc-500" : "bg-amber-500", !project.paused && working > 0 && "animate-pulse")} />
        {project.paused ? "Paused" : `Running · ${working}/${project.max_parallel} slot${project.max_parallel === 1 ? "" : "s"}`}
      </span>

      <span className="inline-flex items-center overflow-hidden rounded-md border border-border text-xs">
        <span className="px-2 py-1 text-muted-foreground">max parallel</span>
        <button aria-label="decrease max parallel" className="px-2 py-1 hover:bg-accent disabled:opacity-40"
          disabled={busy !== null || project.max_parallel <= 1}
          onClick={() => void act({ action: "set_max_parallel", value: project.max_parallel - 1 })}>−</button>
        <span className="bg-card px-2 py-1 font-semibold tabular-nums">{project.max_parallel}</span>
        <button aria-label="increase max parallel" className="px-2 py-1 hover:bg-accent disabled:opacity-40"
          disabled={busy !== null || project.max_parallel >= MAX_PARALLEL_CEILING}
          onClick={() => void act({ action: "set_max_parallel", value: project.max_parallel + 1 })}>+</button>
      </span>

      <Button variant="outline" size="sm" onClick={() => setShowRun((v) => !v)}>Run issue…</Button>
      <Button variant="outline" size="sm"
        onClick={() => void openOrFocusSession(
          { serverId: project.server_id, serverName: project.name, target: project.target,
            name: sessionSlug("plan", project.name), cwd: project.workdir, command: 'claude "/plan-epics"' },
          isDesktop, navigate,
        )}>
        Plan epics…
      </Button>
      {/* require-CI is action-backed (set_require_ci), not a PATCH field —
          spec §9 wants pause/max-parallel/require-CI presented together. */}
      <Button variant="outline" size="sm" disabled={busy !== null}
        title="Require CI green before the merge gate lets an epic through"
        onClick={() => void act({ action: "set_require_ci", on: !project.require_ci },
          project.require_ci ? "CI gate off" : "CI gate on")}>
        CI gate: {project.require_ci ? "on" : "off"}
      </Button>
      <Button variant="outline" size="sm" onClick={onEdit}>Edit…</Button>
      {project.paused ? (
        <Button variant="outline" size="sm" disabled={busy !== null}
          onClick={() => void act({ action: "resume" }, "Project resumed")}>Resume</Button>
      ) : (
        <ConfirmButton label="Pause project" confirmLabel="Pause?" disabled={busy !== null}
          onConfirm={() => void act({ action: "pause" }, "Project paused — running epics finish")} />
      )}

      {showRun && (
        <div className="flex w-full items-center gap-2 pt-2">
          <input
            autoFocus
            value={issue}
            onChange={(e) => setIssue(e.target.value)}
            placeholder="issue number or URL (e.g. 47 or …/issues/47)"
            className="h-8 flex-1 rounded-md border border-input bg-background px-2 text-sm"
          />
          <Button size="sm" disabled={busy !== null || parseIssue(issue) === 0}
            onClick={() => {
              const n = parseIssue(issue);
              if (n === 0) return;
              void act({ action: "run_issue", issue: n }, `Dispatched #${n}`).then((ok) => { if (ok) { setIssue(""); setShowRun(false); } });
            }}>
            Run
          </Button>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 6: Mount in the route**

In `web/src/routes/projects.tsx`, at the header mount point comment (`{/* Task 18 mounts <ProjectHeader … */}`), add:

```tsx
        {project && data?.orchestrator_enabled && (
          <ProjectHeader project={project} epics={epics} onEdit={() => setEditing(true)} />
        )}
```

Add `const [editing, setEditing] = React.useState(false);` to the shell and `import { ProjectHeader } from "@/components/board/ProjectHeader";`. (The `editing` state drives the edit sheet mounted in Task 20 — for now it's set but unread; Task 20 consumes it. To avoid an unused-var lint error in this task, also render nothing-yet: `{editing && null}`.)

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/open-session.test.ts src/components/board/ProjectHeader.test.tsx`
Expected: PASS.

- [ ] **Step 8: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/open-session.ts web/src/components/board/ProjectHeader.tsx web/src/components/board/open-session.test.ts web/src/components/board/ProjectHeader.test.tsx web/src/routes/projects.tsx
git commit -m "feat(web): project header controls (run pill, max-parallel, pause, run-issue, plan-epics)"
```

---

### Task 19: Web — ProjectForm (create) + DoctorVerify + host checklist

**Files:**
- Create: `web/src/components/board/ProjectForm.tsx`
- Modify: `web/src/routes/projects.tsx` (New-project button + form mount, zero-state CTA)
- Test: `web/src/components/board/ProjectForm.test.tsx`

**Interfaces:**
- Consumes: `createProject`/`allBoardKey` (Task 7), `listServers`/`serversKey`, `isValidSessionName`? no — reuse `github.IsValidRepo`? that's Go; use a small client repo regex; `openOrFocusSession` (Task 18) for doctor-verify, `sessionSlug` (Task 7), `useMediaQuery`, `useNavigate`, `queryClient`.
- Produces: `ProjectForm {mode: "create"; servers: ServerSummary[]; onDone(project?: ProjectDTO): void}` (edit mode added in Task 20); `DoctorVerify {project: ProjectDTO}`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/board/ProjectForm.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ createProject: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), createProject: h.createProject }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { ProjectForm } from "@/components/board/ProjectForm";
import type { ServerSummary } from "@/lib/contracts";

// listServers (Registry.List) returns ACTIVE registrations only, always
// enabled:true, with NO connectivity/health field — so the picker cannot
// distinguish "offline" and must not pretend to (Finding 10). Real
// connectivity is proven by the doctor-verify step, which fails loudly on a
// dead host.
const servers: ServerSummary[] = [
  { id: "h1", name: "aigallery", labels: [], enabled: true },
  { id: "h2", name: "carepath-dev", labels: [], enabled: true },
];

describe("ProjectForm create", () => {
  beforeEach(() => { h.createProject.mockReset(); h.openOrFocusSession.mockReset(); });

  it("validates repo and required fields before enabling submit", () => {
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    const submit = screen.getByRole("button", { name: "Register project" });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "not-a-repo" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    expect(submit).toBeDisabled(); // bad repo
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    expect(submit).toBeEnabled();
  });

  it("creates the project and shows the doctor-verify step", async () => {
    h.createProject.mockResolvedValue({ id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: ["cross-model"], max_parallel: 1, paused: false, require_ci: true });
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    fireEvent.click(screen.getByRole("button", { name: "Register project" }));
    await waitFor(() => expect(screen.getByRole("button", { name: /Run doctor/ })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Run doctor/ }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", command: "agentmon doctor", cwd: "/srv/school" }),
      true, h.navigate,
    );
  });

  it("lists every registered server as selectable", () => {
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    for (const name of ["aigallery", "carepath-dev"]) {
      const opt = screen.getByRole("option", { name: new RegExp(name) }) as HTMLOptionElement;
      expect(opt.disabled).toBe(false);
    }
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/ProjectForm.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/ProjectForm.tsx`**

```tsx
import * as React from "react";
import { useNavigate } from "@tanstack/react-router";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { openOrFocusSession } from "@/components/board/open-session";
import { sessionSlug } from "@/lib/board";
import { ApiError, allBoardKey, createProject, patchProject } from "@/lib/api-client";
import type { ProjectCreateRequest, ProjectDTO, ProjectPatchRequest, ServerSummary } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { queryClient } from "@/lib/query-client";

const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

type Mode = { mode: "create"; servers: ServerSummary[]; onDone(project?: ProjectDTO): void }
  | { mode: "edit"; project: ProjectDTO; onDone(project?: ProjectDTO): void };

export function ProjectForm(props: Mode) {
  const editing = props.mode === "edit";
  const init = editing ? props.project : undefined;
  const [name, setName] = React.useState(init?.name ?? "");
  const [repo, setRepo] = React.useState(init?.repo ?? "");
  const [serverId, setServerId] = React.useState(init?.server_id ?? (props.mode === "create" ? props.servers[0]?.id ?? "" : ""));
  const [target, setTarget] = React.useState(init?.target ?? "");
  const [workdir, setWorkdir] = React.useState(init?.workdir ?? "");
  const [baseBranch, setBaseBranch] = React.useState(init?.base_branch ?? "main");
  const [provider, setProvider] = React.useState(init?.provider ?? "claude");
  const [reviews, setReviews] = React.useState((init?.required_reviews ?? ["cross-model"]).join(", "));
  const [requireCI, setRequireCI] = React.useState(init?.require_ci ?? true);
  const [maxParallel, setMaxParallel] = React.useState(init?.max_parallel ?? 1);
  const [busy, setBusy] = React.useState(false);
  const [created, setCreated] = React.useState<ProjectDTO | null>(null);

  const repoOk = REPO_RE.test(repo.trim());
  const canSubmit = name.trim() !== "" && workdir.trim() !== "" && baseBranch.trim() !== "" &&
    (editing || (repoOk && serverId !== "")) && !busy;

  const reviewList = () => reviews.split(",").map((r) => r.trim()).filter(Boolean);

  const submit = async () => {
    setBusy(true);
    try {
      if (props.mode === "edit") {
        const body: ProjectPatchRequest = {
          name: name.trim(), workdir: workdir.trim(), target: target.trim(),
          base_branch: baseBranch.trim(), provider, required_reviews: reviewList(),
        };
        const p = await patchProject(props.project.id, body);
        void queryClient.invalidateQueries({ queryKey: allBoardKey() });
        toast("Project updated");
        props.onDone(p);
      } else {
        const body: ProjectCreateRequest = {
          name: name.trim(), repo: repo.trim(), server_id: serverId, target: target.trim() || undefined,
          workdir: workdir.trim(), base_branch: baseBranch.trim(), provider,
          required_reviews: reviewList(), max_parallel: maxParallel, require_ci: requireCI,
        };
        const p = await createProject(body);
        void queryClient.invalidateQueries({ queryKey: allBoardKey() });
        setCreated(p); // reveal the doctor-verify step
      }
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  if (created) return <DoctorVerify project={created} onDone={() => props.onDone(created)} />;

  const field = (id: string, label: string, node: React.ReactNode) => (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {node}
    </div>
  );
  const selectCls = "h-9 w-full rounded-md border border-input bg-background px-2 text-sm";

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <div className="space-y-3">
        {field("pf-name", "Name", <Input id="pf-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="school-platform" />)}
        {props.mode === "create"
          ? field("pf-repo", "Repo",
              <>
                <Input id="pf-repo" value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="owner/name" spellCheck={false} />
                {repo && !repoOk && <p className="text-xs text-destructive">Must be owner/name.</p>}
              </>)
          : field("pf-repo", "Repo (immutable)", <Input id="pf-repo" value={repo} disabled />)}
        {props.mode === "create"
          ? field("pf-server", "Host",
              <select id="pf-server" aria-label="Host" value={serverId} onChange={(e) => setServerId(e.target.value)} className={selectCls}>
                {/* listServers returns active registrations only (all
                    enabled:true, no health field) — every one is selectable;
                    doctor-verify catches a host that's actually down. */}
                {props.servers.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>)
          : field("pf-server", "Host (immutable)", <Input id="pf-server" value={init?.server_id ?? ""} disabled />)}
        {field("pf-target", "Target (optional)", <Input id="pf-target" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="agent default" />)}
        {field("pf-workdir", "Workdir", <Input id="pf-workdir" value={workdir} onChange={(e) => setWorkdir(e.target.value)} placeholder="/srv/school-platform" />)}
        {field("pf-base", "Base branch", <Input id="pf-base" value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)} />)}
        {field("pf-provider", "Default provider",
          <select id="pf-provider" aria-label="Default provider" value={provider} onChange={(e) => setProvider(e.target.value)} className={selectCls}>
            <option value="claude">Claude Code</option>
            <option value="codex">Codex</option>
          </select>)}
        {field("pf-reviews", "Required reviews", <Input id="pf-reviews" value={reviews} onChange={(e) => setReviews(e.target.value)} placeholder="cross-model" />)}
        {props.mode === "create" && (
          <>
            {field("pf-max", "Max parallel",
              <Input id="pf-max" type="number" min={1} max={32} value={maxParallel} onChange={(e) => setMaxParallel(Math.max(1, Math.min(32, Number(e.target.value) || 1)))} />)}
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={requireCI} onChange={(e) => setRequireCI(e.target.checked)} />
              Require CI green before merge
            </label>
          </>
        )}
        <div className="flex gap-2 pt-1">
          <Button size="sm" disabled={!canSubmit} onClick={() => void submit()}>
            {editing ? "Save changes" : "Register project"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => props.onDone()}>Cancel</Button>
        </div>
      </div>
      {props.mode === "create" && <HostChecklist provider={provider} />}
    </div>
  );
}

function HostChecklist({ provider }: { provider: string }) {
  const cmd = (s: string) => <code className="block overflow-x-auto rounded bg-background px-2 py-1 font-mono text-[11px]">{s}</code>;
  return (
    <div className="rounded-lg border border-border bg-card p-3 text-sm">
      <div className="font-semibold">On the host, once</div>
      <p className="mt-1 text-xs text-muted-foreground">A browser can't do these — set them up on the host, then Verify below.</p>
      <ol className="mt-2 space-y-2 text-xs">
        <li>Authenticate GitHub with push access (as the monitored OS user):{cmd("gh auth login")}</li>
        <li>Clone the repo at the workdir and set a git identity.</li>
        <li>Install the provider CLI + AgentMon hooks (existing installer).</li>
        {provider === "codex" && (
          <li>Codex: add the repo's <span className="font-mono">.git</span> to <span className="font-mono">writable_roots</span> and set <span className="font-mono">network_access = true</span> in <span className="font-mono">~/.codex/config.toml</span>; trust the hooks once interactively.</li>
        )}
      </ol>
    </div>
  );
}

export function DoctorVerify({ project, onDone }: { project: ProjectDTO; onDone(): void }) {
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  return (
    <div className="rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">Project registered — verify the host</div>
      <p className="mt-1 text-muted-foreground">
        Run the doctor inside a session on <span className="font-mono">{project.name}</span>'s host to confirm gh auth,
        the clone, hooks, and (for Codex) the sandbox config. Green means onboarding is actually done.
      </p>
      <div className="mt-3 flex gap-2">
        <Button size="sm"
          onClick={() => void openOrFocusSession(
            { serverId: project.server_id, serverName: project.name, target: project.target,
              name: sessionSlug("doctor", project.name), cwd: project.workdir, command: "agentmon doctor" },
            isDesktop, navigate,
          )}>
          Run doctor on the host ↗
        </Button>
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        Next: <span className="font-medium">Plan epics…</span> to decompose work, or label a GitHub issue{" "}
        <span className="font-mono">agentmon:run</span> to dispatch a one-off.
      </p>
    </div>
  );
}
```

- [ ] **Step 4: Mount New-project in the route**

In `web/src/routes/projects.tsx`: add `const [creating, setCreating] = React.useState(false);` and a `serversQ` (`useQuery({ queryKey: serversKey(), queryFn: listServers })`). At the All-view header mount point add a New-project button (shown when `!projectId && data?.orchestrator_enabled`):

```tsx
        {!projectId && data?.orchestrator_enabled && (
          <Button size="sm" onClick={() => setCreating(true)}>New project</Button>
        )}
```

Pass the create callback INTO `ZeroProjects` via its `onNew` prop — change the Task-12 render site `<ZeroProjects />` to `<ZeroProjects onNew={() => setCreating(true)} />` (do NOT reference `setCreating` inside `ZeroProjects`; it lives in `ProjectsShell`). Then render the form as a modal-ish panel when `creating`:

```tsx
        {creating && (
          <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 p-4" onClick={() => setCreating(false)}>
            <div className="mx-auto max-w-3xl rounded-lg border border-border bg-background p-4" onClick={(e) => e.stopPropagation()}>
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-base font-semibold">New project</h2>
                <Button variant="ghost" size="sm" onClick={() => setCreating(false)}>✕</Button>
              </div>
              <ProjectForm mode="create" servers={serversQ.data ?? []} onDone={() => setCreating(false)} />
            </div>
          </div>
        )}
```

with imports for `ProjectForm`, `listServers`, `serversKey`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/ProjectForm.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/ProjectForm.tsx web/src/components/board/ProjectForm.test.tsx web/src/routes/projects.tsx
git commit -m "feat(web): project registration form with host checklist and doctor-verify step"
```

---

### Task 20: Web — Edit + Delete project

**Files:**
- Modify: `web/src/components/board/ProjectForm.tsx` (edit mode is already built in Task 19; add the delete control)
- Create: `web/src/components/board/DeleteProject.tsx`
- Modify: `web/src/routes/projects.tsx` (mount the edit sheet on `editing`)
- Test: `web/src/components/board/DeleteProject.test.tsx`

**Interfaces:**
- Consumes: `deleteProject`/`allBoardKey`, `useNavigate`, `ApiError`, `queryClient`.
- Produces: `DeleteProject {project: ProjectDTO; onDeleted(): void; onCancel(): void}` — type-the-name confirm.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/board/DeleteProject.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ deleteProject: vi.fn(), invalidateQueries: vi.fn(), navigate: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), deleteProject: h.deleteProject }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { DeleteProject } from "@/components/board/DeleteProject";
import { ApiError } from "@/lib/api-client";
import type { ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: false,
};

describe("DeleteProject", () => {
  beforeEach(() => { h.deleteProject.mockReset(); h.invalidateQueries.mockReset(); });

  it("requires the exact name before deleting", async () => {
    h.deleteProject.mockResolvedValue({ ok: true });
    const onDeleted = vi.fn();
    render(<DeleteProject project={project} onDeleted={onDeleted} onCancel={() => {}} />);
    const del = screen.getByRole("button", { name: "Delete project" });
    expect(del).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "school" } });
    expect(del).toBeEnabled();
    fireEvent.click(del);
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
  });

  it("surfaces the 409 active-epics message", async () => {
    h.deleteProject.mockRejectedValue(new ApiError(409, "project has 2 active epics — cancel or finish them first"));
    render(<DeleteProject project={project} onDeleted={() => {}} onCancel={() => {}} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "school" } });
    fireEvent.click(screen.getByRole("button", { name: "Delete project" }));
    await waitFor(() => expect(screen.getByText(/2 active epics/)).toBeInTheDocument());
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/DeleteProject.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/components/board/DeleteProject.tsx`**

```tsx
import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError, allBoardKey, deleteProject } from "@/lib/api-client";
import type { ProjectDTO } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";

export function DeleteProject({ project, onDeleted, onCancel }: {
  project: ProjectDTO; onDeleted(): void; onCancel(): void;
}) {
  const [confirmName, setConfirmName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  const del = async () => {
    setBusy(true);
    setError("");
    try {
      await deleteProject(project.id);
      void queryClient.invalidateQueries({ queryKey: allBoardKey() });
      onDeleted();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mt-4 rounded-lg border border-destructive/40 p-3">
      <div className="text-sm font-semibold text-destructive">Delete project</div>
      <p className="mt-1 text-xs text-muted-foreground">
        Removes the project and its finished-epic history. Only possible when nothing is running.
        Type <span className="font-mono">{project.name}</span> to confirm.
      </p>
      <div className="mt-2 flex items-center gap-2">
        <Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} placeholder={project.name} aria-label="Confirm project name" />
        <Button variant="destructive" size="sm" disabled={busy || confirmName !== project.name} onClick={() => void del()}>
          Delete project
        </Button>
        <Button variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
      </div>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
  );
}
```

- [ ] **Step 4: Wire edit + delete into the route**

In `web/src/routes/projects.tsx`, replace the Task-18 `{editing && null}` with an edit sheet that hosts `ProjectForm mode="edit"` + `DeleteProject`:

```tsx
        {editing && project && (
          <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 p-4" onClick={() => setEditing(false)}>
            <div className="mx-auto max-w-3xl rounded-lg border border-border bg-background p-4" onClick={(e) => e.stopPropagation()}>
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-base font-semibold">Edit {project.name}</h2>
                <Button variant="ghost" size="sm" onClick={() => setEditing(false)}>✕</Button>
              </div>
              <ProjectForm mode="edit" project={project} onDone={() => setEditing(false)} />
              <DeleteProject project={project}
                onDeleted={() => { setEditing(false); void navigate({ to: "/projects", search: { tab: "board", epic: "" } }); }}
                onCancel={() => setEditing(false)} />
            </div>
          </div>
        )}
```

with `import { DeleteProject } from "@/components/board/DeleteProject";`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/board/DeleteProject.test.tsx src/components/board/ProjectForm.test.tsx`
Expected: PASS.

- [ ] **Step 6: Web gate + commit**

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run
cd /root/agentmon && git add web/src/components/board/DeleteProject.tsx web/src/components/board/DeleteProject.test.tsx web/src/routes/projects.tsx
git commit -m "feat(web): edit-project sheet + guarded delete with type-the-name confirm"
```

---

### Task 21: Web — service-worker epic push + deep-link navigation

**Files:**
- Create: `web/src/lib/push-payload.ts`
- Modify: `web/src/sw.ts` (handle `type:"epic"`, deep-link on click)
- Create: `web/src/lib/sw-navigate.ts`
- Modify: `web/src/main.tsx` (register the SW→router bridge)
- Test: `web/src/lib/push-payload.test.ts`

**Interfaces:**
- Consumes: nothing new from earlier tasks — `sw.ts` runs in the worker scope. The click handler posts a message the page bridges into the router.
- Produces: `push-payload.ts` (`EpicPush`, `isEpicPush`, `epicNotification`), `sw-navigate.ts` (`registerSwNavigation()`).

**Why a pure module:** `sw.ts` can't be unit-tested (worker globals), so the payload→notification and payload→URL logic lives in `push-payload.ts` (plain functions) and `sw.ts` just calls them. This mirrors how `sw.ts` already delegates to `blockedTitle`/`stateKey`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/push-payload.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { epicNotification, epicUrl, isEpicPush } from "@/lib/push-payload";

describe("push-payload", () => {
  it("recognizes epic pushes", () => {
    expect(isEpicPush({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "t", needs: "n", stage: "escalated" })).toBe(true);
    expect(isEpicPush({ type: "blocked", server: "h", target: "t", session: "s" })).toBe(false);
    expect(isEpicPush(undefined)).toBe(false);
    expect(isEpicPush({ type: "epic" })).toBe(false); // missing fields
  });

  it("builds a titled notification tagged per-epic", () => {
    const n = epicNotification({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "Curriculum", needs: "plan-gate: ready", stage: "escalated" });
    expect(n.title).toBe("Epic #16 needs you");
    expect(n.options.body).toContain("Curriculum");
    expect(n.options.tag).toBe("epic:e1");
  });

  it("deep-links into the epic drawer", () => {
    expect(epicUrl({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "t", needs: "n", stage: "stalled" }))
      .toBe("/projects/p1?tab=board&epic=e1");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/lib/push-payload.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `web/src/lib/push-payload.ts`**

```ts
// Board push payload (hubd/internal/orchestrator/push.go dispatchBoardPush) and
// its notification/URL derivations. Pure so sw.ts stays untestable-but-trivial.
export interface EpicPush {
  type: "epic";
  project: string;
  epic_id: string;
  issue: number;
  title: string;
  needs: string;
  stage: string;
}

export function isEpicPush(d: unknown): d is EpicPush {
  if (!d || typeof d !== "object") return false;
  const p = d as Record<string, unknown>;
  return p.type === "epic" && typeof p.project === "string" && typeof p.epic_id === "string" &&
    typeof p.issue === "number" && typeof p.title === "string";
}

export function epicNotification(p: EpicPush): { title: string; options: NotificationOptions } {
  return {
    title: `Epic #${p.issue} needs you`,
    options: {
      body: p.needs ? `${p.title} — ${p.needs}` : p.title,
      tag: `epic:${p.epic_id}`, // coalesce repeat pushes for the same epic
      data: p,
    },
  };
}

export function epicUrl(p: EpicPush): string {
  return `/projects/${encodeURIComponent(p.project)}?tab=board&epic=${encodeURIComponent(p.epic_id)}`;
}
```

- [ ] **Step 4: Update `web/src/sw.ts`**

Add the import and widen the push/click handlers. The `PushPayload` interface stays for the blocked path; branch on epic first.

Add near the top imports:

```ts
import { epicNotification, epicUrl, isEpicPush } from "@/lib/push-payload";
```

In the `push` handler, before the `data.type !== "blocked"` fallback, add an epic branch:

```ts
  if (isEpicPush(data)) {
    const n = epicNotification(data);
    event.waitUntil(sw.registration.showNotification(n.title, n.options));
    return;
  }
```

Replace the `notificationclick` handler with one that deep-links epic notifications while preserving the old focus-or-open behavior for everything else:

```ts
sw.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const data = event.notification.data as unknown;
  const url = isEpicPush(data) ? epicUrl(data) : "/";
  event.waitUntil(
    sw.clients.matchAll({ type: "window", includeUncontrolled: true }).then((cs) => {
      for (const c of cs) {
        if ("focus" in c) {
          // Focused client is already running the SPA — hand it the route so we
          // don't spawn a second tab. The page bridges this into the router.
          void (c as WindowClient).focus();
          if (url !== "/") (c as WindowClient).postMessage({ kind: "navigate", url });
          return;
        }
      }
      return sw.clients.openWindow(url);
    }),
  );
});
```

(`PushPayload.type` is `string`, so the existing blocked branch still compiles. Keep the `data.type !== "blocked"` generic fallback AFTER the epic branch.)

- [ ] **Step 5: Implement the page-side bridge `web/src/lib/sw-navigate.ts`**

```ts
import { router } from "@/router";

// The service worker posts {kind:"navigate", url} when a notification is tapped
// while the SPA is already open (openWindow can't focus-and-route an existing
// client). Parse the URL here and drive the in-app router so the epic drawer
// opens without a full reload.
export function registerSwNavigation(): void {
  if (typeof navigator === "undefined" || !navigator.serviceWorker) return;
  navigator.serviceWorker.addEventListener("message", (event: MessageEvent) => {
    const d = event.data as { kind?: string; url?: string } | undefined;
    if (!d || d.kind !== "navigate" || typeof d.url !== "string") return;
    // Use the low-level history API, NOT the typed router.navigate: the URL is
    // a runtime string (path + search), and router.navigate's `to`/`search`
    // are strictly typed to the route registry — a plain string won't satisfy
    // the "board"|"timeline" tab union. history.push takes a raw path and lets
    // the router match + parse search itself. Keep it same-origin only.
    try {
      const u = new URL(d.url, location.origin);
      if (u.origin !== location.origin) return;
      router.history.push(u.pathname + u.search);
    } catch {
      /* malformed URL from a push — ignore */
    }
  });
}
```

In `web/src/main.tsx`, after `registerSW({ immediate: true })` (inside the same `.then`), call the bridge — import it at top and invoke once:

```ts
  void import("virtual:pwa-register").then(({ registerSW }) => {
    registerSW({ immediate: true });
  });
  registerSwNavigation();
```

with `import { registerSwNavigation } from "@/lib/sw-navigate";`. (Guard: `registerSwNavigation` no-ops when `navigator.serviceWorker` is absent, so it's safe in every environment.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/lib/push-payload.test.ts`
Expected: PASS.

- [ ] **Step 7: Web gate + commit**

The gate includes `npm run build` for the SW path (the worker is only bundled at build time — typecheck alone won't catch a sw.ts break):

```bash
cd /root/agentmon/web && npm run typecheck && npm run test:run && npm run build
cd /root/agentmon && git add web/src/lib/push-payload.ts web/src/lib/push-payload.test.ts web/src/sw.ts web/src/lib/sw-navigate.ts web/src/main.tsx
git commit -m "feat(web): epic web-push notifications that deep-link into the escalated epic drawer"
```

---

### Task 22: Acceptance against the toy stack + CHECKPOINT 4 (final)

**Files:** none (verification task). If bugs surface, fix them in the owning task's files with a regression test, then re-run gates.

**Interfaces:** exercises the whole branch end-to-end against a real hub.

**Prerequisite context:** the toy stack lives at `/root/agentmon-toy` with a 5-epic history (`docs/superpowers/toy-repo-acceptance.md` has the restart commands, the systemd-run BindPaths for the second agent, and the `cj.txt`/`csrf.txt` session cookies). The hub there must be rebuilt from this branch's `feat/board-ui` to serve the new endpoints + SPA. This is a manual, observation-driven pass — no automated harness.

- [ ] **Step 1: Build the branch's hub + web and point the toy hub at it**

Follow `docs/superpowers/toy-repo-acceptance.md` "environment" section to (re)start the local toy hub built from `feat/board-ui`:
- `cd /root/agentmon/web && npm run build` (emits the SPA the hub serves).
- Rebuild/restart the toy hubd from this branch (the doc's systemd-run command), fresh or reusing its 5-epic SQLite.
- Confirm the second toy agent is up on its dedicated socket.

Record: hub build SHA, `orchestrator_enabled` true, board loads.

- [ ] **Step 2: All-projects + narrowed board**

- Load `/projects`: All-projects board shows the toy project's epics across the five columns; stat strip counts match; the header badge on the sessions home reflects needs-you.
- Switch to the project via the switcher; ProjectHeader controls appear; run pill shows slot usage.
- Verify a merged epic in Done, and (create one if absent) a canceled/failed epic shows compact in Done but in no stat tile.

- [ ] **Step 3: Timeline**

- Switch to Timeline: actual bars colored by stage; a running epic's bar reaches the now-line with a live edge; a queued epic shows a barless "blocked by #N" row; dependency arrows connect them; range picker (24h/7d/all) reframes.

- [ ] **Step 4: Drawer + actions (the core loop)**

Drive a fresh epic (or replay one) through the pipeline and, FROM THE BOARD:
- Open a running epic's drawer → live terminal preview renders; **Open full session** lands in the same terminal you use today (desktop grid tile / mobile /t route) and accepts typing.
- Send **guidance** from the drawer → appears in the runner session.
- Reach a **plan-gate** escalation → drawer's Plan panel renders the committed plan as markdown; **Approve plan** resumes the runner.
- Reach a **review** escalation → verdict block shows unresolved findings; **Approve & merge** merges (confirm popover); scheduler advances.
- **Retry** a stalled epic (fresh attempt) and **Cancel** a running one (modal confirm kills the session).

- [ ] **Step 5: Registration, edit, delete**

- **New project** through the form against the toy repo/host: server picker shows online/offline; submit; the doctor-verify step spawns `agentmon doctor` in a session and opens its terminal; watch the 10 checks.
- **Edit** the project (change max-parallel via header stepper + a workdir typo-fix via the edit sheet's PATCH).
- **Delete**: refused with the active-epics message while something runs; succeeds (type-the-name) once idle.
- Confirm **dormant** state text by temporarily pointing at a token-less hub (optional; the code path is unit-tested).

- [ ] **Step 6: Alerts + phone pass**

- With the app backgrounded, trigger an escalation → web-push notification "Epic #N needs you"; tapping it opens `/projects/<id>?epic=<id>` straight into the drawer.
- With the app open, an escalation raises the in-app toast with **View** → drawer; header badge increments.
- On a phone (or a narrow viewport): board defaults to the attention-first stack; Needs you is on top; toggle to Columns and back (pref persists); drawer is a full-screen sheet; approve a plan from it.

- [ ] **Step 7: Regression sweep**

Confirm the additions didn't disturb the existing app:
- Today's home, sessions, grid, mobile tabs, terminals all behave as before (the board is a separate route).
- Full gates green on the branch tip: Go gate + `cd web && npm run typecheck && npm run test:run && npm run build`.

- [ ] **Step 8: Write the acceptance note**

Append a sub-3 section to `docs/superpowers/toy-repo-acceptance.md` (or a new `docs/superpowers/board-ui-acceptance.md`): what was exercised, screenshots/notes, any deferred follow-ups, and the branch SHA validated. Commit:

```bash
git add docs/superpowers/*.md
git commit -m "docs: sub-3 board UI toy-stack acceptance results"
```

- [ ] **Step 9: CHECKPOINT 4 (final) — STOP**

Report the full acceptance result + branch state. This is the merge-decision gate: the owner runs the final whole-branch cross-model review (per the sub-2 pattern) before merging `feat/board-ui`. Do NOT merge without explicit instruction.

---

## Self-review

Ran the plan against the spec with fresh eyes:

**Spec coverage** — every spec section maps to a task:
- §3 IA / routes / dormant+zero states → Task 12.
- §4 data flow (Query + SSE invalidation, presence, live card state) → Tasks 8, 12.
- §5.1 all-board endpoint → Task 3; §5.2 plan proxy → Tasks 4, 5; §5.3 PATCH → Tasks 1, 2; §5.4 DELETE → Tasks 1, 2; §5.5 push epic_id → Task 6.
- §6 Board (columns for all 13 stages, cards, ordering, mobile stack+toggle, stats) → Tasks 7, 10, 11.
- §7 Timeline (actuals, wait-tails, arrows, range) → Tasks 13, 14.
- §8 Drawer (needs/verdict, plan review, terminal preview, actions, guidance, stages, details) → Tasks 15, 16, 17.
- §9 Registration/onboarding + edit/delete + doctor-verify → Tasks 19, 20 (+ open-session helper Task 18).
- §10 Header controls (run pill, stepper, pause, run-issue, plan-epics) + alerts/push → Tasks 18, 21.
- §11 error handling → distributed (reconnect Task 8, action errors Task 9, plan errors Task 16, edge states Tasks 12/15).
- §12 testing → each task is TDD; §12 acceptance → Task 22.
- §13 rollout (one hub rebuild, no migration) → Task 22 Step 1; no DB migration anywhere (Task 1 uses existing tables).

**Placeholder scan** — the only intentional "later task fills this" markers are the mount points in `routes/projects.tsx` (Timeline→14, drawer→15, PlanPanel→16, TerminalPreview→17, ProjectHeader→18, New project→19, edit/delete→20). Each is a compiling no-op (`&& null`) with the consuming task named in its Files list, so the branch builds green at every checkpoint. No "TBD"/"add error handling"/"similar to Task N" left.

**Type consistency** — cross-checked the registry: `EpicDTO`/`ProjectDTO` field names match the Go DTOs (`orchestrator.go:29-78`); `parseVerdict` reads the CAPITALIZED Go field names because the stored JSON is `json.Marshal` of a yaml-tagged struct (verified: `SetEpicVerdict` marshals the struct, no json tags on `Verdict`); `openPane` takes `Omit<OpenPane,"id">` and `paneKey(serverId, target, session, paneId)` — used consistently in Tasks 17, 18; `TerminalView` props (`serverId/paneId/target/active/fontSize/theme`) match `TerminalView.tsx:7`; `createSession(serverId, body, target?)` with `CreateSessionRequest{name,cwd?,command?}` — the hub now accepts `command` (sub-2 lifted the M10 rejection); `themeOf`/`audioCue`/`useMediaQuery` names verified against GridView/useAttentionAlerts.

**Corrections applied during review:**
- Task 5 fixture uses the real `SetEpicPR(ctx, id, 0, branch)` to give an epic a branch without a PR (verified signature `epics.go:179`) — not a guessed helper.
- Board query keys all share the `["board", …]` prefix so a single `invalidateQueries({queryKey:["board"]})` refreshes All + per-project boards + is what the stream fires.
- Task 21 gate adds `npm run build` because the service worker is only bundled at build time; typecheck won't catch an sw.ts regression.
- Column count is FIVE with the Done column absorbing merged+failed+canceled (the board query returns terminal epics), reconciled against the mockup's five columns.

## Cross-model plan review (Codex, 2026-07-11) — 13 findings, all confirmed & fixed

Ran `codex exec` (gpt-5.6-sol, read-only) over this whole plan against the repo before any implementation. It returned 13 findings + 5 explicit "confirmed good" notes and correctly validated the `useParams({strict:false})` fix. I adversarially validated every finding against the actual code — **all 13 were real (zero false positives this round)** — and amended the plan/spec:

1. **[hard] Task 16 plan-approval** — `Approve()` requires `PRNumber>0` ("no PR to merge"); a plan-gate epic has no PR. The runner skill resumes a plan gate on **Retry** (epic-pipeline.md:43,116-117). Fixed: PlanPanel's "Approve plan" now fires `{action:"retry"}`.
2. **[hard] Task 17 target** — `listSessions(server_id)` omitted `project.target`; a non-default-target project's runner would show "session ended". Fixed: pass target + new `boardSessionsKey(serverId, target)` to avoid cache collision (TerminalPreview + drawer).
3. **[hard] Task 17 test** — adding `useNavigate` to the drawer breaks the router-less Task-15 test. Fixed: mock `@tanstack/react-router` in `EpicDrawer.test.tsx` (repo precedent terminal.test.tsx:21).
4. **[hard] Task 18 test** — the 409 test didn't mock the re-list `listSessions`. Fixed: mock it + guard the re-list in the impl (a failed re-list navigates home, never throws) + an extra test.
5. **[hard] Task 18 TS** — `type Navigate = (opts: unknown) => unknown` rejects the real `useNavigate` under strictFunctionTypes. Fixed: `(opts: any) => unknown`.
6. **[hard] Task 12 TS** — home→`/projects` navigate omitted required `search`. Fixed: pass `{tab:"board", epic:""}`.
7. **[hard] Task 19 scope** — `ZeroProjects` referenced `setCreating` from another scope. Fixed: `onNew` prop threaded from ProjectsShell.
8. **[hard] Task 21 TS** — typed `router.navigate({to:string,search})` won't satisfy the route schema. Fixed: `router.history.push(path)` (same-origin guarded).
9. **[hard] Task 4 import** — `net/url` already imported at client.go:13; adding it duplicates. Fixed: add only `encoding/base64`.
10. **[soft] Task 19 offline servers** — `Registry.List` returns active servers only, always `enabled:true`, no health. Fixed: list all as selectable (doctor-verify is the real check); spec §9 + test corrected.
11. **[soft] edit require-CI** — `set_require_ci` had no UI. Fixed: CI-gate toggle added to ProjectHeader.
12. **[soft] detail states** — added Host/Target to the drawer Details; a GitHub-branch fallback link to PlanPanel's error state.
13. **[soft] dormant stream + visibility** — the app-wide board SSE 503-loops on a dormant hub. Fixed: events handler serves an idle empty stream when `BoardBcast==nil`; `useBoardStream` adds a `visibilitychange`→invalidate.

Confirmed-good by Codex (no change needed): all Task 1-6 Go signatures/fields (`db.Project/Epic`, `execFound`, `TransitionEpic/SetEpicPR/UpsertEpicIssue/GetEpic`, `Deps`), auth/CSRF/router/audit wiring, the GitHub JSON contents shape + base64/newline/QueryEscape handling, all questioned TS exports (`TerminalView`, `openPane/paneKey`, `request<T>` CSRF, `prefs` partialize, `themeOf/audioCue/useMediaQuery/toast`), the session-create `command` path (M10 rejection lifted), and `parseVerdict`'s capitalized-key reading.

**Verdict: the plan-review step earned its place again** — 13 real defects (7 hard compile/runtime/test blockers) caught before a single line of implementation. Plan is now execution-ready.
