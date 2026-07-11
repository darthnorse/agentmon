# epic-pipeline / plan-epics — design inputs from the live sub-project-1 run

**Date:** 2026-07-10. **Status:** raw material for sub-project 2's brainstorm/spec.
Everything here was learned by handing the sub-1 plan (`plans/2026-07-10-orchestrator-hub-core.md`)
to a Codex (gpt-5.6-sol) session and supervising it — a live prototype of the runner.
Spec context: `specs/2026-07-10-agentmon-orchestrator-design.md` §7.

## 1. The proven execution rules (verbatim-preserve these)

The consolidated prompt that produced disciplined autonomous execution. The skill
must encode these as instructions; the plan generator must assume them:

1. Read the plan IN FULL first, including Global Constraints and the type registry,
   which apply to every task.
2. Execute tasks strictly in order, single-agent. Follow every checkbox step
   literally and in sequence — including "run test to verify it FAILS" steps.
   Do not skip, merge, or reorder. No reviewer-agent emulation beyond what the
   plan's own steps say (left alone, Codex invented a two-reviewer-per-task
   ceremony — 3× cost for nothing).
3. Full build+test green before every commit; exact commit message from the plan;
   never any trailer; never push.
4. Where the plan anchors to existing files ("mirror X at file:line"), open those
   files first — the plan is authoritative for WHERE to look, the code on disk for
   exact names.
5. **Stop-don't-improvise (rule 7):** any mismatch between plan and repo — a
   signature, a path, an unexplainable failure — STOP the task, record the
   mismatch, report. Trivial mechanical fixes (missing import, gofmt) excepted.
   *Fired 3× in the live run; correct all 3 times (pre-existing config_test.go,
   sandbox loopback, duplicate test helper). Maps 1:1 onto the orchestrator's
   escalate-with-question stage.*
6. Scope discipline: touch only files the current task lists.
7. Checkpoint stops are hard stops: report and WAIT for explicit continue/fixes.
8. End-of-run (or on stop): write the implementation report per the plan's
   convention and commit it.

## 2. Plan-emission requirements (the generator's contract)

- Plans carry: a Global Constraints section, a **shared type registry** (single
  source of truth for cross-task names — the compiler then enforces cross-task
  consistency for free), exact file paths, complete code per step, run commands
  with expected output, exact commit messages.
- **Checkpoint steps are artifact content, not process vibes**: a global
  constraint line PLUS an explicit `Step N: CHECKPOINT — STOP` after the chosen
  tasks. Literal-instruction agents obey artifacts, not remembered intentions.
- Seam heuristic: layer boundaries (foundations/schema → pure logic →
  integration/IO → wiring), every ~4–6 tasks, always one immediately after the
  highest-judgment task. Data-layer checkpoints earn the most (schema errors
  compound fastest).
- Annotate file dispositions truthfully (create vs append) — two of three rule-7
  stops were the plan saying "(new)" for existing things. The generator should
  verify against the repo before emitting.

## 3. Checkpoint review mechanics (empirically validated)

- At each checkpoint the runner runs a cross-provider multi-review **in-session**
  on the **segment diff** (linear cost): Claude runner → `/multi-review --codex`;
  Codex runner → inverts with headless `claude` as reviewer. Lenses run as
  subagents/`codex exec` subprocesses, so only the consolidated report enters the
  runner's context.
- FIX findings: applied + committed at the checkpoint (with regression tests per
  the test-with-fix policy). DISCUSS findings: `agentmon report --stage escalated
  --note "<finding>"` — reuses the human-summoning path. NITPICKs: report only.
- Validation datum (2026-07-10, checkpoint 1, 875-line diff): 10 findings,
  10/10 adversarially confirmed, 3 cross-model. **All 10 were plan-author bugs,
  not implementer bugs** — staged review checks the plan against reality; no
  amount of up-front plan quality substitutes.
