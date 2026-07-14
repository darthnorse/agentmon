# Two-agent split — Planning Agent / Coding Agent (v1, approach A)

**Status:** design approved (brainstorm), implementation blocked until requirements epic #3 merges (collision — both edit the runner-skill files).
**Date:** 2026-07-14

## Problem & goal

Today every epic runs on a single provider (`Project.Provider`, or a per-epic
`agent:*` label) that does *both* planning and implementation in one session. The
owner runs **two separate flat-rate subscriptions** (Claude + Codex). With
everything on one provider, that provider's usage limit is the bottleneck while
the other paid-for subscription sits idle, and a limit hit **stalls the epic**
(a stalled epic halts the pipeline).

**Goal:** let a project route the *planning* phase to one provider and the
*implementation* phase to another — e.g. plan on Claude, code on Codex — so load
spreads across both subscriptions. This is **capacity/resilience**, not spend
reduction (both fees are fixed) and not a quality play (see below).

Non-goal: making a single epic *faster*. The split may add slight handoff
overhead. Its throughput payoff comes only in combination with a higher
`max_parallel` (spreading concurrent load across both subs without limit-stalls).

## Why the quality argument does NOT justify this (and what does)

The tempting rationale — "the coder gets a free cross-model plan review" — is
already delivered by the existing pipeline: the Step 4.3 cross-model plan review
fires regardless of who implements (proven live on epic-02: 6 plan findings incl.
2 fail-opens). So the split's justification is **purely capacity arbitrage across
the two subscriptions**, which is structural and needs no measurement.

## Approach A (chosen for v1); B deferred

