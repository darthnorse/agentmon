# AgentMon Orchestrator — Sub-project 3: Board UI (design)

Date: 2026-07-11 · Status: approved by owner (section-by-section brainstorm)
Parent: `2026-07-10-agentmon-orchestrator-design.md` §8 (Board) — this spec refines
and supersedes §8 where they differ. Visual reference:
`2026-07-10-orchestrator-board-mockup.html` (interactive, fake data).
Siblings: sub-1 (hub core + GitHub sync, shipped), sub-2 (runner, shipped).

## 1. Goal & non-goals

**Goal:** a polished web view of the orchestrator — see every epic across every
project, get summoned only at decision points, and act (approve / retry / cancel /
guide / review plans) from desktop or phone. Plus a first-class project
registration & onboarding experience.

**Non-goals (v1):**
- Projected/ghost bars on the Timeline (no duration estimates exist yet; revisit
  once merged-epic history can feed them). Owner decision.
- Live CI pass/fail on cards — the hub does not store check states (the merge
  gate queries GitHub at merge time); showing cached CI would lie. The PR link is
  one tap away.
- Changing a project's `repo` after creation (existing epics belong to it — a new
  repo is a new project).
- Any change to today's interactive session UX. The board is an **addition**;
  the home screen, session list, grid, mobile tabs, and terminals are untouched.
  Owner decision (parent-spec invariant).
- Any agent-side change. Sub-3 ships web + small hub API additions only.

## 2. Decisions log (owner-approved)

| # | Decision |
|---|---|
| D1 | Hybrid navigation: `/projects` lands on an **All-projects board** (cross-project kanban/timeline); a header switcher (`All projects ▾`) narrows to `/projects/{id}` where per-project controls live. No separate cards-overview page. |
| D2 | Today's UI is unchanged; Projects is a new header link from the existing home. Addition, not replacement. |
| D3 | Full **registration UI** — form + live prerequisite awareness + host checklist + doctor-verify step. Not curl-only. |
| D4 | Include **PATCH/DELETE project** hub APIs (create-only registration would make typos unfixable from the UI — "not hacky" requirement). |
| D5 | Timeline v1 is **actuals-only**: real bars, live edge, now-line, dependency arrows, wait-tails. No projections. |
| D6 | Mobile board default is an **attention-first stacked list** (Needs you → Working → PR open → Queued → Done-collapsed) with a persisted toggle to horizontal-scroll columns. |
| D7 | Data flow is **Query-centric** (approach A): TanStack Query owns server state; the existing SSE stream seeds a badge store and *invalidates* queries on deltas (debounced ~300 ms). No client-side patching of board state. |
| D8 | Verdict facts on cards come from the stored verdict JSON; plan-gate detection is the runner-skill note convention (`needs` prefix `plan-gate:`). |

## 3. Information architecture

### Routes (all new; TanStack Router, under the existing auth layout)

- `/projects` — All-projects board. Tabs via `?tab=board|timeline` (default board).
- `/projects/{projectId}` — narrowed view, same tabs + project header controls.
- Drawer state: `?epic={epicId}` on either route — deep-linkable, survives
  reload, and is the push-notification landing target.

### Navigation

- **Projects** button in the existing home header (next to "New session"), with a
  red count badge when any epic is escalated/stalled (fed by the badge store, §4).
- Inside: breadcrumb `AgentMon / Projects / {project}` — the root crumb returns
  to today's home. Switcher lists `All projects` + each project with per-project
  needs-you counts.

### Page states (`/projects`)

1. **Dormant** (`orchestrator_enabled: false`): setup callout — "add
   `github.token` to the hub config (deploy/data/config.yaml) and restart", with
   README pointer. Registration is disabled while dormant (POST would 503).
2. **Enabled, zero projects**: short pipeline explainer + **New project** CTA.
3. **Normal**: board with **New project** in the All-projects header.

## 4. Data flow (approach A)

- **Queries** (TanStack Query, shared query-key builders like the rest of the app):
  - `GET /api/v1/orchestrator/board` (new, §5.1) — All view + switcher counts.
  - `GET /api/v1/orchestrator/projects/{id}/board` (existing) — narrowed view;
    also fetched lazily when a drawer opens (it carries per-epic events).
  - `GET …/epics/{epicId}/plan` (new, §5.2) — fetched only when the plan panel renders.