- The final pre-PR multi-review reviews the whole branch and feeds the verdict
  block (`reviews:` list must satisfy the project's `required_reviews`).

## 4. Resume-from-artifacts property

State lives in (a) the plan's ticked checkboxes, (b) per-task commits, (c) the
report file — never in session context. Consequences the skill must preserve:

- A fresh session resumes losslessly mid-plan. The resume prompt must be
  **self-contained** (a fresh session never saw the original rules — restate
  them; "rules as before" is meaningless to it).
- Context bloat in long plans (esp. Codex, which has no subagent primitive and
  codes in its main window) is handled by killing the session and resuming fresh,
  not by fighting the window. `codex exec` subprocesses are Codex's stand-in for
  subagents on heavy subtasks.
- Fix-instruction pattern for resolved mismatches: update the plan artifact
  FIRST (commit it), then hand the runner a short explicit resolution
  ("the plan is corrected at X; do Y; resume at step Z"). The artifact stays
  truthful for any future resume.

## 5. Environment facts (doctor-run checklist inputs)

- Kickoff commands must carry autonomy flags (a permission prompt = a stalled
  epic): `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline N"`,
  `codex -a never "/epic-pipeline N"`. tmux runs the command via `sh -c`, so env
  prefixes work.
- Codex hosts need `~/.codex/config.toml` `[sandbox_workspace_write]`:
  project repo's `.git` in `writable_roots` (else no branches/commits) and
  `network_access = true` (else `httptest` loopback binds fail the test gate).
  Read-only default GOCACHE → `GOCACHE=/tmp/<proj>-go-cache`. Config changes
  need a fresh session. `danger-full-access` rejected: blast radius on a shared
  host; grant the narrow capability instead.
- `gh` CLI authenticated per host; repo clone at workdir.

## 6. plan-epics (the human-in-loop sibling skill)

- Interactive session on the project host (`/plan-epics`); brainstorms the
  PRD/phase with the human (may wrap superpowers:brainstorming on Claude);
  emits `docs/plan/epic-NN-<name>.md` — front-matter: title, labels
  (`agentmon:epic` + dials: `pr-gate`, `plan-gate`, `agent:codex`,
  `pipeline:light`), `Blocked-by: #N` lines — body: scope, acceptance criteria,
  constraints, decisions-taken. Commits, then runs the `gh issue create` import.
- Epic bodies are REQUIREMENTS with decisions baked in, never implementation
  plans — the runner regenerates the plan at execution time against current code.
- Go-live ritual: import with the project paused; human reviews the board; Resume.
- Board button ("Plan epics…") = New-Session-with-command preset (spec §8).

## 7. Open questions for the sub-2 brainstorm

From the checkpoint-3 review (agent-contract items deliberately deferred):

- **Report drain durability:** the v1 drain is destructive (clear-on-GET). The
  agent endpoint should be two-phase (peek + ack by cursor/batch id) so a lost
  response or hub crash cannot lose reports; hub-side pending-queue mitigation
  exists (`orchestrator.pending`), redelivered duplicates are safe (guarded
  transitions reject them).
- **Session lifecycle on retry/cancel:** the hub never kills runner sessions.
  Attempt-suffixed names avoid the 409 collision, but a timeout-stalled runner
  keeps working orphaned. AgentAPI should gain KillSession; Cancel/Retry should
  retire the old session.
- **Attempt-scoped provenance:** reports carry no attempt token; a buffered
  report from attempt N-1 could in principle pass the session check if names
  ever collide. Attempt-suffixed session names close this in practice; a run
  token would close it in principle.
- **Retry kickoff:** spec §4 wants retries kicked off with "assess branch state
  and continue" — KickoffCommand needs an attempt/resume parameter once the
  epic-pipeline skill defines the flag.

- Verdict block: include checkpoint-review count/results, or final review only?
- Checkpoint FIX commits: same branch mid-plan (current practice) — any reason
  to fold into the task commits instead?
- `pipeline:light`: which of the above scale down (single checkpoint? review
  only at PR?) for one-off maintenance issues.
- Codex-side review invocation: exact headless `claude` form + findings format
  (mirror of multi-review's codex lens, inverted).
