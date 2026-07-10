# AgentMon Orchestrator — Design

**Date:** 2026-07-10
**Status:** Approved design, pending implementation planning
**Depends on:** existing hub/agent/web (sessions, new-session spawn, lifecycle-hook state, alerts/push, kill/rename, provider tags)

## 1. Vision

Turn AgentMon from a fleet monitor into a project orchestrator: GitHub epics flow
through an autonomous pipeline (plan → implement → review → PR → merge) executed by
Claude Code or Codex sessions on fleet hosts, 24/7, with the human summoned only at
decision points. A realtime board shows what every agent is doing and surfaces the
decisions that need a human.

Primary driver: the upcoming multi-tenant UK school learning platform ("school-platform"),
built epic-by-epic from a PRD-derived implementation plan. Secondary driver: point
AgentMon at any single GitHub issue in any registered repo (maintenance apps) and have
it run the same pipeline.

### Non-goals (v1)

- Automatic provider failover on token exhaustion (manual relabel/project-default switch in v1; scheduler policy later).
- Cross-provider review policy knob (Claude runners can already `/multi-review --codex`; a formal "reviewer ≠ implementer" policy is later).
- GitHub App auth (fine-grained PAT in v1).
- Epic creation/editing UI (GitHub is the editor).
- Multi-repo epics, mobile-polished board (board is responsive/usable on mobile; optimization later).

## 2. Decisions log

| Decision | Choice |
|---|---|
| Autonomy | Every epic goes through a real PR. Hub auto-merges when review verdict is clean + CI green + no gate label. Anything murky escalates and waits. Labels dial per epic. |
| Parallelism | Dependency-graph-aware scheduler from day one; `max_parallel` config, ships at 1 (skill-tuning mode); raising it is a settings change. |
| Epic source of truth | Two-layer: `docs/plan/epic-*.md` files → imported as GitHub Issues (definition: body, labels, blocked-by). Hub DB holds runtime state (stage, session, timings) and mirrors issue metadata. Hub writes progress back as labels/comments. |
| Where the brain lives | Hub-native orchestrator (Go, in hubd) for sequencing/gates/durability; per-epic judgment stays in agent sessions (approach "B"). No long-lived foreman agent. |
| Session granularity | One fresh session per epic; the session self-pilots all stages (context hygiene via subagents inside the session; durable learnings written back to the repo). |
| Providers | Claude Code and Codex are co-equal runners behind one runner contract. Per-epic `agent:codex` label; project default otherwise. |
| Board | Two tabs over the same hub state: **Board** (attention-oriented kanban, default) and **Timeline** (Gantt: dependency arrows, now-line, projections). Observe + decide: approve/retry/pause/max-parallel/run-issue on the board; epic editing stays in GitHub. |
| Interactive use | Unchanged. Orchestrator sessions are ordinary sessions (attachable, typeable). Orchestrator never touches sessions it didn't spawn; `max_parallel` counts only its own. |
| Licensing of borrowed work | agent-orchestrator (Apache-2.0, Go) — code borrowing OK with attribution (worktree/PR/CI plumbing candidates). Aperant (AGPL-3.0) — ideas only, no code. |

## 3. Architecture

Four pieces; the first three live in existing binaries, the contract ties them together.

```
GitHub (issues, PRs, CI, webhooks)
   ▲  ▼ sync + write-back                 ┌────────────────────────────┐
┌──────────────────────────────┐  spawn   │ project host (fleet agent) │
│ hubd: orchestrator core      │──────────▶ tmux session epic-N-slug   │
│  projects / epics / events   │  WS      │  runner (Claude or Codex)  │
│  state machine + scheduler   │◀─────────│  agentmon report (local)   │
│  merge gate + escalations    │  reports └────────────────────────────┘
└──────────────────────────────┘
   ▼ WS fan-out (existing)
web SPA: Board + Timeline tabs, drawer, actions
```

### 3.1 Orchestrator core (hubd, Go package `orchestrator`)

- **projects**: registered repos — name, `owner/repo`, host, workdir, base branch,
  provider default, `max_parallel`, `paused`, webhook secret ref.
- **epics**: runtime rows — issue number, title, labels, blocked-by list, stage,
  attempt count, session name, branch, PR number, parsed verdict, needs-attention
  reason, timings. Mirrors issue metadata; GitHub remains the definition.
- **epic_events**: append-only transition log (from→to, source: report | github |
  hub | user, note). Powers the drawer's stage list and doubles as orchestrator audit
  alongside the existing audit log.
- **State machine** (§4), **scheduler** (§5), **merge gate** (§6).

