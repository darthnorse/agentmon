---
title: Requirements injection & verification in the runner skills
labels: agentmon:epic, pr-gate, plan-gate
blocked-by: epic-02
---
## Scope

Close the loop: make the runner **inject** requirements into planning and build
context and **verify** them at final review, emitting the verdict block epic-02's
gate consumes. This is prompt / skill work across the four embedded runner files —
`plan-epics.md` and `epic-pipeline.md` for both the Claude and Codex variants.

Three touch points:

- **Plan** (`plan-epics`): read the project's platform requirements as ground
  truth, and restate the effective set (platform ∪ any epic-specific requirements)
  into each generated epic file so every epic carries its requirements.
- **Build** (`epic-pipeline` orient / global constraints): inject the effective
  requirement set into the committed plan so every task inherits it. Each epic is a
  fresh session, so re-inject per epic.
- **Verify** (`epic-pipeline` final review): for each requirement, if it has a
  `check_cmd`, **run it** and set the requirement's status from the real exit code
  (`0` → `met`; non-zero / absent / error → `unmet`); otherwise assess it via the
  review lens (`met` / `unmet` / `uncertain`). Emit a `requirements:` list into the
  verdict block matching epic-02's schema exactly.

Out of scope: the gate / verdict Go code (epic-02); storage / UI (epic-01); and any
new hub API for delivering requirements — v1 carries them in the epic issue body.

## Acceptance criteria

- `plan-epics` (both variants) reads platform requirements and restates the
  effective set into each generated epic file's requirements-bearing sections.
- `epic-pipeline` (both variants) injects the effective set into the committed
  plan's global constraints so downstream tasks inherit it.
- `epic-pipeline` final verification runs each requirement's `check_cmd` when
  present and derives status from the exit code; requirements without a command are
  assessed by review; the run emits a `requirements: [{id, status, via}]` block
  whose keys and values match epic-02's parser exactly.
- All four files updated (`claude` + `codex` × `plan-epics` + `epic-pipeline`),
  behavior-equivalent across providers; they remain embedded and installed by
  `runnerfiles`, and `agentmon doctor` still locates them.
- The agent module builds (`go test ./agent/...`) and the embed list stays correct.
- Because prompt efficacy is not machine-checkable, functional validation is the
  **supervised first dogfood run**: the first epic's plan shows the injected
  requirements and its PR verdict carries a populated `requirements:` block the gate
  can read.

## Constraints & decisions

- **The `requirements:` verdict block MUST match epic-02's schema exactly**
  (`id`, `status ∈ met|unmet|uncertain`, `via ∈ cmd|review`). This runner↔gate seam
  is **not** compiler-guarded — treat schema-match against `verdict.go` as an
  explicit acceptance check, verified at the first real run.
- **Delivery carrier is the epic issue body**, not a hub fetch: plan-epics restates
  the requirements into each epic, and the runner reads the issue it already reads.
  No new API in v1. The gate's source of truth remains `Project.Requirements`
  (epic-02); the issue body is only the runner's carrier, so the two can drift — and
  the gate fails closed on the safe side.
- **Check-command honesty:** report the actual exit code; never mark a `check_cmd`
  requirement `met` without running it. A command that is missing, errors, or exits
  non-zero → `unmet` (fail-closed), never silently skipped.
- Keep the two provider variants behavior-equivalent.
- Do not bake implementation-plan detail into the epic files plan-epics generates —
  the requirements restatement lands in the epic's requirements sections
  (Acceptance / Constraints), consistent with plan-epics treating the epic file as a
  requirements document, not a plan.

## Pointers

- Runner files: `agent/internal/runnerfiles/files/claude/{plan-epics,epic-pipeline}.md`,
  `agent/internal/runnerfiles/files/codex/{plan-epics,epic-pipeline}.md`; embed list
  `agent/internal/runnerfiles/runnerfiles.go`; `agentmon doctor`
  (`agent/cmd/agentmon-agent/doctor_cli.go`).
- Consumes the verdict schema from epic-02 (`verdict.go`) and the field from
  epic-01.
- Design ground truth: three injection points (plan / build / final review),
  honesty-via-check-command, and the decision that no separate explore stage is
  needed (grounding is already reasoning-integrated in the pipeline).
