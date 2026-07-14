---
description: Interactive PRD/phase → epic decomposition for AgentMon orchestration — brainstorm with the human, emit docs/plan/epic-NN files, import them as GitHub issues
argument-hint: [topic or PRD pointer]
---

You are running `/plan-epics`: an INTERACTIVE working session with a human to
decompose a PRD, phase, or feature cluster into orchestrator-ready epics for
this repository. Nothing here is autonomous — every epic ships only after the
human approves it.

## What an epic is (and is not)

An epic file is a REQUIREMENTS document with decisions baked in — scope,
acceptance criteria, constraints, decisions-taken, pointers to the PRD. It is
**never an implementation plan**: the runner regenerates the plan at execution
time against the code as it exists THEN. Writing implementation detail into an
epic bakes in staleness. If the human hands you a detailed plan, capture the
*decisions* it encodes and drop the step-by-step.

Good epic bodies answer: What outcome? What is in/out of scope? How do we know
it is done (acceptance criteria)? What constraints and prior decisions bind the
implementer? What order/dependency does it have?

## Step 1: Understand the ground

1. Read `$ARGUMENTS` — a topic, PRD path, or nothing. Read any referenced docs.
2. Skim the repo (README, docs/, recent commits) enough to talk concretely.
3. Check for existing epics: `ls docs/plan/epic-*.md` and
   `gh issue list --label agentmon:epic --state all --limit 50`. New epics must
   slot into (not duplicate) what exists; note the highest `epic-NN` number.
4. Establish the current project's platform requirements before decomposition.
   Ask the human to copy or confirm the exact `requirements` JSON array from the
   existing authenticated `GET /api/v1/orchestrator/projects` response (for
   example, from the board's Network response or an authenticated same-origin
   browser fetch), selecting the project DTO whose `repo` matches this clone.
   Treat the confirmed list — including an explicitly empty `[]` — as ground
   truth. Never infer, rename, edit, or reconstruct ids, text, or `check_cmd`
   values from repo clues. If the current set cannot be established, STOP before
   writing epic files. Keep these platform records separate from epic-specific
   textual requirements gathered during brainstorming.

## Step 2: Brainstorm the decomposition WITH the human

If the `superpowers:brainstorming` skill is available, invoke it and follow it
(it will drive the question flow). Otherwise: ask questions ONE AT A TIME,
prefer multiple choice, and converge on:

- The independent pieces (each epic = one runner session's worth of coherent
  work; if you cannot state acceptance criteria in a few lines, split it).
- The dependency order (`blocked-by` edges — only REAL blockers, not niceties).
- The effective requirement set for each epic: the full confirmed platform set
  applies to every epic, union that epic's own textual requirements. Keep the two
  tiers visibly distinct; only platform records later enter the structured PR
  verdict schema.
- Per-epic dials, from this table:

| Label | Effect |
|---|---|
| `agentmon:epic` | REQUIRED on every epic — the orchestrator's filter. |
| `pr-gate` | Hub never auto-merges; the PR waits for a human. |
| `plan-gate` | Runner pauses after planning for board approval. |
| `agent:claude` / `agent:codex` | Provider override for this epic. |
| `pipeline:light` | Skip the committed plan + checkpoints (small fixes). |

Where to cut — decide boundaries by review value, not just layer or size.
MERGE work across a seam the build already guards (compiler, typechecker, a
contract-mirror test, a round-trip test) — a human plan-gate on a
machine-checked seam is wasted ceremony. ISOLATE into its own epic whatever is
novel, security-sensitive, invokes an external tool, or is genuinely ambiguous
— that is where a fresh-context plan/checkpoint review earns its cost. A
layer-wise stack (storage → API → wiring) is a starting point, not the answer:
adjacent layers whose contract the build enforces usually belong together; the
one layer that carries the feature's real risk usually stands alone.

Recommend dials, don't just ask: first-of-its-kind or risky epics deserve
`plan-gate` (a human reviews the plan before implementation — ambiguity found
at planning is far cheaper than at review); schema/auth/data-loss-adjacent
epics deserve `pr-gate`; one-file maintenance fixes deserve `pipeline:light`.

Before presenting, self-critique the slice: is there a strictly-better cut that
isolates the same risk in FEWER epics — merging machine-checked layers while
keeping the novel/risky work standalone? If so, lead with it. When you offer
alternatives, make them differ in WHERE the boundaries fall, not merely how many
pieces (three-vs-four-vs-five of the same layer-wise cut is one option, not
three).

Present the final epic list (title, one-line scope, dials, blocked-by) and get
explicit approval BEFORE writing files.

## Step 3: Write the epic files

One file per epic: `docs/plan/epic-NN-<slug>.md`, numbered in dependency-ish
order continuing from the highest existing NN. The front-matter is a STRICT
`key: value` format — not YAML. The importer rejects unknown keys, so a typo
fails loudly instead of silently dropping a dial. Exact contract:

```
---
title: <plain text, required>
labels: agentmon:epic, plan-gate, agent:claude
blocked-by: epic-01, #12
---
<markdown body>
```

- `labels`: comma-separated; MUST include `agentmon:epic`. Brackets `[...]`
  are tolerated but pointless.
- `blocked-by`: comma-separated refs — a sibling file basename prefix
  (`epic-01`) for epics in this batch, or `#N`/`N` for issues that already
  exist. Omit the line entirely when there are no blockers.
- Never write an `issue:` key yourself — the importer stamps it after
  creating the issue. A stamped file is skipped on re-import; the file is the
  epic's birth certificate.
- Body sections to include: `## Scope`, `## Acceptance criteria`,
  `## Constraints & decisions`, optionally `## Pointers` (PRD/docs links).
  Acceptance criteria must restate observable compliance with every effective
  requirement. End `## Constraints & decisions` with this canonical carrier:

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

  Copy the complete platform array as valid JSON, in its original order, so
  quotes, backticks, and shell characters remain unambiguous. Omit `check_cmd`
  only when the source record has none; use `[]` for an empty platform tier and
  `None.` for an empty epic-specific tier. Do not filter records by perceived
  relevance or add implementation-plan detail. This section is the issue-body
  carrier consumed by `epic-pipeline`, not the gate's source of truth.
  Do NOT write your own `Blocked-by:` lines in the body — the importer
  appends the resolved `Blocked-by: #N` line the hub parses.

Commit the files: `git add docs/plan/ && git commit -m "docs: epics — <topic>"`
(no trailers).

## Step 4: Import — with the project PAUSED

The go-live ritual, in this order (files → issues → human reviews the board →
work starts). Confirm each with the human:

1. **Pause first.** Ask the human to confirm the project is paused on the
   AgentMon board (or that the orchestrator/`github.token` isn't live yet).
   Importing into a running project starts work immediately — never do that.
2. Dry-run and show the output: `agentmon import-epics --dir docs/plan --dry-run`
3. Import: `agentmon import-epics --dir docs/plan`
   - Idempotent: files already stamped with `issue:` are skipped.
   - The blocked-by pass rewrites those issues' bodies FROM THE FILES —
     warn the human that manual GitHub-side body edits to blocked-by epics
     get overwritten on re-import (files are the source of truth).
   - If it fails on `gh` auth or access, run `agentmon doctor` and hand the
     human its output.
4. Commit the stamped files: `git add docs/plan/ && git commit -m "docs: epics imported — issue numbers stamped"`
   Then push (this interactive session may push; autonomous runners may not).
5. Tell the human: review the board (order, dials, dependencies), then hit
   **Resume** when ready to let runners start.