No new service, queue, or DB engine: SQLite + plain Go. The merge gate is an `if`
statement, not a policy engine.

### 3.2 GitHub sync (hubd)

- Fine-grained PAT scoped to registered repos (contents, issues, pull requests).
  Stored like existing hub secrets. Never distributed to agents.
- Webhook endpoint (`/api/github/webhook`, HMAC-validated) for `issues`,
  `issue_comment`, `pull_request`, `check_suite` → update mirror → wake scheduler.
- 60s poll reconciliation as backup; the hub never trusts its mirror over GitHub.
- Write-back: stage labels (e.g. `agentmon:implementing`) + progress comments on
  transitions worth a human reading (started, PR opened, escalated, merged).

### 3.3 Runner contract + two runners (§7)

### 3.4 Board (web SPA) (§8)

## 4. State machine

```
queued → starting → planning → implementing → reviewing → pr_open → merging → merged
                        │            │            │           │
                        └────────────┴────────────┴───────────┴──→ escalated / stalled
                                                             failed (attempts exhausted) / canceled (user)
```

- `queued`: mirrored, unblocked or waiting on deps; shows blocked-by on the board.
- `starting`: session spawn dispatched; guards double-spawn (spawn is DB-state-guarded, idempotent on reconcile).
- `planning / implementing / reviewing`: reported by the runner. Optional `plan-gate`
  label: after committing the plan doc the runner pauses and the epic enters
  `escalated` with reason kind `plan-approval` (no extra state); board Approve sends
  guidance text into the live session via the existing send-keys relay and the epic
  returns to `implementing`.
- `pr_open`: reported by runner AND corroborated by the `pull_request` webhook — a
  runner that forgets to report cannot leave the board lying about PR states.
- `merging → merged`: hub action (squash-merge; `Closes #N` closes the issue) or a
  human merging in GitHub — the webhook moves the machine either way.
- `escalated`: gate declined (§6), plan-gate hold, or **runner-initiated at any stage**
  — the runner may report `escalated` with a question whenever it hits a decision it
  shouldn't make alone (ambiguous requirement, competing designs), planning included.
  The session stays alive waiting; the human answers from the board (guidance text →
  send-keys into the live session) or by attaching to the terminal. Carries a
  human-readable reason; fires existing alert/push.
- `stalled`: session died before `pr_open`, or per-stage timeout exceeded
  (configurable, e.g. planning 2h, implementing 8h, reviewing 2h). Push notification;
  board offers Retry.
- Retry = fresh session, same branch, kickoff includes "assess branch state and
  continue"; increments attempt; attempts > limit (default 2) → `failed`.
- All transitions idempotent. On hub restart every non-terminal epic reconciles
  against GitHub (branch? PR? merged?) and live sessions before the scheduler resumes.
  GitHub is truth for git/PR facts; hub DB only for pipeline bookkeeping.

## 5. Scheduling & dispatch

- **Ready** = all blocked-by issues closed/merged ∧ project not paused ∧
  orchestrator-owned running sessions < `max_parallel`.
- Trigger: any state change or sync event; plus periodic tick.
- Spawn via the existing hub→agent create-session call with the (already-defined,
  currently rejected) `Command` field carrying the provider kickoff. Runners must
  run fully autonomously — a permission prompt is a stalled epic — so kickoffs
  carry the autonomy flags: `IS_SANDBOX=1 claude --dangerously-skip-permissions
  "/epic-pipeline N"` and `codex -a never "/epic-pipeline N"`. Agent-side command
  execution ships in sub-project 2. Session named `epic-N`. Session liveness for
  stall detection is polled from the hub's state projection (vanished sessions
  emit no event).
- **Worktrees are the runner's job** (`git worktree add ../<repo>-epic-N`): moot at
  `max_parallel=1`, ready for >1. Hub stays git-ignorant.
- **Epic import** (no hub code): a `gh`-based script/skill turns `docs/plan/epic-*.md`
  (front-matter: title, labels, blocked-by) into issues. Files are the birth
  certificate; issues are the living documents.
- **Ad-hoc dispatch**: (a) `agentmon:run` label on any issue in a registered repo
  (webhook → queue; works from the GitHub mobile app); (b) "Run issue…" box on the
  board. One-offs have no deps → immediately ready; same pipeline and gate.
- **Doctor run** at project registration: a trivial dispatched session verifies
  `gh auth status`, clone + fetch of base branch, and `agentmon report` connectivity;
  result shown on the project page. Misconfigured hosts fail at setup, not mid-epic.

## 6. Merge gate (deterministic, fails closed)

Evaluated on `pr_open` once CI checks complete:

