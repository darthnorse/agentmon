# Pin projects → header chips

## Problem

Reaching a project's board takes two clicks from the home/fleet screen: the
**Projects** header button (→ `/projects`, the all-projects board), then picking
one from the `ProjectSwitcher` `<select>` (→ `/projects/$projectId`). For the
projects you actively work on, that's friction every time. Pinning should put
those projects one click away.

## Goal

Let a user **pin** a project. Pinned projects appear as quick **chips** in the
home-screen header; clicking a chip jumps straight to that project's board.

## Decisions (settled during brainstorming)

- **Storage:** hub-persisted, not per-device. AgentMon v1 is single-user, so pin
  state is a boolean attribute of the *project* itself — it then syncs across
  devices for free. This follows the existing `require_ci` feature end-to-end
  (a persisted boolean project attribute, **action-backed**, not a PATCH field).
- **Chip location:** the main shell header only (`routes/index.tsx`) — that is
  the screen where the two-step friction lives.
- **Pin control:** a toggle button on the project's board header
  (`ProjectHeader`, shown while viewing a specific project).
- **Chip badge:** each chip shows an attention (needs) count when its project has
  epics needing attention — consistent with the existing total badge on the
  **Projects** button.

## Non-goals (YAGNI)

- No per-user pins (single-user v1).
- No pin cap, no manual reordering (chips follow the project list's `ORDER BY name`).
- No pin control inside the switcher dropdown (it is a native `<select>`).
- No chips on the `/projects` page.
- No service-worker changes.

## Data flow (DB → API → contract → UI)

Per `CLAUDE.md`, a new field must traverse every layer. `require_ci` is the
reference implementation for each step.

### 1. DB — `hubd/internal/db/`

- **Migration** `migrations/0008_pinned.sql`:
  ```sql
  ALTER TABLE projects ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
  ```
- `Project` struct: add `Pinned bool`.
- `projectCols`: append `pinned`; `scanProject`: scan it into `&p.Pinned` (in the
  same column order).
- `CreateProject`: **left untouched** — new projects default to unpinned via the
  column `DEFAULT 0`. (The INSERT uses its own explicit column list, independent
  of `projectCols`, so no change is required there.)
- New method `SetProjectPinned(ctx, id string, v bool) (bool, error)` mirroring
  `SetProjectRequireCI` (returns `found` so the API can 404 an unknown id).

### 2. API — `hubd/internal/api/orchestrator.go`

- `projectDTO`: add `Pinned bool `+"`"+`json:"pinned"`+"`"+`.
- `projectOut`: pass `p.Pinned` through. This automatically covers **both**
  `/orchestrator/projects` and `/orchestrator/board` (the board query the home
  screen reads), since both build DTOs via `projectOut`.
- `OrchestratorActionsHandler`: new `case "set_pinned"` mirroring
  `set_require_ci` — call `d.DB.SetProjectPinned(ctx, id, in.On)`, 404 when not
  found. **No `d.Orch.Wake()`**: pinning is purely presentational and never
  affects scheduling. Authz (`OrchestratorControl`) and audit are already applied
  generically by the handler; `epicScoped("set_pinned")` is false, so no epic
  lookup happens (correct — the action is project-scoped).

### 3. Contract — `web/src/lib/contracts.ts`

- `ProjectDTO`: add `pinned: boolean`.

### 4. UI — `web/src/`

- **Pin toggle** in `components/board/ProjectHeader.tsx`: an outline button
  reading `☆ Pin` / `★ Pinned` that calls
  `act({ action: "set_pinned", on: !project.pinned }, project.pinned ? "Unpinned" : "Pinned")`.
  It reuses `useEpicActions`, which already invalidates `["board"]` on success —
  so the chips refresh with no extra plumbing. Disabled while `busy !== null`.
- **New component** `components/board/PinnedProjects.tsx` — small, pure, and
  presentational (styled after `ProjectSwitcher`):
  ```ts
  PinnedProjects({ projects, needs, onOpen }: {
    projects: ProjectDTO[]; needs: Map<string, number>;
    onOpen(id: string): void;
  })
  ```
  It filters `projects` to `p.pinned`, and:
  - returns `null` when there are no pinned projects (no empty row);
  - renders a horizontally-scrollable row of chip buttons (`overflow-x-auto`) so
    many pins never break the header layout;
  - each chip shows the project name plus a small count badge when
    `needs.get(p.id) > 0`;
  - clicking a chip calls `onOpen(p.id)`.
  Because it renders the live `projects` list, a deleted (or unpinned) project
  simply drops out.
- **Wire-up** in `routes/index.tsx`:
  - add `const boardQ = useQuery({ queryKey: allBoardKey(), queryFn: getAllBoard })`
    (shared `["board"]` cache key — deduped with the rest of the app);
  - read the attention store the same reactive way `routes/projects.tsx` does:
    `const attention = useBoardAttention((s) => s.attention)` then
    `const needs = React.useMemo(() => needsByProject(attention), [attention])`.
    The store is populated app-wide (`useBoardStream` is mounted in `AuthLayout`),
    so per-project counts are available on the home screen, exactly like the
    existing total badge;
  - render `<PinnedProjects>` in the header, immediately to the right of the
    "AgentMon" wordmark, with
    `onOpen={(id) => navigate({ to: "/projects/$projectId", params: { projectId: id }, search: { tab: "board", epic: "" } })}`.

## Behavior / edge cases

- Chips render on all widths, inside a scrollable container, so a long pin list
  never crowds out the header buttons.
- Orchestrator dormant, board not yet loaded, or zero pins → `PinnedProjects`
  renders nothing.
- The pin toggle only appears where `ProjectHeader` already renders — i.e. when
  `orchestrator_enabled` and a project is selected.

## Testing (TDD — test first for each unit)

- **Go**
  - `db` round-trip: `SetProjectPinned` returns `found=false` for an unknown id;
    for a real project it flips `pinned` and the new value survives `GetProject`
    and `ListProjects`.
  - API: the `set_pinned` action returns 200 and the subsequent project/board
    payload reflects `pinned: true`; an unknown project id → 404.
  - The existing migration test covers that `0008_pinned.sql` applies cleanly.
- **Web**
  - `PinnedProjects.test.tsx`: renders only pinned projects; shows the needs badge
    only when the count is > 0; `onOpen` fires with the correct project id;
    renders nothing when no project is pinned.
  - `ProjectHeader` test surface: the pin toggle is present and dispatches
    `set_pinned` with the negated `pinned` value.

## Gate

- Go: `make test`.
- Web: `cd web && npm run typecheck && npm run test:run`.
- `npm run build` is **not** required (no `web/src/sw.ts` change).

## Commit

Conventional prefixes; no `Co-Authored-By` trailer. Land on the current branch
`feat/pins-superpowers` (no PR required).