- **Stream**: one app-wide `useBoardStream` hook (mounted like the session-state
  stream) holds the `EventSource` on `GET /api/v1/orchestrator/events`:
  - `board-snapshot` → seeds a small zustand store: needs-you counts per project
    + global badge. Re-delivered on every reconnect → self-healing.
  - `board` deltas → update the badge store AND invalidate the board queries
    (debounced ~300 ms so transition bursts refetch once). Escalated/stalled
    deltas additionally raise the in-app toast (§10).
  - Reconnect: same backoff conventions as the existing session-state stream;
    PWA visibility-resume triggers the same catch-up.
- **Presence** (inherited, deliberate): an open events stream marks the principal
  present, so the hub suppresses redundant web-push while the app is open; when
  the PWA backgrounds, the stream drops and push takes over. Identical semantics
  to session alerts today.
- **Live session state on cards**: a Working epic's `session` + its project's
  `server_id`/`target` key into the existing hook-fed session-state store
  (`effectiveSessionState`) — the card pulse means the agent is *actually*
  working, and a blocked runner session shows as blocked, not amber.

## 5. Hub API additions

All under the existing authz model (`OrchestratorView` / `OrchestratorControl`),
CSRF via RequireAuth on mutating methods, audit on mutations, body caps as today
(`maxOrchestratorBody`).

### 5.1 `GET /api/v1/orchestrator/board`

Authz `OrchestratorView` on `orchestrator:*`. Returns:

```json
{ "orchestrator_enabled": true,
  "projects": [ { …projectDTO, "counts": {"queued":3,…} } ],
  "epics":    [ …epicDTO ] }
```

Assembly shares a helper with the SSE `board-snapshot` builder (one source of
truth — today's handler duplicates it inline). No per-epic events in this
payload; the drawer fetches the per-project board for those.
`orchestrator_enabled` = orchestrator constructed (github.token present).

### 5.2 `GET /api/v1/orchestrator/projects/{id}/epics/{epicId}/plan`

Authz `OrchestratorView` on `project:{id}`. Guards epic∈project exactly like the
epic-scoped actions. Resolves the plan path:

- If the epic's `needs` matches the runner-skill convention
  `plan ready at <path>` → use `<path>` after sanitizing: relative, no `..`
  segments, charset `[A-Za-z0-9._/-]`, length ≤ 512. Sanitization failure falls
  back to the default, never 500s.
- Default: `docs/plans/epic-<issue>.md`. Ref: the epic's `branch`
  (409 if the epic has no branch yet — the house user-error convention).

Fetch via a new GitHub client method `GetContents(ctx, repo, path, ref)` via the
JSON contents API (the client's `do()` is JSON-only by design — this reuses its
auth/error/status handling): decode the base64 `content` field, guarding the
response's `size` field and the decoded length against a 256 KiB cap. Responses:

- 200 `{ "path", "ref", "markdown" }` (`Cache-Control: no-store`)
- 404 → `{"error":"no plan doc found at <path> on <ref>"}` (drawer shows verbatim)
- over-cap → 413-style user error → drawer offers the GitHub link instead.

The PAT never reaches the browser; the endpoint can only ever read from the
project's registered repo.

### 5.3 `PATCH /api/v1/orchestrator/projects/{id}`

Authz `OrchestratorControl` on `project:{id}`. Editable fields: `name`,
`workdir`, `target`, `base_branch`, `provider`, `required_reviews` — same
validation as create; absent fields unchanged; `repo` is immutable (400 if
supplied). `server_id` immutable v1 (moving hosts mid-flight has session/workdir
implications; revisit if needed). `paused` / `max_parallel` / `require_ci` keep
their existing action verbs. Audited (`ProjectUpdate`).

### 5.4 `DELETE /api/v1/orchestrator/projects/{id}`

Authz `OrchestratorControl` on `project:{id}`. Refuses (409, user error naming
the count) while any non-terminal epic exists — cancel or finish them first.
On success deletes the project row + its (terminal) epics and epic events.
Audited (`ProjectDelete`). Web: type-the-name confirm.

### 5.5 Push payload: add `epic_id`

`dispatchBoardPush` already sends `{type:"epic", project, epic(issue), stage,
title, needs, ts}` — add `epic_id` so notification clicks can deep-link
`?epic=…` without a lookup. Existing sw ignores unknown types, so ordering with
the web deploy is a non-issue (hub+web ship together anyway).

## 6. Board tab

### Stage → column mapping (all 13 stages land somewhere)

| Column | Stages | Notes |
|---|---|---|
| Working | starting, planning, implementing, reviewing | live pulse from session state |
| Needs you | escalated, stalled | red accent; column + cards |
| PR open | pr_open, merging | |
| Queued | queued | |
| Done | merged · failed · canceled | merged green; failed/canceled compact + muted — visible, never hidden |

The card's stage dot/word always shows the precise stage; the column is grouping.

### Header strip

Stat tiles Merged / Working / Needs you (red-accented when >0) / PRs open /
Queued — derived client-side from the visible epics; aggregate on All,
per-project when narrowed. The Merged tile counts merged epics only —
failed/canceled appear in the Done column but in no tile.

### Card anatomy (`EpicCard`, body varies by column)

- Always: stage dot + word · live indicator (Working, from session state) ·
  provider tag (existing `ProviderTag`) · `#N title` · `repo · branch` ·
  label chips · project chip (All view only).
- **Working**: `session @ host`, started time + elapsed.
- **Needs you**: `needs` text; verdict facts (unresolved findings, tests
  passed/failed) from the stored verdict JSON; PR # when present. Inline
  actions: **Approve & merge** (primary; confirm popover — deliberate friction
  before merging from a phone) · **Retry** (confirm) · **PR↗**. Plan-gate cards
  (`needs` prefix `plan-gate:`) swap the primary to **Review plan** → opens the
  drawer's plan panel.
- **PR open**: PR #, verdict summary, merge mode (`auto-merge on green` vs
  `pr-gate — you merge`, from labels).
