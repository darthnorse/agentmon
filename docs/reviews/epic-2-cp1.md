# Epic #2 — Checkpoint 1 Review

**Segment:** `f7bf2c0..50cb5d3` (Tasks 1–2: verdict schema + validation, gate enforcement)
**Command:** `/multi-review f7bf2c0..HEAD --codex`
**Reviewed to:** `50cb5d3`

```
═══ MULTI-REVIEW REPORT ═══
Diff: 5 files, 752 lines (242 code; 504 are docs/plans/epic-2.md) — 3 commits + 2 plan commits
Size: medium — roster: specialist + simplifier + deep-scan + security + codex(gpt-5.6-sol)
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓  codex(gpt-5.6-sol) ✓
Validation: skipped (no corrective findings — the sole finding is scheduled WIP, neither FIX nor DISCUSS)
Review-of-fixes: n/a (no fixes applied)
```

## Outcome: clean segment, no fixes applied

Three lenses (specialist, simplifier, deep-scan) returned **zero findings**. Each
independently verified the substance of the checkpoint:

- **Fail-closed parse validation** (`verdict.go` `ParseVerdict`): empty id,
  duplicate id, out-of-domain `status`, and out-of-domain/absent `via` each
  reject the whole verdict (return error → gate escalates). Verified against
  YAML edge cases (null list items, type-mismatched scalars, a mapping instead
  of a sequence) — all fail closed. No path lets a garbage/absent status or via
  masquerade as `met`.
- **`Decide` ordering** (`gate.go`): the new `unmetRequirements` check is
  dead-last, after `missingReviews` and immediately before
  `return GateResult{Merge: true}`. It moves nothing; every existing
  short-circuit still fires first. `v` is guaranteed non-nil at that point.
- **`unmetRequirements`** iterates the authoritative project set (deterministic
  order); the three-way switch (`(missing)` / `(status)` / skip) is correct;
  extra reported entries not in the project set are correctly ignored; an id
  case-mismatch fails **closed** as `(missing)` — the documented safe drift.
- **`unmetRequirements`'s dedup/enum precondition** (inputs are ParseVerdict-
  validated) holds because the single production `Decide` caller sources `v`
  from `ParseVerdict` (`orchestrator.go:631`).
- **Cross-layer:** `Requirements` (yaml tag only) marshals to the CAPITALIZED
  `Requirements` JSON key at `orchestrator.go:633`, consistent with the
  documented Verdict-JSON convention; no reader consumes it back this epic.

## Single finding — acknowledged, expected WIP (not escalated, not fixed here)

**`orchestrator.go:646-647` — platform-requirements enforcement not yet wired
into the production gate call.** Flagged by **codex (HIGH)** and the
**security lens (INFO)**. At this checkpoint the sole production `Decide` caller
builds `GateInput` without `Requirements: p.Requirements`, so `in.Requirements`
is nil and the new check is a no-op in production.

**Disposition: acknowledged, no action at this checkpoint.** This is precisely
the plan's **Task 3** (`docs/plans/epic-2.md`, Step 3: add
`Requirements: p.Requirements`), whose checkbox was still `- [ ]` when this
segment was reviewed. It is scheduled, TDD-gated work (Task 3 opens with a
failing `TestTickGateEnforcesPlatformRequirements` integration test that fails
without the wire), not a hidden defect — hence the security lens rated it INFO.
Applying it as a "review fix" here would jump the plan's sequencing and skip the
failing-first step; escalating it would surface a non-decision (the plan already
covers it). The branch is a mid-epic checkpoint and does not merge in this inert
state. **The finding is closed by Task 3, immediately following this checkpoint,
and re-verified by the final whole-branch review.**

No fail-open path exists in the delivered parse+gate logic: once the Task-3 wire
lands, no crafted `requirements:` block makes `Decide` return `Merge=true` when a
project requirement is not genuinely `met`.

**Fixed 0, 0 need discussion, 0 nitpicks, 0 refuted.**