```
verdict parsed ∧ verdict.unresolved == 0 ∧ verdict.uncertain == false
∧ CI checks all green ∧ no `pr-gate` label        → squash-merge, delete branch
anything else (including missing/malformed verdict,
unknown reporting session, unparseable CI state)   → escalated + reason
```

The gate never merges on ambiguity, and an agent cannot talk its way past it — the
verdict is data the gate parses, not an argument the gate believes. From `escalated`,
board **Approve & merge** or a human merge in GitHub both work.

## 7. Runner contract

A runner session must:

1. Read the epic issue (`gh issue view N`).
2. Work on branch `epic/N-slug` (own worktree when parallel).
3. Report transitions: `agentmon report --epic N --stage <stage> [--note …]`.
   The CLI posts to a loopback-only endpoint on the local agent (mirroring the
   existing Claude-hook intake; no credentials); the agent buffers reports and the
   **hub drains them over its existing poll channel** (the hub dials agents — there
   is no agent→hub connection, so pull matches the architecture). The hub validates
   that reports for epic N come from the assigned host/session.
4. Scale process to the issue: full flow by default — plan (committed plan doc) →
   implement with subagent/TDD discipline → multi-review → fix loop. `pipeline:light`
   label skips heavy planning for small fixes. The epic issue body is the
   *requirements* (scope, acceptance criteria, constraints, PRD pointers); the runner
   writes the implementation plan at execution time against the current codebase. If
   the body already carries a detailed plan, the planning stage validates and adapts
   it instead.
   The mechanism is skills-invoking-skills: `epic-pipeline` is a skill whose
   instructions invoke the same skills used manually today (superpowers planning,
   subagent-driven implementation, `/multi-review --codex`) — no new automation
   machinery. Required skills are distributed to project hosts (or committed as
   repo-level skills) as part of sub-project 2.
   May escalate with a question at any stage (§4) — ambiguity found while planning
   is far cheaper than at review time.
5. Open a PR whose body ends with a fenced verdict block:

   ```yaml
   agentmon-verdict: v1
   epic: 15
   reviews: [specialist, simplifier, deep-scan, codex]   # gate checks required set
   findings: { found: 9, resolved: 7, unresolved: 2 }
   unresolved:
     - "Deletion cascade: guardian-linked records span tenants"
   tests: { passed: 47, failed: 0 }
   uncertain: true
   learnings_updated: true
   ```

   The full multi-review report is committed to the branch (spot-checkable evidence;
   the `reviewing` stage duration on the board is a plausibility signal). The gate
   compares `reviews` against the project's `required_reviews` config — a PR without
   proof of the required reviews escalates instead of merging.

6. Write durable learnings into `CLAUDE.md`/`AGENTS.md`/docs in the same PR
   (context is a workspace; the repo is memory).
7. Exit after reporting `pr_open`; the session ending is then normal, not `stalled`.
8. Rebase on base-branch movement; a conflict it cannot cleanly resolve is an
   escalation, never a force push.

**Claude runner**: `epic-pipeline` skill wrapping the existing superpowers flow
(brainstorm-scale planning → subagent-driven implementation → `/multi-review`).
**Codex runner**: equivalent playbook via AGENTS.md/Codex instructions (Codex ≥0.144
lifecycle hooks already feed live state). The skill is the tuning surface while
`max_parallel=1`.

### Labels (the per-epic dial)

| Label | Effect |
|---|---|
| `pr-gate` | Hub never auto-merges; PR waits for a human. |
| `plan-gate` | Runner pauses after planning for board approval. |
| `agent:codex` / `agent:claude` | Provider override for this epic. |
| `pipeline:light` | Skip heavy planning (small maintenance fixes). |
| `agentmon:run` | Dispatch trigger for ad-hoc issues. |

## 8. Board (web SPA)

New project view; state over the existing hub→web WS fan-out. Two tabs, one state.
Reference mockup: `docs/superpowers/specs/2026-07-10-orchestrator-board-mockup.html`
(open in a browser; fake data; both tabs + drawer are interactive).

- **Board tab (default)** — attention-oriented kanban:
  `Working · Needs you · PR open · Queued · Merged`. Card: stage dot + name, live
  working indicator, provider tag, `#N title`, `repo · branch`, facts
  (PR/CI/review/merge), and on escalated cards a NEEDS ATTENTION block with the
  reason and inline Approve & merge / Retry / PR↗ actions.
- **Timeline tab** — Gantt: actual bars colored by stage, live bar growing to the
  now-line, dashed projected bars for queued epics (serialized under current
  `max_parallel`), dependency arrows, escalation wait-tail.
