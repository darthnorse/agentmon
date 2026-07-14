# Epic #1 — Final whole-branch review

Segment: `git merge-base HEAD origin/main` (`1111e06`) .. HEAD — deliverable code
(`hubd/` + `web/`; planning docs excluded). Covers the frontend (Tasks 3-4) not seen
at Checkpoint 1, plus a re-review of the backend. This is the fixpoint.

```
═══ MULTI-REVIEW REPORT ═══
Diff: 21 files, 909 lines (whole branch; 10 files are 1-line ProjectDTO fixture additions)
Size: large — roster: specialist + simplifier + deep-scan + security + codex(gpt-5.5)
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓  codex(gpt-5.5) ✓
Validation: skipped (no FIX or DISCUSS findings)
Review-of-fixes: n/a (no fixes applied)

✅ FIXED & COMMITTED (0 items)

🗣️ NEEDS DISCUSSION (0 items) — none

🚫 REFUTED BY VALIDATION (0 items) — none

📝 NITPICKS (1 item) — cosmetic, left as-is
  i. web/src/components/board/ProjectForm.tsx:124 — requirement rows keyed by array
     index (`key={i}`) on a removable list.
     (Reviewers: specialist, simplifier, deep-scan — low/info, style)
     All three independently confirmed this is NOT a correctness bug: both inputs are
     fully controlled (`value={r.text}` / `value={r.check_cmd ?? ""}`), so on removing
     a middle row React re-applies each value from state and every surviving row shows
     the correct value — the "removes a row, keeping ids" test proves it. The only
     residual effect is the classic index-key footgun: browser-native focus/IME/
     selection could track DOM position rather than the logical row if an input is
     focused during a middle-row removal. A proper stable key needs client-side uid
     machinery (rows carry id="" until the server slugifies), and introducing
     crypto.randomUUID risks the jsdom/happy-dom test env — not worth it for an inert,
     rarely-edited settings form. Deferred deliberately; revisit if per-row uncontrolled
     state is ever added.
```

Tally: Fixed 0, 0 need discussion, 1 nitpick, 0 refuted.

## End-to-end verification (from the reviewers, for the audit trail)

- **Contract closes DB → API → TS → UI:** `db.Requirement` ↔ `projectDTO.Requirements`
  (`json:"requirements"`, no omitempty) ↔ `contracts.ts Requirement` +
  `ProjectDTO.requirements: Requirement[]` (required, non-null) ↔ `ProjectForm`. The
  required (non-null) TS type is correct: the `'[]'` column default +
  `marshalRequirements` (min `"[]"`) + `unmarshalRequirements` (non-nil for `"[]"`) +
  `normalizeRequirements` (non-nil `make`) guarantee the wire value is always `[]`,
  never `null` (specialist, simplifier, deep-scan, codex).
- **Form round-trip:** create + edit both send `requirements`; new rows carry `id:""`
  (server derives), existing rows keep their id on a text edit (slugify idempotent on a
  stored kebab id — the stable join key); edit mode renders existing rows from
  `init.requirements` (specialist, deep-scan).
- **Security:** requirement `text`/`check_cmd` render only through value-bound
  controlled `<Input>` (React-escaped native `<input>`); zero
  `dangerouslySetInnerHTML`/`innerHTML` in `web/src`; `check_cmd` is never executed or
  rendered as HTML client-side (inert this epic). Backend unchanged: parameterized
  JSON-in-TEXT SQL, 16 KiB body cap, fail-closed on duplicate ids, error echoes only
  the caller's own slugified id — no new endpoint or authz surface (security, codex).
- **No duplication introduced:** the requirements editor is genuinely different from
  the `required_reviews` free-text field; no existing list-editor/FieldArray helper in
  `web/src` to reuse (simplifier).