- **Queued**: blocked-by chips (`#13 #14`), each a link when that epic is on the
  board; paused project adds a "held — project paused" hint.
- **Done**: compact one-liners — `✓ 4h · PR #41`; failed/canceled muted with the
  stage word. Newest ~10 per project + "show all (n)" expander (hub returns up
  to 50 terminal epics per project).

### Ordering

Needs-you: oldest `stage_updated_at` first (longest-waiting on top). Working: by
`started_at`. PR open: by `stage_updated_at`. Queued: by issue number. Done:
newest first.

### Layout

Desktop: 5-column grid, min card width, horizontal scroll when narrow (mockup
pattern). Mobile (< desktop breakpoint): D6 — stacked sections in priority order,
Done collapsed; toggle (list ⇄ columns) persisted as `projectsBoardLayout` in the
existing prefs store. Stage colors per parent spec §8 (validated against the dark
surface). Reduced-motion disables pulses.

## 7. Timeline tab (actuals only)

- Left rail: `#N title`, stage chip, provider (+ project group headers on All
  view; rows sorted by start within group).
- **Bars**: from `started_at`, colored by current stage. Running epics grow to
  the **now-line** with a pulsing live edge. Terminal bars end at their last
  transition, capped `✓ 4h`.
- **Wait-tail**: escalated/stalled bars are solid to `stage_updated_at` (the
  moment they entered the waiting stage), then a red hatched tail growing to now
  — "how long has this waited on me" at a glance, DTO-only (no event fetch).
- **Queued**: row with `queued · blocked by #16` in the track, **no bar**.
- **Dependency arrows**: SVG overlay from `blocked_by`, dep-bar-end →
  dependent-bar-start when both visible; recomputed on resize/data change.
- **Window**: auto (earliest visible start → now) + range picker 24 h / 7 d /
  all; day ticks, hour ticks under 48 h; horizontal scroll when dense.
- Hover tooltip (title, stage, start → end, duration); click opens the drawer.
- All geometry (window/clamp, time→percent, ticks, arrow endpoints, tail
  fractions) in pure `web/src/lib/gantt.ts` with table-driven tests; the
  component renders its output (grid-layout.ts pattern).

## 8. Drawer

Opens via `?epic=`. Desktop: right sheet ~560 px; mobile: full-screen sheet.
Sections render only when relevant:

