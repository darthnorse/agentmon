---
title: Project-scoped platform requirements — storage & settings UI
labels: agentmon:epic, pr-gate
issue: 1
---
## Scope

Introduce a first-class, per-project set of **platform requirements** — invariants
that must hold for every epic in the project (e.g. "always use RLS",
"tenant isolation", "WCAG 2.2 AA", "no PII in logs"). Add a new `Requirements`
field to the `Project` registration, persisted, exposed through the API, mirrored
in the web contract, and editable from the project settings form beside the
existing Require-CI control.

Each requirement is a structured record: a stable `id` (slug used later by the
verdict/gate to match), a natural-language `text` (the standard, which doubles as
the review lens), and an **optional** `check_cmd` — a shell command whose exit
code can certify the requirement where an LLM cannot (WCAG / PII / RLS scanners).

This epic is **storage + UI only**. The field is inert: nothing reads it yet. The
merge gate reads it in epic-02; the runner injects and verifies it in epic-03.
Out of scope: any gate, verdict, or runner behavior; and epic-level requirements
(those live in the epic issue body and get no schema).

## Acceptance criteria

- A new migration (next number, `0009`) adds a `requirements` column to `projects`
  following the one-line `ALTER TABLE … NOT NULL DEFAULT '[]'` pattern of
  `0007_require_ci` / `0008_pinned`; applying it to a populated DB preserves
  existing rows.
- `Project.Requirements` round-trips through `CreateProject`, `GetProject`,
  `ListProjects`, and `UpdateProject` using the JSON-in-TEXT marshal pattern used
  for `required_reviews` (never NULL; `[]` for empty). Covered by `projects_test`.
- `POST /projects` and the project `PATCH` accept and return `requirements`; the
  field is present in the project DTO. Covered by `orchestrator_test`.
- `web/src/lib/contracts.ts` mirrors the new field and the `Requirement` shape —
  Go build, `npm run typecheck`, and the contract-mirror test all green.
- The project settings form (`ProjectForm.tsx`) lets a user add / edit / remove
  requirement rows (text + optional check-command per row) and persists them via
  PATCH; existing values render on load. Covered by a component test.
- Full gate green: `make test` and `cd web && npm run typecheck && npm run test:run`.

## Constraints & decisions

- **Requirement shape is decided:** `{ id: string; text: string; check_cmd?: string }`.
  `id` is a lowercase-kebab slug that is **stable** — derive it from `text` when
  the author does not supply one, but do not re-derive it when `text` is later
  edited. The id is the join key the verdict and gate match on; silently changing
  it would break enforcement.
- **Mirror `RequiredReviews` for storage, not for type.** `RequiredReviews` is
  `[]string`; `Requirements` is a `[]Requirement` struct slice serialized as JSON
  in a TEXT column. Reuse the `marshalStrings` / `unmarshalStrings` approach
  (`json.Marshal` / `Unmarshal` of the slice, `[]` for empty, never NULL).
- The Project DTO uses **json tags in snake_case** (`requirements`, and within each
  record `id` / `text` / `check_cmd`), consistent with `required_reviews`. This is
  the API DTO — distinct from the Verdict struct's yaml-only / CAPITALIZED-JSON
  caveat (that applies in epic-02, not here).
- These are the **platform** requirements only — the fail-closed source of truth
  the gate reads in epic-02. Epic-level requirements are authored in the epic issue
  body and get no schema here.
- `repo` and `server_id` remain non-updatable; `requirements` joins the
  editable-registration-field set that `UpdateProject` rewrites (alongside
  `required_reviews`).
- No gate or runner wiring in this epic. A reviewer should be able to confirm the
  field is inert end-to-end.

## Pointers

- Storage analog: `hubd/internal/db/projects.go` (`RequiredReviews`,
  `marshalStrings` / `unmarshalStrings`); migrations
  `hubd/internal/db/migrations/0007_require_ci.sql`, `0008_pinned.sql`.
- API: `hubd/internal/api/orchestrator.go` (project DTO, create handler, PATCH).
- Contract: `web/src/lib/contracts.ts`; UI: `web/src/components/board/ProjectForm.tsx`.
- Design ground truth: the project-requirements two-tier model (platform-invariant
  fail-closed + epic-level flag) with honesty-via-check-command.
