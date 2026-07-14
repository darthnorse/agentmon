The validator **DEMOTED** SEC-1 (medium → low) rather than refuting it, with solid working-tree evidence: (1) the actual execution sink is **not in this diff** — verbatim `check_cmd` execution lives only in an unchecked Task-2 plan item; the current `epic-pipeline.md` has no such logic. (2) The trust boundary is barely widened vs. baseline — today's runner already treats the attacker-editable issue body as its authoritative REQUIREMENTS and runs arbitrary shell. Re-running Step 7 at low severity: it's still a genuine forward-looking design concern (Task 2 should re-validate the carrier `check_cmd` against authoritative `Project.Requirements` before executing), whose "fix" is a design decision, not a mechanical edit → stays **DISCUSS**.

**No FIX findings** → nothing to apply, no commit, no test run needed (I changed no code).

---

```
═══ MULTI-REVIEW REPORT ═══
Diff: 3 files, 457 lines (2 commits, explicit range 8a98dd6..HEAD — no working tree)
Size: medium — roster: specialist + simplifier + deep-scan + security
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓  (no --codex)
Validation: 1 finding validated: 0 confirmed, 1 demoted, 0 refuted
Review-of-fixes: n/a (no fixes applied)

✅ FIXED & COMMITTED — none
  This is a prose-only diff (two plan-epics.md provider variants + a new implementation
  plan). Deep-scan traced the full requirements carrier round-trip and found zero defects;
  the substantive items below are one design note and cosmetic plan-doc nits — nothing that
  warrants an auto-applied edit to already-committed docs.

🗣️ NEEDS DISCUSSION (1 item) — not applied
  a. [1/4] docs/plans/epic-3.md:228 (+ .../{claude,codex}/plan-epics.md carrier) —
     check_cmd relocated into the mutable issue body, executed verbatim by Task-2 (demoted: medium→low by validation)
     Reviewers: security
     Concern: The carrier copies each operator-set `check_cmd` into the GitHub issue
       body, and the Task-2 design ("run each platform check_cmd verbatim from the worktree
       root") will execute it. A GitHub issue-editor is a *wider* principal set than the
       AgentMon project-admin who set the command, and the runner by design can't reach the
       authoritative `Project.Requirements` to re-validate. Validation demoted this: the
       execution sink is NOT in this diff (unchecked Task-2 item; current epic-pipeline.md
       has no check_cmd execution), and the runner already treats the issue body as
       authoritative and runs arbitrary shell — so the widening is marginal, not a live RCE.
       Still worth a decision *now* because Task 2 is where the hardening must land.
     Reviewer's suggested fix: When Task 2 implements execution, re-validate the carrier
       id→check_cmd against a trusted source (hub-injected, or the gate runs it server-side),
       and require exactly one `### Effective requirements` section (mirroring the multi-doc
       YAML rejection). Refuse/escalate on mismatch rather than trusting the issue-body copy.

🚫 REFUTED BY VALIDATION — none

📝 NITPICKS (3 items) — cosmetic, left for you
  i.   docs/plans/epic-3.md:219 — "Verified file dispositions" table lists the two
       plan-epics.md rows at their pre-implementation sizes (127/124), but at this SHA
       `wc -l` shows 164/161 (Task 1 added +37 each). The two epic-pipeline.md rows
       (252/264) still match only because Task 2 is unrun — so the table mixes current
       and baseline counts. (Reviewers: specialist)
  ii.  docs/plans/epic-3.md:43 — the two-tier routing rule ("only platform records enter
       the structured `requirements:` verdict") is restated in ~5 places. Partly by design
       for a resumable step-by-step plan (each step is self-contained on resume); consolidating
       would arguably hurt resumability. (Reviewers: simplifier)
  iii. docs/plans/epic-3.md:144 — the plan re-embeds the `### Effective requirements`
       carrier block that Task 1 already shipped verbatim into both skill files. Harmless
       for a frozen plan artifact; finding author noted "no action needed." (Reviewers: simplifier)
```

**Fixed 0, 1 needs discussion, 3 nitpicks, 0 refuted.**

---

## Step 12 — the one discussion item

Since M = 1, let me put a concrete recommendation on the table rather than just re-listing it.

**My read:** SEC-1 is not a bug in *this* diff to fix — it's a **design constraint to carry into Task 2** (the `epic-pipeline.md` changes). The reviewed code (plan-epics writing the carrier + the plan doc) is fine as-is; the risk only becomes real when Task 2 wires up verbatim `check_cmd` execution. The validator was right to demote it.

**Recommended approach — don't touch this diff, but bake a guardrail into Task 2's plan.** Concretely, I'd add one bullet to `docs/plans/epic-3.md` Step 2.1 (the carrier-parse step) so the hardening isn't forgotten when Task 2 gets implemented:

- Require **exactly one** `### Effective requirements` section — reject/escalate on zero-or-multiple, mirroring the existing `ParseVerdict` multi-document-YAML rejection you already documented in CLAUDE.md. This closes the same smuggling class on the read side.
- State explicitly that reviewer/LLM judgment may never *alter which command runs* — the executed string comes only from the parsed carrier record, and a `check_cmd` that fails/errs records `unmet` (never silently `met`).

That's a plan-doc edit only (no runtime code, no schema change), and it's genuinely in-scope for this epic since Task 2 is the natural home. The heavier options the security lens floated (hub executes `check_cmd` server-side, or an admin-only delivery channel with id→cmd cross-check) are a real architectural question, but they'd expand epic-3's scope and likely belong in a follow-up — the gate already fails *closed* on missing/mismatched carrier data, so the residual exposure is "runner sandbox executes a command a repo-collaborator could edit," which the single-operator threat model largely absorbs.

Two questions for you:

1. Want me to add that one Step-2.1 hardening bullet to the plan now (so Task 2 inherits it), or leave the plan untouched and just track it as a review note?
2. Is "issue-editor who is not an AgentMon operator" a principal you care about for this fleet, or do issue-write and operator access effectively coincide (which is what the validator assumed)? Your answer decides whether the heavier server-side-execution option is worth a follow-up epic.

The 3 nitpicks I'd leave as-is — they're cosmetic edits to already-committed plan docs, and #ii/#iii are arguably correct as written for a resumable/frozen artifact. Say the word if you want any of them applied.