1. **Needs attention** (escalated/stalled): `needs` text + parsed verdict block —
   unresolved items list, findings/tests counts, `uncertain` flag.
2. **Plan review** (plan-gate): fetches §5.2, renders markdown (react-markdown +
   GFM, HTML escaped, code blocks scroll). Below: **Approve plan** + guidance
   box. NOTE (verified against the runner skill + `Orchestrator`): a plan-gate
   epic is `escalated` with NO PR, so the merge-oriented `Approve` action returns
   "no PR to merge". The runner resumes past a plan gate on **Retry** (a fresh
   session's assess-artifacts step finds the committed plan and continues), so
   "Approve plan" fires the `retry` action, not `approve`.
3. **Live session** (running epics): existing `TerminalView` as a preview with an
   input-blocking overlay — watch live; **Open full session** opens the pane as
   today (desktop grid tile via pane store / mobile `/t/…` route), resolved by
   matching the epic's session name in the server's session list. Session gone →
   "session ended" note.
4. **Actions** (contextual): Approve & merge (escalated with PR; confirm) · Retry
   (escalated/stalled; confirm — fresh attempt) · **Cancel** (any non-terminal;
   red confirm spelling out "kills the runner session, closes this attempt";
   KillSession-modal precedent) · **Send guidance** (textarea when a live session
   exists; hub types + submits it into the runner session) · PR↗ · Issue↗.
5. **Pipeline stages**: event history (from→to, source, note, ts) as the dotted
   stage list — doubles as the epic's audit trail (`user:…` vs `gate` vs agent).
6. **Details**: branch, blocked-by, host + target, attempt #, session name,
   autonomy (labels), queued/started/merged times.

Action errors: typed 409 user errors toast verbatim; infra errors toast
generically. Unresolvable `?epic=` (aged out of the terminal-50 window) → small
not-found state.

## 9. Project registration & onboarding

**New project** (All-projects header; also the zero-state CTA) — one screen, two
halves:

- **Form**: name · repo (`owner/name`, validated as the hub validates) · server
  picker from registered agents (NOTE: `listServers`/`Registry.List` returns
  active registrations only, always `enabled:true`, with no connectivity/health
  field — the picker lists every registered host as selectable and does NOT fake
  an offline state; the doctor-verify step is the real connectivity check and
  fails loudly on a dead host) · target (defaults to the agent's default) · workdir
  (absolute path hint) · base branch (default `main`) · provider (claude/codex
  with tags) · require-CI toggle · required reviews (pre-filled `cross-model`,
  the sub-2 convention) · max parallel (default 1, hub ceiling 32).
- **Host checklist** (per-provider, copy-paste commands): `gh auth login` as the
  target OS user with push access; repo cloned at workdir + git identity;
  provider CLI + AgentMon hooks installed; Codex extras (sandbox
  `writable_roots` + `network_access`, one-time interactive hook trust).
- **Verify step (post-create)**: **"Run doctor on `<host>`"** — spawns a session
  via the existing session-create-with-command (`cwd=workdir`,
  `command=agentmon doctor`, name `doctor-<project-slug>` sanitized to a valid
  session name) and opens its terminal to watch the checks live. Then a "next
  steps" pointer: Plan epics… / label an issue `agentmon:run`.

**Edit project**: same form in edit mode — PATCH fields (§5.3) plus the
action-backed knobs (pause, max-parallel, require-CI) presented together; the UI
routes each field to the right API. **Delete**: guarded, type-the-name confirm,
surfaces the 409 ("N epics still active") verbatim.

## 10. Project header controls & alerts

**Narrowed header**: run pill (`Running · 1/2 slots`, pulsing; `Paused` when
paused) · max-parallel stepper (`set_max_parallel`) · Pause/Resume (confirm) ·
**Run issue…** (dialog accepting issue number or full GitHub URL, parsed
client-side → `run_issue`) · **Plan epics…** — spawns an *interactive* session on
the project host (`cwd=workdir`, `command=claude "/plan-epics"`, name
`plan-<project-slug>`) and opens its terminal; no autonomy flags, the human
drives; Claude-only by design (plan-epics ships only in the Claude skill set).
All-projects header: just New project + switcher.

**Alerts & push**:

- Service worker: render `type:"epic"` pushes ("Epic #16 needs you —
  school-platform" + needs text), tagged per-epic to coalesce; notification
  click focuses/opens `/projects/{project}?epic={epic_id}` (extends today's
  open-"/" click handler).
- In-app: escalated/stalled deltas raise a sonner toast with **View** → drawer
  (only while the stream is open, which is exactly when push is suppressed), plus
  the header badge visible from the sessions home.

## 11. Error handling (summary)

| Failure | Handling |
|---|---|
| SSE drop / missed deltas | Backoff reconnect → fresh `board-snapshot` re-seeds store + invalidates queries; visibility-resume same. |
| Board query error | Standard error + Retry state (serversQ pattern). |
| Action 409 (typed user error) | Toast the hub's message verbatim. |
| Action infra error | Generic toast; button re-enabled; no optimistic state to roll back. |
| Plan doc missing / oversized | Friendly panel message with path/ref · GitHub link fallback. |
| Preview session gone | "Session ended" note; Open-full-session hidden. |
| Project deleted while viewing | Redirect to `/projects` + toast. |
| `?epic=` unresolvable | Drawer not-found state. |
| Dormant orchestrator | Setup callout; mutations disabled. |

## 12. Testing

- **Go (table-driven httptest)**: §5.1 (enabled flag, counts, authz); §5.2
  (membership guard, path-sanitization matrix, raw accept, size cap, 404
  mapping); §5.3 (validation, repo/server immutability); §5.4 (refusal with
  non-terminal epics, cascade); §5.5 payload.
- **Web (vitest)**: pure libs first — `lib/board.ts` (all 13 stages mapped,
  ordering, stat derivation, plan-gate detection) and `lib/gantt.ts` (window,
  ticks, bars, tails, arrows) — then fixture-state component tests: BoardView
  (columns, mobile stack + toggle), TimelineView, EpicDrawer (conditional
  sections, confirms, plan panel), project form (create/edit/delete guard),
  `useBoardStream` (seed, debounced invalidation, reconnect), sw routing helpers.
- **Acceptance**: toy stack (`/root/agentmon-toy`, 5-epic history; restart via
  the systemd-run commands in `docs/superpowers/toy-repo-acceptance.md`) — drive
  a full epic replay **from the board**: live cards, plan-gate approve from the
  drawer, retry, cancel, guidance, run-issue, register-project + doctor-verify.
  Plus a phone PWA pass including push tap → drawer.

## 13. Rollout

Web + hub API additions ship in one hub rebuild (`docker compose up -d --build`);
no agent changes, no DB migration (project/epic tables unchanged; delete uses
existing rows). The Projects button is always visible — on a dormant hub it
leads to the setup callout, which doubles as onboarding documentation.

## 14. Deferred / future

- Timeline projections fed by real merged-epic durations (D5).
- Live CI state on cards (needs hub-side check-state storage).
- `server_id` reassignment; repo change (= new project).
- Kill-on-merge session cleanup (parent-spec accepted v1 behavior).
- Guided in-UI onboarding for host-side steps beyond doctor-verify.

## 15. Component / file inventory (for the implementation plan)

- Hub: `hubd/internal/api/orchestrator.go` (+`_test`) — §5.1–5.4 handlers, snapshot
  assembly helper shared with `orchestrator_events.go`; `hubd/internal/github/client.go`
  — `GetContents`; `hubd/internal/orchestrator/push.go` — `epic_id`;
  `hubd/internal/db/projects.go` — update/delete; `router.go` routes; audit methods.
- Web: `routes/projects.tsx` (+ narrowed route) · `components/board/` —
  `BoardView`, `EpicCard`, `TimelineView`, `EpicDrawer`, `PlanPanel`,
  `ProjectHeader`, `ProjectForm`, `ProjectSwitcher` · `hooks/useBoardStream.ts` ·
  `lib/board.ts`, `lib/gantt.ts` · `store/board.ts` (badge) · `lib/api-client.ts`
  + `lib/contracts.ts` additions · `store/prefs.ts` (`projectsBoardLayout`) ·
  `sw.ts` (epic push + click routing) · home header Projects button.
- New web dependency: `react-markdown` (+ `remark-gfm`) for the plan panel.
