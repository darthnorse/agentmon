# Requirements injection & verification in the runner skills (epic #3)

## Goal

Carry a project's requirements through all three reasoning seams of the runner:
`plan-epics` copies the effective requirement set into each epic requirements
document, `epic-pipeline` restates that set in the implementation plan, and the
final review verifies it honestly before emitting epic-02's exact verdict schema.

## Acceptance criteria

1. Both `plan-epics` variants establish the project platform requirements as
   ground truth and restate the effective platform-plus-epic-specific set in each
   generated epic's acceptance/constraints sections.
2. Both `epic-pipeline` variants inject the effective set from the issue body into
   the committed plan's Global Constraints so every task inherits it.
3. Both pipeline variants execute every platform `check_cmd` at final verification,
   derive its status from the real exit code, assess no-command requirements by
   review, and emit exact `{id,status,via}` verdict entries.
4. All four embedded runner files change and remain behavior-equivalent across
   providers while retaining their provider-specific argument/reviewer syntax.
5. The agent module builds/tests and the existing embed/install/doctor paths remain
   valid.
6. Prompt efficacy remains explicitly assigned to a supervised first dogfood run:
   inspect the generated plan and the populated verdict block against `verdict.go`.

## Global Constraints

- **Full pre-commit gate:** from the repository root run
  `GOCACHE=/tmp/agentmon-go-cache make test`, then
  `cd web && npm run typecheck && npm run test:run`; both commands must exit 0
  before every commit. The focused acceptance command
  `GOCACHE=/tmp/agentmon-go-cache go test ./agent/...` must also exit 0 during final
  validation. [Source: `AGENTS.md`; epic #3 Acceptance]
- **Commit style:** conventional prefixes, exact messages named below, and no
  `Co-Authored-By` or other AI-attribution trailers. [Source: `AGENTS.md`; epic #3]
- **Scope:** modify only the four embedded prompt files named below, `CLAUDE.md`,
  this plan, and generated review evidence. Do not change the gate/verdict Go code, project
  storage/UI, runnerfiles embed list, doctor implementation, or add a hub API.
  Existing `runnerfiles.go` already embeds and installs the four paths, and
  `doctor_cli.go` already locates each provider's installed `epic-pipeline.md`.
  [Source: epic #3 Scope/Constraints; verified in current repo]
- **Two-tier semantics:** platform requirements are structured project records
  `{id,text,check_cmd?}` and are the only entries emitted under verdict
  `requirements:`; epic-specific requirements remain textual, are reviewed, and
  surface as unresolved findings when not met/uncertain. This preserves epic-01's
  “epic-level requirements get no schema” decision and epic-02's platform-only
  gate input. [Source: `docs/plan/epic-01-project-requirements-storage.md`;
  `docs/plan/epic-02-requirements-merge-gate.md`; epic #3]
- **Exact verdict seam:** `hubd/internal/orchestrator/verdict.go` accepts only
  `status` values `met|unmet|uncertain` and `via` values `cmd|review`; ids must be
  non-empty and unique. A platform requirement with `check_cmd` always uses
  `via: cmd`: exit 0 is `met`; non-zero, command-not-found, or execution error is
  `unmet`. A platform requirement without `check_cmd` uses `via: review`.
  [Source: epic #3 Constraints; `hubd/internal/orchestrator/verdict.go`]
- **Carrier and drift:** the epic issue body is the runner carrier, while
  `Project.Requirements` remains the gate authority. Never fetch a replacement set
  from a new API and never invent or normalize ids/commands in the runner. The
  existing authenticated `GET /api/v1/orchestrator/projects` DTO is the reliable
  planning-time source because the settings form displays `text`/`check_cmd` but
  not the stable server-derived `id`. Missing carrier data therefore fails closed
  at the gate. [Source: epic #3 Constraints; `hubd/internal/api/router.go`;
  `hubd/internal/api/orchestrator.go`; `web/src/components/board/ProjectForm.tsx`]
- **Accepted v1 trust boundary:** executing a carried `check_cmd` verbatim from
  the private-repository epic issue body is a conscious v1 decision. Issue editors
  are limited to the owner and runners and already control repository code the
  runner executes; this matches the PR-body provenance boundary in `gate.go`.
  Re-fetching commands from authoritative `Project.Requirements` or using signed
  delivery is deferred to v2. Preserve the exact carried command and fail closed
  on parse/execution errors. [Source: human resolution of Checkpoint 1 DISCUSS]
- **Provider equivalence:** prose and behavior added to Claude/Codex variants must
  be equivalent; preserve only existing provider differences (`$ARGUMENTS` versus
  `$1`/`N`, Codex/Claude reviewer commands, and provider-specific lifecycle text).
- **Execution order/resume:** complete and tick every checkbox in order. A
  plan/repo mismatch requires escalation; update and commit this plan before
  resuming after any approved correction. [Source: current epic-pipeline contract]
- **Operational variance:** the sandbox denied the prescribed sibling worktree
  `/root/agentmon-epic-3`; this branch is instead isolated at
  `/tmp/agentmon-epic-3`. No repository structure or branch convention changes.

## Requirements traceability

| Epic acceptance criterion | Planned coverage |
|---|---|
| AC1 — both generators carry effective requirements | Task 1 |
| AC2 — both pipelines inject requirements into plans | Task 2 |
| AC3 — honest final verification + exact verdict entries | Task 2 |
| AC4 — four-file/provider equivalence | Tasks 1, 2, 3 |
| AC5 — agent build/embed/doctor remain valid | Task 3 |
| AC6 — supervised dogfood remains the efficacy gate | Tasks 1, 2, 3 |

## Verified file dispositions

| Path | Disposition | Planned change |
|---|---|---|
| `agent/internal/runnerfiles/files/claude/plan-epics.md` | **Modify** (127 lines) | Establish/carry effective requirements using Claude argument conventions. |
| `agent/internal/runnerfiles/files/codex/plan-epics.md` | **Modify** (124 lines) | Behavior-equivalent generator instructions using Codex argument conventions. |
| `agent/internal/runnerfiles/files/claude/epic-pipeline.md` | **Modify** (252 lines) | Orient, plan injection, final verification, verdict schema, light-pipeline parity. |
| `agent/internal/runnerfiles/files/codex/epic-pipeline.md` | **Modify** (264 lines) | Same behavior with headless Claude review mechanics. |
| `docs/plans/epic-3.md` | **Create** | This resumable plan. |
| `CLAUDE.md` | **Modify** | Record the accepted v1 issue-body command trust boundary and deferred v2 hardening. |
| `docs/reviews/epic-3-cp1.md`, `epic-3-cp2.md`, `epic-3-final.md` | **Create later** | Checkpoint/final review evidence. |

No structure variance is needed in the repository: prompt behavior remains in the
four already-embedded Markdown assets, and existing tests already compare installed
bytes to their embedded source. Prompt efficacy is deliberately not converted into
brittle substring tests; AC6 retains the issue's supervised dogfood validation.

## Task 1: Carry effective requirements through both epic generators (AC: 1, 4, 6)

### Files

- Modify `agent/internal/runnerfiles/files/claude/plan-epics.md`.
- Modify `agent/internal/runnerfiles/files/codex/plan-epics.md`.

### Complete content to add

- [x] **Step 1.1 — Establish the authoritative input before decomposition.** In
  each `Step 1: Understand the ground`, after reading the topic/repo, add
  behavior-equivalent instructions that:

  1. ask the interactive human to copy or confirm the current project's exact
     `requirements` JSON array from the existing authenticated
     `GET /api/v1/orchestrator/projects` response (for example, the board's Network
     response or an authenticated same-origin browser fetch), selecting the DTO
     whose `repo` matches this clone;
  2. treat that confirmed list, including an explicitly empty list, as ground
     truth and never infer/rename/edit ids, text, or commands from repo clues;
  3. stop before writing files if the current set cannot be established; and
  4. keep platform records separate from per-epic textual requirements collected
     during brainstorming.

  The terminal runner has no hub browser session/authentication, the settings form
  does not display stable ids, and epic #3 forbids adding a delivery API. Explicit
  human confirmation of the existing authenticated project DTO in this
  already-interactive workflow is therefore the delivery mechanism at this seam.

- [x] **Step 1.2 — Define the effective set during brainstorming.** Add a bullet to
  each `Step 2` saying that every epic's effective set is the full confirmed
  platform set union that epic's own textual requirements. Platform requirements
  apply to every epic; epic-specific requirements apply only to their epic. Keep
  the two tiers visibly distinct so only platform records later enter the verdict
  schema.

- [x] **Step 1.3 — Define one canonical issue-body carrier.** Extend each `Step 3`
  body contract so `## Acceptance criteria` restates observable compliance with
  every effective requirement and `## Constraints & decisions` ends with this
  exact shape. Copy the platform array as valid JSON so quotes/backticks/shell
  characters are escaped unambiguously; use `[]` for an empty platform tier and
  `None.` for an empty epic-specific tier:

  ````markdown
  ### Effective requirements

  Platform (project ground truth; exact records):
  ```json
  [
    {"id":"<stable-id>","text":"<requirement text>","check_cmd":"<verbatim shell command>"}
  ]
  ```

  Epic-specific:
  - <textual requirement>
  ````

  Omit only the `check_cmd` property when that platform record has none. Require
  exact JSON ids/text/commands from the DTO, no implementation-plan detail, no platform record
  filtering based on perceived relevance, and no extra front-matter keys (the
  importer rejects them). State that this section is the carrier consumed by
  `epic-pipeline`, not the gate's source of truth.

- [x] **Step 1.4 — Verify provider parity and the focused module.** Run:

  ```bash
  rg -n "Effective requirements|project ground truth|check_cmd|effective set" \
    agent/internal/runnerfiles/files/{claude,codex}/plan-epics.md
  GOCACHE=/tmp/agentmon-go-cache go test ./agent/...
  ```

  Expected: both files contain every new carrier concept; all agent packages pass.

- [x] **Step 1.5 — Run the full pre-commit gate and commit.** Run the two Global
  Constraints gate commands; both must exit 0. Then:

  ```bash
  git add agent/internal/runnerfiles/files/claude/plan-epics.md \
    agent/internal/runnerfiles/files/codex/plan-epics.md docs/plans/epic-3.md
  git commit -m "feat(runner): carry requirements into generated epics"
  ```

- [x] **CHECKPOINT 1 — reviewed to 7757eef. Review the generator carrier seam immediately after this
  highest-judgment format decision.** Review from the merge-base through `HEAD`;
  validate that both providers create equivalent issue bodies, exact project fields
  survive verbatim, epic-specific requirements remain unstructured, and the strict
  importer front matter is untouched. Record the reviewed SHA here and commit the
  report/tick as `docs: epic #3 checkpoint 1 review`.

## Task 2: Inject and verify requirements in both epic pipelines (AC: 2, 3, 4, 6)

### Files

- Modify `agent/internal/runnerfiles/files/claude/epic-pipeline.md`.
- Modify `agent/internal/runnerfiles/files/codex/epic-pipeline.md`.

### Complete content to add

- [ ] **Step 2.1 — Parse the carrier while orienting.** In both `Step 1: Orient`
  sections, require the runner to read `### Effective requirements` from the issue
  body, parse its fenced JSON platform array, and retain two inventories:

  - platform records with exact `id`, `text`, and optional verbatim `check_cmd`;
  - epic-specific textual requirements.

  `[]`/`None.` mean explicitly empty tiers. For backward-compatible older issues
  lacking the canonical section, treat their existing Scope/Acceptance/Constraints
  as epic-specific requirements and the structured platform inventory as empty;
  do not invent platform ids. If the canonical section exists but its JSON is
  malformed, has duplicate/empty ids, or is ambiguous, use the escalation protocol.
  State that executing its exact `check_cmd` is accepted for v1 under the private-repo
  owner/runner trust model; authoritative lookup or signed delivery is v2 hardening.

- [ ] **Step 2.2 — Make requirements inherited Global Constraints.** Extend the
  plan contract in both `Step 4` sections so the committed plan's Global
  Constraints must reproduce the complete two-tier inventory verbatim, label the
  platform versus epic-specific tiers, and state explicitly that every task
  inherits every effective requirement. Require requirement-to-task/verification
  traceability in addition to the existing AC traceability. The light variant has
  no committed plan, so add equivalent instructions that its scratch task list must
  retain the same inventory through final verification.

- [ ] **Step 2.3 — Add final requirement verification after fixes settle.** In both
  `Step 7: Finish`, after final review/fixes and before the last test/verdict steps,
  add an ordered verification procedure:

  1. assess every epic-specific requirement and every platform requirement without
     `check_cmd` against the final reviewed diff/repository using the final review
     lens, recording exactly `met`, `unmet`, or `uncertain`;
  2. from the worktree root run each platform `check_cmd` verbatim and separately,
     capture its actual exit code even under fail-fast shells, and record `met` only
     for exit 0; non-zero, command-not-found, or execution error records `unmet`;
  3. never replace, skip, or override a command-backed result with reviewer
     judgment, and never claim `via: cmd` for an unexecuted command;
  4. route every unmet/uncertain epic-specific requirement through the existing
     review/DISCUSS unresolved path because that tier is flag-only. Platform results
     always go in the structured list; do not synthesize an `unresolved:` finding
     merely because a command-backed platform status is `unmet` (it was not a final
     review finding), though any independent final-review finding/DISCUSS remains in
     the existing list/count. Set overall `uncertain: true` for any uncertain
     result/material doubt, and follow the existing escalation semantics for a
     requirement needing human judgment. `findings` counts continue to come only
     from the final review and need not equal the number of structured non-met
     platform statuses.

- [ ] **Step 2.4 — Extend the exact verdict template.** Insert immediately after
  `tests:` in both fenced YAML templates:

  ```yaml
  requirements:
    - { id: <platform requirement id>, status: <met|unmet|uncertain>, via: <cmd|review> }
  ```

  Require one entry per platform carrier record, in carrier order, and
  `requirements: []` when the platform tier is empty. Add verdict rules that
  `via: cmd` is mandatory whenever `check_cmd` exists, `via: review` otherwise,
  ids/status/via must match `hubd/internal/orchestrator/verdict.go` exactly, and
  epic-specific requirements never appear in this structured list. Preserve the
  existing provider-specific `reviews:` examples.

- [ ] **Step 2.5 — Make the dogfood acceptance explicit.** Add a finish rule to
  inspect the first supervised run's committed plan for both requirement tiers and
  validate its populated PR `requirements:` YAML against
  `hubd/internal/orchestrator/verdict.go`; this is the functional efficacy check,
  while `go test` verifies the assets remain embedded/installed.

- [ ] **Step 2.6 — Verify provider parity and exact schema vocabulary.** Run:

  ```bash
  rg -n "Effective requirements|check_cmd|requirements: \[\]|via: cmd|via: review|met\|unmet\|uncertain" \
    agent/internal/runnerfiles/files/{claude,codex}/epic-pipeline.md
  rg -n 'yaml:"(id|status|via|requirements)"|case "met", "unmet", "uncertain"|case "cmd", "review"' \
    hubd/internal/orchestrator/verdict.go
  GOCACHE=/tmp/agentmon-go-cache go test ./agent/...
  ```

  Expected: both prompt variants contain all new behaviors; the prompt vocabulary
  exactly matches the parser; all agent packages pass.

- [ ] **Step 2.7 — Run the full pre-commit gate and commit.** Run the two Global
  Constraints gate commands; both must exit 0. Then:

  ```bash
  git add agent/internal/runnerfiles/files/claude/epic-pipeline.md \
    agent/internal/runnerfiles/files/codex/epic-pipeline.md docs/plans/epic-3.md
  git commit -m "feat(runner): verify requirements in epic pipelines"
  ```

- [ ] **CHECKPOINT 2 — review the final-verification/schema seam immediately after
  the highest-judgment execution logic.** Review from checkpoint 1's recorded SHA
  through `HEAD`; validate command exit-code honesty, no-command review semantics,
  two-tier behavior, light-pipeline coverage, provider equivalence, and the exact
  `verdict.go` key/enums. Record the reviewed SHA here and commit report/tick as
  `docs: epic #3 checkpoint 2 review`.

## Task 3: Final structural and acceptance validation (AC: 4, 5, 6)

- [ ] **Step 3.1 — Confirm only intended implementation files changed.** Run
  `git diff --name-only origin/main...HEAD` and inspect each path. Expected
  implementation paths are exactly the four embedded Markdown files; this plan and
  review evidence are the only additional artifacts. Confirm
  `agent/internal/runnerfiles/runnerfiles.go` still embeds/maps all four paths and
  `agent/cmd/agentmon-agent/doctor_cli.go` still checks the installed pipeline path
  for every detected provider.

- [ ] **Step 3.2 — Run focused and full gates.** Run:

  ```bash
  GOCACHE=/tmp/agentmon-go-cache go test ./agent/...
  GOCACHE=/tmp/agentmon-go-cache make test
  cd web && npm run typecheck && npm run test:run
  ```

  Expected: all commands exit 0; record exact Go package and web test counts for
  the final verdict. No separate commit is expected unless validation exposes a
  plan/repo mismatch (which must be escalated/corrected in the plan first).

- [ ] **Step 3.3 — Preserve the supervised efficacy handoff.** In the PR summary,
  call out that the first post-merge `/plan-epics` dogfood must show the effective
  set in its generated issue and committed plan, execute any `check_cmd`, and emit
  a populated parser-valid `requirements:` list. Do not claim that Markdown/static
  unit tests prove prompt behavior.

  Also update `CLAUDE.md` with the accepted v1 issue-body `check_cmd` trust boundary
  and the authoritative-lookup/signed-delivery hardening deferred to v2.

- [ ] **Step 3.4 — Rebase, run the required final whole-branch cross-provider
  review, route all outcomes, commit `docs/reviews/epic-3-final.md`, rerun the full
  gate, push, and open the PR.** The PR verdict must include the new platform
  requirement results applicable to this epic (or `requirements: []` only if the
  issue carrier has an explicitly empty platform tier), plus honest findings/tests
  counts and uncertainty. Report `pr_open` only after `gh pr create` succeeds.
