# Epic #1 — Checkpoint 1 review (backend seam)

Segment reviewed: `1111e06..6592984` (Task 1 storage + Task 2 API), code files only
(the plan doc was excluded — it was already cross-model reviewed at plan time).
Fixes applied on top: `0bb3e5e`. Reviewed to **`0bb3e5e`**.

```
═══ MULTI-REVIEW REPORT ═══
Diff: 7 files, 565 lines (2 commits: db storage + API)
Size: medium — roster: specialist + simplifier + deep-scan + security + codex(gpt-5.5)
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓  codex(gpt-5.5) ✓
Validation: 1 finding validated empirically via TDD (failing test → fix → passing test): 1 confirmed
Review-of-fixes: skipped (mechanical — 3-line reorder transcribing the recommended fix)

✅ FIXED & COMMITTED (1 item, 0bb3e5e)
  1. [1/5] hubd/internal/api/requirements.go:44 — Row with valid text silently dropped when an explicit id is unsluggable
     Reviewers: specialist
     Fix: normalizeRequirements now slugifies the supplied id, then falls back to
     slugify(text) when that is empty OR unsluggable, dropping the row only when the
     text too has no slug-able characters. Matches the function's documented intent.
     Regression test: TestNormalizeRequirementsUnsluggableIDFallsBackToText
     (fails on pre-fix code — row dropped to []; passes after).

🗣️ NEEDS DISCUSSION (0 items) — none

🚫 REFUTED BY VALIDATION (0 items) — none

📝 NITPICKS (1 item) — cosmetic/optional, left as-is
  i. hubd/internal/db/projects.go:131 — marshalRequirements/unmarshalRequirements
     could collapse with marshalStrings/unmarshalStrings via a Go generic pair.
     (Reviewers: simplifier; severity info)
     Left as-is deliberately: the per-type mirror matches the established
     required_reviews convention, and collapsing it would touch pre-existing
     required_reviews helpers — out of scope for this epic. The simplifier itself
     rated it optional ("leaving as-is is entirely defensible").
```

Tally: Fixed 1, 0 need discussion, 1 nitpick, 0 refuted.

## Non-findings explicitly verified (for the audit trail)

- **SQL injection:** none — requirements bound as a marshaled JSON string via `?`
  placeholders; `projectCols` is a compile-time constant (deep-scan, security).
- **Authz:** POST/PATCH inherit `RequireAuth` (router.go:56-58); the new field adds
  no endpoint or authz surface, and no client-supplied id feeds an access decision
  (security).
- **Error leakage:** the duplicate-id 400 reflects only a caller-derived `[a-z0-9-]`
  slug as JSON — no path/schema/secret/stack trace (security).
- **Resource bound:** the requirements array is bounded by the 16 KiB
  `maxOrchestratorBody` `MaxBytesReader` cap; no per-list cap needed (security).
- **`check_cmd` execution:** confirmed inert — zero read/exec sinks for `CheckCmd`
  repo-wide; command injection is not reachable this epic (security, deep-scan).
- **Cross-layer contract (DB→API):** column/scan/insert/update ordering consistent
  through the single `scanProject`/`projectCols` pair; JSON round-trips as `[]` for
  empty and never NULL on both DB and wire; PATCH preserves requirements when omitted
  (specialist, deep-scan). Web layer intentionally out of this segment.
