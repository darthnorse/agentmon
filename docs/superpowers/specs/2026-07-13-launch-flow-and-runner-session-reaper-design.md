# Launch flow + runner-session reaper — design

- **Date:** 2026-07-13
- **Status:** approved (design); implementation pending
- **Branch:** `feat/launch-flow-and-reaper`

## Why

Prepping AgentMon's first real orchestration dogfood (the project-requirements
feature) surfaced a cluster of friction and one real defect in the launch/board
surface. All are small, and several change the very cockpit used to launch and
watch runners — so they are done **by hand, outside the pipeline**, before the
first dogfood, to keep that cockpit stable and keep the pipeline's first run
focused on an epic-sized feature.

Item numbers match the working labels from the design conversation. Grounding is
cited to `file:line` as of this branch's base (`507e626`).

## Scope

Three items + one relabel, one branch:

- **#1 — Plan-epics vibe input** (web): the launch button seeds `$ARGUMENTS`.
- **#2 — Board focus-trap fix** (web): a newly opened pane is never hidden.
- **#3 — Runner-session reaper + worktree teardown** (hub + agent): the meaty one.
- **Rename** (web): "CI gate" → "Require CI".

(#4 in the conversation was the CI shellcheck SC2016 fix — already shipped
separately on `fix/ci-shellcheck-installer`, not part of this branch.)

### Explicitly out of scope (deferred, by decision)

- **Helper-session (`plan-*`/`doctor-*`) reap** — no lifecycle event to hang a
  safe reap on; a heuristic idle-reaper risks killing the owner's *personal*
  tmux sessions on the same host. These are human-launched and occasional; the
  existing ⋯ → Kill handles strays. Revisit only if real usage proves them a
  nuisance, and then only name-prefix + idle scoped.
- **CI cosmetic warnings** (Node-20 action bumps, `go.sum` cache path) — ride
  along only if we touch `ci.yml`.

---

## #1 — Plan-epics vibe input (web)

### Problem

The "Plan epics…" button launches `claude --dangerously-skip-permissions
"/plan-epics"` with **no argument** (`ProjectHeader.tsx:42-44,68-75`), so
`$ARGUMENTS` is empty and the skill brainstorms from scratch — even though the
skill is *built* to be seeded (`plan-epics.md:3` `argument-hint`, `:26` reads
`$ARGUMENTS`).

### Design

A small **modal** (owner preference; the inline "Run issue…" pattern at
`ProjectHeader.tsx:107-125` is the fallback idiom) captures a free-text vibe,
then constructs the launch command with the vibe as the slash-command argument:
`… "/plan-epics <vibe>"`. Empty vibe → today's bare `"/plan-epics"`.

**Correctness (not a privilege boundary — it is the user's own host/terminal,
but a stray quote must not break the launch):** the vibe is **shell-safe quoted**
into the command string, since the command is executed by the agent via tmux →
`sh -c` (`create.go:22`). Single-quote-wrap the whole `/plan-epics <vibe>` arg
and escape embedded single quotes. This is a web-only change — `createSession`
already forwards an arbitrary `command`; no hub change.

### Testing

Command construction: empty vibe → unchanged command; a plain vibe → correctly
appended; a vibe with quotes/`$`/spaces → shell-safe, no breakage. Codex vs
Claude command variants both seed.

---

## #2 — Board focus-trap fix (web)

### Problem

When `focusedId` is set, `GridView` enters expanded (single-tile) mode and hides
**every other tile** with `display:none` (`GridView.tsx:28,64,74,86`). The
Plan-epics/Doctor buttons open via `openPaneTail`, which **calls `focus()`**
(`open-session.ts:47`), forcing that mode; the sidebar opens via `openPane`
only, which by design leaves `focusedId` as-is (`panes.ts:52`). Combined: while a
button-opened tile is expanded, sidebar clicks add **hidden** tiles with no
feedback — the reported "can't switch, must close it first."

### Design

Principle: **a newly opened pane is never hidden.**

1. **Buttons don't force-expand.** The Plan-epics/Doctor open path opens the tile
   into the **grid** (no `focus()`), matching the sidebar. A lone tile fills the
   view anyway (grid-of-1). Scope the change so the shared `openPaneTail` callers
   (board drawer/switcher, home) are unaffected — an explicit "open into grid"
   path rather than a global flip of `focus()`.
2. **Opening while expanded reveals it.** If a *new* pane is opened (sidebar or
   button) while a tile is expanded, collapse to the grid so the new tile is
   visible among the others. (Re-opening an already-open session stays a no-op,
   unchanged.)

### Testing

Button-open adds a grid tile without setting `focusedId`; opening a new pane
while expanded collapses to grid; re-opening an already-open pane is a no-op;
existing openPaneTail callers keep their behavior.

---

## #3 — Runner-session reaper + worktree teardown (hub + agent)

### Problem

Runner sessions never self-terminate on the happy path — by design. The skill
"ends its turn" after `pr_open` but `claude` stays interactive so the session
"stays attachable for follow-up" (`epic-pipeline.md:216-217`), and the hub only
kills a runner session on **Cancel/Retry** (`orchestrator.go:879,894`); the
happy path relies on a "normal exit-after-pr_open" that **does not exist**
(stale comments at `create.go:24`, `orchestrator.go:840` — fix them too). Result:
one live idle `claude` accumulates per completed epic → the board becomes
unusable after a few.