- **A (chosen):** split the pipeline at the existing Step 4/5 seam across the two
  *existing* runner variants. The plan review (planner's `codex exec`) and the
  code review (coder's `claude /multi-review`) already exist and are already
  cross-model — **zero new review logic**. Net new build = two provider fields +
  a hub-side auto-handoff.
- **B (deferred to v2):** fold the plan-review into the coding agent's Orient
  (drop Step 4.3), saving one Codex plan-read. Rejected for v1 because (1) it adds
  conditional logic to the runner *prompts* (where defects originate), (2) a
  dedicated reviewer is more adversarial than a coder-who-also-reviews — the
  fail-opens we've caught came from dedicated passes — and (3) it's a core-
  orchestrator first build; ship minimal-correct, then optimize. Whether B's
  saved plan-read is worth it is **empirical** — revisit once cost/time-tracking
  (#1) quantifies the extra `codex exec`.

## Architecture — reuse the plan-gate / resume seam

The runner already treats the plan→implement boundary as a clean session
boundary: state lives entirely in artifacts (the committed plan + git commits +
plan checkboxes), kickoffs are idempotent, and Step 2 is the resume path. On a
`plan-gate` today, the planning session commits+pushes the plan and **ends its
turn**; a fresh session's Step 2 finds the plan and continues at Step 5.

The split reuses this wholesale:

1. **Planning Agent** (`Provider`) runs Steps 1–4 in the *claude/native* variant:
   orient → plan → the Step 4.3 cross-model plan review → commit+push the plan →
   **stop** (the same "stop after planning" behavior plan-gate already uses).
2. **Hub auto-handoff (the only genuinely new logic):** on the "plan ready"
   signal, instead of waiting for a human retry, the hub **auto-spawns the Coding
   Agent** with `CodingProvider`.
3. **Coding Agent** (`CodingProvider`) is spawned as the *resume* session — its
   Step 2 finds the committed plan and continues from Step 5 (implement) →
   checkpoint reviews → final review → PR, in *its* provider's variant.

Everything downstream of the handoff is the existing pipeline, unchanged.

## Schema (additive, backward-compatible)

- Keep `Project.Provider` as the **planning / default** provider.
- Add optional `Project.CodingProvider`.
- **Split is "active"** iff `CodingProvider` is set and differs from the resolved
  planning provider. Empty → single-agent behavior exactly as today (no
  migration break; existing projects unaffected).
- Full traversal DB→API→contract→UI, mirroring the existing `provider` field.

## Provider resolution

`ProviderFor` becomes **phase-aware**:
- **Planning spawn** → `Provider` (per-epic `agent:*` label still overrides).
- **Coding (resume) spawn** → `CodingProvider`, falling back to `Provider` when
  unset (`agent:*` label still overrides).

## Reviews & cross-model (unchanged)

Preserved by the existing variants — the review's cross-lens is always the
*opposite* model from the coder:
- Plan reviewed by the planner's `codex exec` (Codex reviews Claude's plan).
- Code reviewed by the coder's `claude -p "/multi-review"` (Claude reviews
  Codex's code — the codex variant already does this and declares
  `cross-model` in its verdict).

No new review logic.

## Interactions

- **`plan-gate` label** stays orthogonal: if set, human approval gates the
  handoff *first* (approve → then the hub auto-spawns the coder). Split without
  `plan-gate` = fully automatic handoff.
- **`agent:*` label** on an epic still overrides both phases (per-epic escape
  hatch).
- **Retry** semantics: a retry re-runs Step 2 (assess artifacts). If the plan
  exists, retry resumes at implementation — so a retry should spawn with
  `CodingProvider` (resume = coding phase). A pre-plan failure re-spawns planning.
  *(exact rule → writing-plans.)*

## Sequencing constraint

Implementation **must land after requirements epic #3 merges** — #3 rewrites the
same runner-skill files (`epic-pipeline.md`, `plan-epics.md`, both variants) this
feature touches. Build hand-built/interactively (core-orchestrator change), and
shake out on a **fresh standalone epic**, not under the requirements run.

## Deployment requirements (surfaced live by epic-3)

The Codex-coder → Claude-review path — which this whole design leans on — had a
real gap, found the first time it ran (epic-3, first Codex coder, blocked at
checkpoint 1). Two deployment items:

- **Codex sandbox must permit the nested Claude review.** On every host that runs
  a Codex *coding* agent, `~/.codex/config.toml`
  `[sandbox_workspace_write].writable_roots` must include `~/.claude` — the codex
  variant's Steps 6–7 run `claude -p "/multi-review"`, which writes its
  session-env under `~/.claude/`. Without it the cross-model review **fails closed
  mid-epic**. Fleet-wide config update + a fresh Codex session per host.
- **`agentmon doctor` Codex-runner preflight.** Extend doctor to verify the host
  is coder-ready: `writable_roots ⊇ {repo .git dirs, ~/.claude}` and `claude` on
  PATH — so a misconfigured host is caught *before* an epic fails, not during.
  (`agent/cmd/agentmon-agent/doctor_cli.go`; agent-binary change → rides an agent
  redeploy alongside #4.)
- **README `Prepare a runner host` section** currently documents *no* Codex
  sandbox setup (`writable_roots`, `network_access`, `GOCACHE` were all configured
  by hand on this host, never written down). Add a Codex coding-agent sandbox
  subsection so a new host is set up right the first time — `writable_roots ⊇
  {repo .git dirs, ~/.claude}`, `network_access` for the test suites, `GOCACHE` to
  a writable path. Doctor (above) is the runtime check; the README is the doc.

## Out of scope (v2, gated on #1 cost/time data)

- **B** — fold the plan-review into the coder's Orient.
- **Dynamic routing** — "route to whichever subscription has headroom" (enabled
  later by #1's usage-vs-limit data). v1 is static (plan→Provider, code→CodingProvider).

## Open questions for writing-plans

1. Exact "planning agent stops after Step 4 in split mode" signal — kickoff flag
   (e.g. a plan-only kickoff) vs. the agent reading project config. Leaning
   kickoff flag (hub controls the phasing).
2. Exact "plan ready → auto-spawn coder" mechanism in the orchestrator — a new
   stage vs. reusing the plan-gate escalation signal plus a split-mode check.
3. Failure/retry matrix: handoff-spawn failure, coder failure mid-implement,
   and which provider a retry re-spawns with at each stage.
