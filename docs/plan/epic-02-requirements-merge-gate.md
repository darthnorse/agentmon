---
title: Requirements merge gate & verdict enforcement
labels: agentmon:epic, pr-gate, plan-gate
blocked-by: epic-01
---
## Scope

Make platform requirements **enforceable at the merge gate**. Extend the runner's
`Verdict` self-report with a per-requirement results block, and extend the
deterministic gate (`Decide`) so that every platform requirement declared on the
project must be reported **met** before an epic's PR can merge. Any unmet,
uncertain, or unreported platform requirement fails the gate closed (escalate),
exactly as a missing required-review does today. Wire the project's requirement
set into the gate's input.

Platform requirements (from `Project.Requirements`) **fail-close** the gate.
Epic-level requirements are **flag-only** — they surface through the existing
review / unresolved-findings path and never block via this new check.

In scope: the `Verdict` schema extension, the `Decide` logic and reason strings,
the `GateInput` plumbing in the run loop, and tests. Out of scope: the runner
actually producing the new verdict block or running check-commands (epic-03), and
storage / UI (epic-01).

## Acceptance criteria

- `Verdict` gains a `Requirements` field — a list of `{ id, status, via }` where
  `status ∈ {met, unmet, uncertain}` and `via ∈ {cmd, review}` — parsed from the
  same fenced-yaml verdict block, with the same fail-closed malformed-input
  handling as the rest of `ParseVerdict`.
- `Decide` escalates when any platform requirement id in `GateInput` is not
  present-and-`met` in the verdict; the escalation reason names the offending ids
  (unmet / uncertain / missing). It **merges** only when every platform
  requirement id is `met` and all pre-existing conditions pass.
- The existing fail-closed ordering and short-circuits are preserved
  (checks-pending → pr-gate → verdict presence → epic binding → CI → uncertainty →
  unresolved findings → tests → required reviews → **requirements**); no reordering
  that could weaken an existing gate.
- **Backward compatibility:** a project with no platform requirements behaves
  exactly as today (existing gate tests unchanged).
- The run loop passes `Project.Requirements` into `GateInput` where it already
  passes `RequiredReviews`.
- New `gate_test` cases: all-met→merge; one-unmet→escalate; uncertain→escalate;
  platform-req-absent-from-verdict→escalate; no-platform-reqs→unchanged.
- `make test` green.

## Constraints & decisions

- **Two tiers, two severities:** platform = STOP (fail-closed gate); epic-level =
  FLAG (never blocks through this check). Only `Project.Requirements` feeds the
  gate.
- **The authoritative set is `Project.Requirements`, not the epic issue body.** The
  gate iterates the project's platform requirements and demands a matching `met` in
  the verdict. Accepted v1 consequence (document it): if a platform requirement is
  added *after* epics were imported — so the runner never reported it — the gate
  fails **closed** on the missing id. That is the safe drift direction.
- **Matching is by `id`** (the stable slug from epic-01).
- **Trust model is unchanged.** The verdict is self-reported data the gate treats
  as data-not-argument (see the provenance contract in `gate.go`). A `met` backed
  by `via: cmd` is trusted the same way `tests.passed` already is — the runner is
  instructed in epic-03 to actually run the command; this epic does not attempt to
  independently re-run or authenticate it. v1 threat model = private repo, PR body
  editable only by owner + runners.
- **Verdict CAPITALIZED-JSON caveat applies** (project convention): the `Verdict`
  struct carries only yaml tags, so `json.Marshal` (used by `SetEpicVerdict`) emits
  `Requirements`; anything reading persisted verdict JSON must use the capitalized
  key, not a lowercase one.
- Generalize the `missingReviews` helper pattern rather than inventing a parallel
  mechanism.

## Pointers

- Gate: `hubd/internal/orchestrator/gate.go` (`Decide`, `GateInput`,
  `missingReviews`); gate invocation in `hubd/internal/orchestrator/orchestrator.go`
  (assembles `GateInput` next to `RequiredReviews`).
- Verdict: `hubd/internal/orchestrator/verdict.go` (`Verdict`, `ParseVerdict`).
- Depends on `Project.Requirements` from epic-01.
- Design ground truth: enforcement tiers (platform STOP / epic FLAG) and the
  honesty principle (a check-command's real exit code, not the reviewer's opinion).
