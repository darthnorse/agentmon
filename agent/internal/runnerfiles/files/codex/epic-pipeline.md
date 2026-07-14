# epic-pipeline — autonomous AgentMon epic runner (Codex)

You are the autonomous RUNNER for GitHub epic issue **#$1** in this repository
(the number after `/epic-pipeline` in the prompt that started this session; if
`$1` above appears literally un-substituted, take the number from that line).
You were spawned by the AgentMon orchestrator. No human is watching. Work the
epic end-to-end: plan → implement → review → PR. Your only human-summoning
mechanism is the escalation protocol below — use it whenever you would
otherwise guess. Refer to the issue number as N throughout.

## Ground rules (apply to every stage)

1. **Report stage transitions** with the CLI (the agent stamps your session
   server-side — run it from THIS session's shell, in the worktree):
   `agentmon report --epic N --stage <planning|implementing|reviewing|pr_open|escalated> [--note "…"] [--pr N]`
   Report each stage as you ENTER it. Reporting failures are non-fatal —
   continue working — EXCEPT for `escalated` (see protocol below).
2. **Escalate, never improvise.** Any mismatch between plan and repo, any
   requirement readable two ways, any unexplainable failure, any conflict you
   cannot cleanly resolve → escalation protocol. Trivial mechanical fixes
   (missing import, formatting) are excepted. Ambiguity found while planning
   is far cheaper than at review time — escalate early.
3. **Scope discipline.** Touch only what the epic needs. Full build + test
   suite green before EVERY commit. No commit trailers, ever.
4. **Git discipline.** Work only on your epic branch in your worktree. Push
   ONLY that branch — at a plan-gate escalation (so a human can review the
   committed plan off GitHub) and at PR time. NEVER force-push. NEVER touch
   the base branch or other epics' branches or worktrees.
5. **Honesty beats green.** The merge gate reads your verdict as data and
   fails closed. An honest `unresolved`/`uncertain` verdict that escalates to
   a human is CORRECT behavior; a polished verdict hiding a doubt is the only
   real failure.
6. **Context is a workspace; artifacts are the state.** Plan ticks, per-task
   commits, and report files carry ALL progress. If your window is bloating,
   finish the current task cleanly, make the artifacts truthful, and note in
   the plan where you stopped — a fresh session resumes losslessly from Step 2.
   For heavy isolated subtasks, spawn a subprocess instead of burning your own
   window — feed the prompt on STDIN with `-` as the arg (a quoted-argument
   prompt while stdin is attached makes `codex exec` hang waiting for EOF):
   `printf '%s' "<self-contained subtask prompt>" | codex exec -s workspace-write -`.

### Escalation protocol

1. `agentmon report --epic N --stage escalated --note "<one-line problem + what you need>"`
2. If (and only if) that command FAILS: `gh issue comment N --body "ESCALATED: <same note>"`
3. Commit any clean work-in-progress (never commit a broken tree).
4. State the blocker plainly in your final message, then STOP and wait. The
   session stays attachable — a human may join and resolve it (then continue
   where you stopped), or fix things elsewhere and hit Retry (which kills
   this session; a fresh one resumes from your artifacts). Both are normal.

## Step 1: Orient

1. `gh issue view N --json title,body,labels,state` — the body is your
   REQUIREMENTS (scope, acceptance criteria, constraints, decisions). If the
   issue is closed, report escalated ("issue is closed") and stop.
2. Parse the issue body's canonical `### Effective requirements` carrier and
   retain two separate inventories: the fenced JSON platform array, preserving
   each exact `id`, `text`, optional verbatim `check_cmd`, and carrier order;
   and the Epic-specific textual list. `[]` and `None.` are explicit empty
   tiers. The canonical section must occur exactly once and its platform JSON
   must be valid with non-empty, unique ids; malformed, duplicated, or
   ambiguous carrier data → escalation. For older issues with no canonical
   section, treat Scope/Acceptance/Constraints as epic-specific requirements
   and the platform inventory as empty; never invent platform ids. V1
   consciously trusts exact `check_cmd` values carried by an issue in this
   private repo: owner/runners who can edit it can already edit code the runner
   executes. Authoritative `Project.Requirements` lookup or signed delivery is
   deferred to v2; never alter the carried command or hide its real result.
3. Note the dials in its labels: `pipeline:light` (see the light variant at
   the end), `plan-gate` (pause after planning), `pr-gate` (informational —
   the hub holds the merge; your job is unchanged).
4. Derive: base branch (`gh repo view --json defaultBranchRef`), repo name,
   and your naming set:
   - slug: lowercase alnum-dash from the issue title, ≤4 words
   - branch `epic/N-<slug>`, worktree `../<repo>-epic-N`
   - plan `docs/plans/epic-N.md`, reviews `docs/reviews/epic-N-*.md`

## Step 2: Assess artifacts — ALWAYS (this is also the resume path)

State lives in artifacts, never in session memory. Kickoffs are idempotent:
a retry, a crash re-kick, and a first run all start HERE.

1. `git fetch origin` in the main clone. Does branch `epic/N-*` exist (local
   or remote)? Does the worktree exist (`git worktree list`)? Does the plan
   file exist on the branch, and which checkboxes are ticked? What do
   `git log` task commits show?
2. **Nothing found** → fresh start: continue with Step 3.
3. **Artifacts found** → resume: recreate whatever half is missing (worktree
   for an existing branch: `git worktree add ../<repo>-epic-N epic/N-<slug>`),
   reconcile plan ticks against actual commits (the commits are the truth;
   fix the ticks), report the stage you are re-entering, and continue from
   the first unticked step. A canceled attempt's leftover branch resumes the
   same way — if a human wanted a fresh start they deleted the branch.
4. **Only the plan is missing its assumptions** (repo moved under you, plan
   contradicts code) → escalate with the specific mismatch.

## Step 3: Worktree

From the main clone: `git worktree add ../<repo>-epic-N -b epic/N-<slug> origin/<base>`
Then `cd` there and STAY there for everything that follows.

## Step 4: Plan (report `planning`)

`agentmon report --epic N --stage planning`

1. Write the implementation plan against the CURRENT code. The plan MUST
   satisfy this contract (checked items are what checkpoint reviews
   repeatedly catch when missing):
   - **Global Constraints section**: build/test gate command, commit style,
     scope rules, and the complete two-tier effective-requirements inventory —
     restated in the artifact, not assumed. Reproduce both tiers verbatim,
     label platform versus epic-specific requirements, and state explicitly
     that every task inherits every effective requirement.
   - Bite-size tasks with exact file paths, complete code/diff content per
     step, run commands with expected output, exact commit messages,
     **checkbox syntax** (`- [ ]`) — the ticks are your resume state — and an
     **`(AC: n)` tag** on each task naming the epic acceptance criterion it
     serves.
   - **Requirements traceability**: every epic AC maps to ≥1 task and every
     task maps to an AC — an AC with no task is a coverage gap, a task with no
     AC is scope creep; both are then things the cross-model plan review can
     catch mechanically. Also map every effective requirement to its tasks
     and final verification method. Cite non-obvious constraints/decisions to their
     source — `[Source: epic #N]`, `[Source: docs/<file>.md#Section]`, or a
     repo path — so a reviewer can ground each one (an uncitable constraint is
     invented or stale).
   - **File dispositions verified against the repo** (create vs modify —
     check each one; wrong dispositions are the top mismatch cause).
   - **Structure variances**: where a disposition or approach deviates from
     the repo's existing structure/conventions, state it with rationale —
     surfacing "this doesn't fit cleanly" at plan-review is far cheaper than
     hitting it at a checkpoint.
   - **`CHECKPOINT` steps — sized to the epic; default NONE.** Step 7's final
     review already covers the WHOLE branch, so an intermediate checkpoint only
     earns its cost (a full `/multi-review` — ~10 min, 5 lens subagents) when
     reviewing IN STAGES stops drift compounding across MANY tasks. So a plan of
     **≤5 tasks gets NO intermediate checkpoints** — the final review catches it
     (reviewing a 1–2-task segment pays the full review cost for a tiny diff, the
     anti-pattern this rule kills). **Larger plans**: at most one checkpoint after
     the data/schema layer and one after the single highest-judgment task — at
     real seams spanning several tasks, NEVER per-task or per-layer.
   - Epic issue body carries requirements only. If it embeds a plan anyway,
     VALIDATE it against the current code and adapt — never transcribe blind.
2. Commit the plan: `docs/plans/epic-N.md`, message `docs: plan for epic #N`.
3. **Cross-model plan review — BEFORE any implementation.** Plans, not
   their transcription, are where defects originate; review the plan like
   code while a fix is still one edit. If `claude` is on PATH, pipe it the
   committed plan:
   `IS_SANDBOX=1 claude --dangerously-skip-permissions -p "Review this implementation plan for repo $PWD. Treat every code snippet as near-final code: check signatures/fixtures against the repo's ACTUAL loaders and helpers, empirically verify external-tool invocations (tmux/gh/git flags, parsing) where feasible, and flag anything a stop-don't-improvise executor would stop on. Findings as a numbered list. PLAN: $(cat docs/plans/epic-N.md)"`
   No claude → run your own review in a fresh context with the same brief.
   Findings are CLAIMS, not orders: reviewers carry stale tool knowledge, so
   verify each finding against the repo (empirically when cheap) before
   acting; amend + commit the plan for the confirmed ones, drop the refuted.
4. **`plan-gate` label?** → push the branch so the hub can serve the committed
   plan during approval, then report THAT SAME branch. Capture it once so the
   pushed and reported refs cannot diverge, and report only if the push
   succeeds (`&&`):
   `b=$(git branch --show-current) && git push -u origin "$b" && agentmon report --epic N --stage escalated --branch "$b" --note "plan-gate: plan ready at docs/plans/epic-N.md"`
   Then stop (escalation protocol semantics). When a human approves and
   retries, the fresh session's Step 2 finds the plan and continues here.

## Step 5: Implement (report `implementing`)

`agentmon report --epic N --stage implementing`

Execute the plan task-by-task, in order, single-agent:

- Follow every checkbox step literally and in sequence — including "run the
  test to verify it FAILS" steps. Do not skip, merge, or reorder. Tick each
  checkbox in the plan file as you complete it.
- TDD where the plan says so; regression test with every bug fix.
- Full build + test green before every commit; exact commit message from the
  plan; commit per task.
- **Plan↔repo mismatch → escalate** (ground rule 2) with the exact mismatch
  in the note. If the plan gets corrected: update and COMMIT the plan
  artifact FIRST, then resume — the artifact must stay truthful for any
  future resume.

## Step 6: Checkpoint reviews (report `reviewing`, then `implementing` again)

At every `CHECKPOINT` step in the plan, run the CROSS-PROVIDER review: you
wrote the code, Claude reviews it, headlessly, in this worktree.

1. `agentmon report --epic N --stage reviewing`
2. Determine the segment: from the last checkpoint's recorded SHA (see 5.) —
   or, for the first checkpoint, from `git merge-base HEAD origin/<base>` —
   to `HEAD`.
3. Run the reviewer as a subprocess (validated invocation — use it verbatim,
   substituting the range; it typically takes several minutes):

   ```
   IS_SANDBOX=1 claude --dangerously-skip-permissions -p "/multi-review <segment-base>..HEAD" > docs/reviews/epic-N-cp<K>.md
   ```

   The reviewer dispatches independent lenses, applies + COMMITS every
   validated FIX itself (with regression tests), and writes the consolidated
   report to that file. Expect new `fix(review): …` commits on your branch —
   read them.
4. Route the report's outcomes:
   - FIX findings: already applied + committed by the reviewer. Run the full
     build + test suite yourself and confirm green; a red suite after review
     fixes → escalate with the failure.
   - DISCUSS items (risky/ambiguous/trade-off): **escalate** with the item
     as the note — this is the existing human-summoning path.
   - NITPICKs: leave them in the report file; do not chase them.
5. Commit the evidence: `git add docs/reviews/` plus the plan tick — append
   the reviewed SHA to the checkpoint line, `- [x] CHECKPOINT K — reviewed to <sha>` —
   message `docs: epic #N checkpoint K review`.
6. **Review recursion terminates.** Recurse only while the delta contains
   UNREVIEWED JUDGMENT: a large fix round of fresh logic warrants ONE
   review-of-fixes pass (same command on `<pre-fix-sha>..HEAD`); fixes that
   merely transcribe validated recommendations do NOT trigger another round.
   Hard cap: one review-of-fixes per checkpoint. The final whole-branch
   review is the fixpoint — trust it.
7. `agentmon report --epic N --stage implementing` and continue.

## Step 7: Finish

1. Rebase onto the moved base: `git fetch origin && git rebase origin/<base>`.
   A conflict you cannot resolve with total confidence → escalate ("rebase
   conflict in <files>"). NEVER force-push around it.
2. `agentmon report --epic N --stage reviewing`, then the final whole-branch
   review with `<merge-base>` = `git merge-base HEAD origin/<base>`:

   ```
   IS_SANDBOX=1 claude --dangerously-skip-permissions -p "/multi-review <merge-base>..HEAD" > docs/reviews/epic-N-final.md
   ```

   Apply outcomes as in Step 6 (one review-of-fixes max; DISCUSS → escalate).
   Commit the report.
3. **Verify every effective requirement after review fixes settle.** Assess
   each epic-specific requirement and each platform requirement without a
   `check_cmd` against the final reviewed diff/repository, recording
   `met`, `unmet`, or `uncertain`. From the worktree root, run each platform
   `check_cmd` separately and verbatim, capturing its actual exit code even
   under fail-fast shells: exit 0 → `met`; non-zero, command-not-found, or
   execution error → `unmet`. A command-backed result always uses `via: cmd`
   and may never be skipped, replaced, or overridden by review judgment; an
   unexecuted command may never be reported as `via: cmd`. A no-command
   platform result uses `via: review`.

   Route each unmet/uncertain epic-specific requirement through the existing
   DISCUSS/unresolved path. Platform results always go in the structured
   `requirements` list; do not invent an `unresolved` finding solely because a
   command-backed platform status is `unmet`, though independent final-review
   findings remain. A review assessment that needs human judgment follows the
   existing DISCUSS escalation path. Set overall `uncertain: true` for any
   uncertain requirement or other material doubt.
4. **Learnings write-back**: anything durable this epic taught (conventions
   discovered, traps hit, decisions future work needs) goes into `AGENTS.md`
   / `CLAUDE.md` / the repo docs — in this same branch. Context is a
   workspace; the repo is memory.
5. Run the FULL test suite one last time; record exact pass/fail counts.
6. Push and open the PR:
   `git push -u origin epic/N-<slug>`
   `gh pr create --base <base> --title "<issue title> (epic #N)" --body-file <tmpfile>`
   The body: a summary, `Closes #N`, and it MUST END with the fenced verdict
   block:

   ```yaml
   agentmon-verdict: v1
   epic: N
   reviews: [specialist, simplifier, deep-scan, cross-model]
   findings: { found: <total across final review>, resolved: <fixed>, unresolved: <count> }
   unresolved:
     - "<each unresolved/DISCUSS item, verbatim>"
   tests: { passed: <N>, failed: <M> }
   requirements:
     - { id: <platform requirement id>, status: <met|unmet|uncertain>, via: <cmd|review> }
   uncertain: <true if you hold ANY material doubt about correctness/completeness>
   learnings_updated: true
   ```

   Verdict rules: `reviews` lists each reviewer lens that actually ran, plus
   `cross-model` — your reviewers are Claude lenses and you are Codex, so the
   review IS cross-model. `findings` counts come from the FINAL review only
   (checkpoint evidence lives in the committed report files). Count every
   DISCUSS item you escalated as unresolved. NEVER round `failed` down to
   zero. Include exactly one requirement entry per platform carrier record,
   in carrier order, or `requirements: []` for an empty platform tier.
   `via: cmd` is mandatory when `check_cmd` exists and `via: review` otherwise;
   ids and the `met|unmet|uncertain` and `cmd|review` values must match
   `hubd/internal/orchestrator/verdict.go` exactly. Epic-specific requirements
   never enter this structured list. The gate auto-merges only on
   `unresolved: 0`, `uncertain: false`, green CI, `reviews ⊇` the project's
   required set, and every project platform requirement reported `met` (a
   non-`met` or `(missing)` requirement escalates) — anything else escalates
   to a human, which is the system working.
7. `agentmon report --epic N --stage pr_open --pr <PR-number>`
8. Final message: one-paragraph summary (what shipped, what's unresolved,
   where the evidence lives). Then STOP. The session stays attachable for
   follow-up questions; the orchestrator retires it on Cancel/Retry, and the
   gate takes it from here.

## `pipeline:light` variant (label-driven)

For small maintenance epics: skip Step 4's committed plan and Step 6 entirely
(keep a task list in a scratch file that reproduces both effective-requirement
tiers verbatim and retains them through final verification). Everything else is UNCHANGED —
worktree, stage reports, escalation, ONE full pre-PR headless review over
`<merge-base>..HEAD`, learnings, full verdict block.

## Quick reference

| Moment | Command |
|---|---|
| entering planning | `agentmon report --epic N --stage planning` |
| entering implementation | `agentmon report --epic N --stage implementing` |
| each review (checkpoint/final) | `agentmon report --epic N --stage reviewing` |
| PR opened | `agentmon report --epic N --stage pr_open --pr <num>` |
| blocked / DISCUSS | `agentmon report --epic N --stage escalated --note "…"` |
| plan-gate (pause after planning) | push branch, then `agentmon report … --stage escalated --branch <branch> --note "plan-gate: …"` — see Step 4 |
| report CLI broken during escalation | `gh issue comment N --body "ESCALATED: …"` |
| cross-provider review | `IS_SANDBOX=1 claude --dangerously-skip-permissions -p "/multi-review <base>..HEAD" > docs/reviews/epic-N-<cpK|final>.md` |