- **Drawer** (click any card/row): stage history with timestamps, verdict block when
  escalated, live terminal preview + **Open full session** for running epics (the
  same session view used today), branch/deps/host/autonomy, PR/issue links.
  **Plan review ("plan mode"):** when an epic is escalated with reason kind
  `plan-approval`, the drawer renders the plan doc committed on the epic branch
  (via GitHub contents API) with Approve / send-guidance actions — reviewing a
  runner's plan from a phone is one tap.
- **Header**: run pill (`Running · 1/1 slot`), max-parallel stepper, Run issue…,
  Pause project. Stat strip: Merged / Working / Needs you / PRs open / Queued.
- Escalations also ride the existing M9 alerts/web-push path.
- Stage colors (validated against the dark surface, always paired with text):
  queued `#6b7280`, planning `#8b5cf6`, implementing `#d97706`, reviewing `#0284c7`,
  PR open `#3b82f6`, escalated `#ef4444`, merged `#16a34a`.

## 9. Error handling

Every failure is either *machine-retryable* or *needs you*; nothing fails silently.

| Failure | Handling |
|---|---|
| Session death / stage timeout | `stalled` → push + board Retry (fresh session, assess-and-continue). |
| Merge conflict | Runner rebases; unclean → escalate. |
| GitHub down / rate limit | Sync backs off; scheduler holds; no double-spawn (DB-guarded). |
| Provider usage limit | Session waits → stage timeout → `stalled` with reason; human relabels provider or waits. |
| Hub crash | Reconcile non-terminal epics vs GitHub + live sessions before resuming. |
| Malformed verdict / unknown reporter | Gate fails closed → escalate. |
| Runaway/wrong-direction run | Human: attach and type into the session, or kill it (→ `stalled` → Retry with guidance). |

## 10. Security

- GitHub PAT: fine-grained, only registered repos, hub-side only.
- Hosts authenticate to GitHub with their own `gh auth` (prerequisite), as today.
- Reporter: no new credentials — localhost-only CLI→agent, agent's existing WS
  identity; hub cross-checks epic↔host↔session assignment.
- All actions (spawn, merge, approve, retry, pause) behind existing authz + CSRF +
  audit log.
- Gate fails closed (§6). Escalation, not merge, is the default on any ambiguity.

## 11. Testing

- **Hub**: table-driven tests — state machine transitions (incl. idempotency +
  crash-reconcile), scheduler (graph, capacity, pause), merge gate matrix
  (verdict × CI × labels); `httptest` fake GitHub for sync/webhook/reconcile.
- **Runner contract**: stub runner script walks reporter transitions against a real
  hub in CI — proves the contract tokens-free.
- **Web**: Vitest component tests for Board/Timeline/drawer from fixture states.
- **Acceptance**: toy repo, three fake epics (auto-merge / pr-gate / forced
  escalation) end-to-end on a real host before school-platform onboards.

## 12. Host prerequisites (per project host)

- `gh` CLI installed and authenticated with push access to the project repo
  (not yet installed on most fleet hosts — one-time setup, verified by the doctor run).
- Repo clone at the project workdir; git identity configured.
- Claude Code and/or Codex ≥0.144 with AgentMon hooks (existing install flow).
- Codex hosts: `~/.codex/config.toml` `[sandbox_workspace_write]` with the project
  repo's `.git` in `writable_roots` and `network_access = true` (loopback binds for
  test suites) — without these, runner sessions cannot commit or pass test gates.
- Agent version with the localhost reporter endpoint (ships with sub-project 2).

## 13. Decomposition & order

Three sub-projects, each with its own implementation plan (writing-plans):

1. **Hub orchestrator core + GitHub sync** — schema, mirror/webhook/poll, state
   machine, scheduler, merge gate, actions API, audit. (Largest; unblocks the rest.)
2. **Runner** — agent reporter endpoint + `agentmon report` CLI, `epic-pipeline`
   Claude skill, Codex playbook, verdict format, import script, doctor run, and a
   `plan-epics` skill (interactive PRD→epics decomposition with the human: emits
   `docs/plan/epic-*.md` with front-matter + decisions, then runs the import).
3. **Board UI** — Board + Timeline tabs, drawer, actions, alerts integration.

Order 1 → 2 → 3 for integration, but the skill's *content* (prompts, pipeline
discipline) can be drafted and hand-tested against real epics at any time — every
lesson tunes the exact skill the orchestrator will run.

Rollout: deploy hub → register toy repo → doctor → 3-epic acceptance →
school-platform at `max_parallel=1` → raise parallelism when the foundation epics
have merged and the skill is trusted.