Separately, the runner's git **worktree** (`../<repo>-epic-<issue>`,
`epic-pipeline.md:82`) is never removed — not by the skill (Step 7 ends without
cleanup) nor the hub (worktree/branch names are attempt-agnostic and *reused* on
Retry, `orchestrator.go:117-119`). So worktrees also leak one-per-epic on disk.

### Design

**Trigger:** hook teardown into `finishMerged` (`orchestrator.go:721`) — the
single funnel every merge path passes through (auto-merge, human-merge-detected-
by-poll at `:610`, boot reconcile at `:1008`). When an epic transitions to
`EpicMerged`:

1. **Reap the tmux session** via the existing `killEpicSession`
   (`orchestrator.go:843`) — reuses its `retryRetire` queue for an unreachable
   agent, and already treats an already-gone session as a no-op
   (`ErrNoSession` ignored, `:856`).
2. **Tear down the worktree + branch** via a **new agent capability** (below).

**Order:** session **first**, worktree **second**. A live session's shell CWD
may sit inside the worktree, which would (correctly) block a non-forced
`worktree remove`; killing the session first frees it.

**Unchanged:** Cancel/Retry (already reap); **escalated** sessions stay alive
(the human needs them to resume/fix — escalated epics never reach `EpicMerged`,
so this reaper never touches them). Autonomous vs human-gated is irrelevant to
the rule: in autonomous mode the session simply lingers a shorter time
(pr_open → gate auto-merges → reap).

#### New agent capability: worktree teardown

The worktree lives on the agent's filesystem, created by the runner, so removal
must run agent-side. The agent already execs git (`report_cli.go:133`,
`doctor_cli.go:58,196` use a `run(dir, "git", …)` helper), so this is an
extension of existing capability, not a new dependency.

- **Endpoint:** `POST /worktrees/teardown` on the agent, beside
  `POST /sessions/kill` (`agent/cmd/agentmon-agent/main.go:99`), bearer-auth via
  the same `RequireBearer(cfg.HubToken, …)` wrapper.
- **Request:** `{ workdir, branch }` — the project's main-clone path and the
  epic's tracked branch (`e.Branch`, set from the PR head at
  `orchestrator.go:604`). We pass **branch, not a guessed path**: the handler
  resolves the worktree by branch via `git -C <workdir> worktree list --porcelain`
  (robust to the skill's naming convention drifting).
- **Handler steps (all idempotent — "already gone" is success):**
  1. `git -C <workdir> worktree list --porcelain` → find the worktree whose
     branch is `refs/heads/<branch>`; if none, skip to step 3.
  2. `git -C <workdir> worktree remove <path>` — **no `--force`**, so a dirty
     worktree (uncommitted work) is refused → log and skip, never destroy.
  3. `git -C <workdir> branch -d <branch>` — **safe delete** (`-d`, not `-D`):
     only removes a fully-merged branch; a non-merged branch is left.
  4. `git -C <workdir> worktree prune` — clears any stale administrative refs.
- **Safety:** `workdir` is validated against the agent's configured
  `session_dirs` roots (reuse the `ValidateCwd` allow-list check,
  `tmux/create.go:125`) before any git runs, so a bad/malicious `workdir` cannot
  point git at an arbitrary path. `branch` is passed as a positional git arg via
  the arg-array `run` seam (no shell interpolation).
- **Hub side:** a `registry.Client.TeardownWorktree(ctx, srv, target, workdir, branch)`
  mirroring `KillSession` (`registry/client.go:183`). A **404 from an old agent**
  (pre-endpoint) is logged and swallowed — the session reap (existing endpoint)
  still works, so a mixed fleet degrades gracefully; full teardown lands once
  agents are updated.

#### No schema change

Reaping-on-merge is correct default behavior, not a toggle — no new `Project`
field. (If a future need to keep merged sessions arises, add an opt-out then.)

#### UI interaction

None new. A reaped session surfaces through the **existing** ended-session UX
(the terminal-reconnect work: session-ended banner + `paneEnded` detection). An
open tile for a reaped runner shows "session ended" and can be closed — correct.

### Testing

- Hub: `finishMerged` calls session-kill **and** worktree-teardown; escalated /
  cancel / retry paths unaffected; a teardown 404 does not fail the merge.
- Agent: teardown handler — resolves worktree by branch; refuses a dirty
  worktree without `--force`; `-d` leaves an unmerged branch; idempotent when the
  worktree/branch are already gone; rejects an out-of-roots `workdir`.

---

## Rename — "CI gate" → "Require CI" (web)

### Problem

The toggle reads `CI gate: {on|off}` (`ProjectHeader.tsx:91`), which implies
"ignore CI" when off. It does **not** — failing CI blocks the merge regardless
(`gate.go:50`); the flag only decides whether a repo reporting *zero* checks is
treated as pending vs green (`orchestrator.go:642-643`).

### Design

Relabel the button `Require CI: {on|off}` and align the tooltip to "Wait for CI
checks before the merge gate lets an epic through (no effect on repos without
CI; failing checks always block)." Update any test asserting the old text. UI
copy only — no behavior change.

---

## Rollout

- **#1 (vibe), #2 (focus-trap), and the rename** are **web-only** (the #1 launch
  command is built client-side; `createSession` already forwards it).
- **#3 (reaper + worktree teardown)** touches hub (orchestrator) **and** agent
  (new endpoint): rebuild the hub, **then** update agents. A mixed fleet reaps
  sessions but skips worktree teardown (404-swallowed) until agents update.

## Verification gate

Go: `make test` (all 3 modules). Web: `npm run typecheck && npm run test:run`.
No service-worker touch, so `npm run build` is optional.
