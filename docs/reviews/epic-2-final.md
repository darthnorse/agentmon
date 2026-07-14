# Epic #2 — Final Whole-Branch Review

**Segment:** `f7bf2c0..HEAD` (whole epic branch; merge-base `f7bf2c0` = origin/main)
**Command:** `/multi-review f7bf2c0..HEAD --codex`
**Reviewed at:** `5cc692a` (fix commit applied on top of `02bdac9`)

```
═══ MULTI-REVIEW REPORT ═══
Diff: 9 files, 904 lines (304 code; 600 docs) — whole epic branch (Tasks 1-4)
Size: medium — roster: specialist + simplifier + deep-scan + security + codex(gpt-5.6-sol)
Reviewers: specialist ✓  simplifier ✓  deep-scan ✓  security ✓  codex(gpt-5.6-sol) ✓
Validation: findings verified empirically (multi-doc via failing→passing regression test); no separate validator dispatched
Review-of-fixes: ran (codex-only over 02bdac9..5cc692a) — 0 findings
```

## ✅ FIXED & COMMITTED (2 items, `5cc692a`)

1. **[codex, medium, input_validation] `hubd/internal/orchestrator/verdict.go` — multi-document YAML bypassed fail-closed validation.**
   `yaml.Unmarshal` decodes only the *first* YAML document in the fenced block
   and silently drops anything after a `---` separator. A block whose first
   document reported a requirement `met` and whose second document contradicted
   it (`unmet` / duplicate / junk) parsed as clean — `Decide` saw only the first
   `met` and could merge. This defeated the exact "escalate on ambiguity, don't
   resolve it" invariant the duplicate-id rejection was added to uphold (the code
   is otherwise internally inconsistent: rejects a dup id in one document, would
   accept the same id contradicted across two).
   **Confirmed empirically** with a failing→passing regression test
   (`TestParseVerdictRejectsMultiDocument`): the pre-fix code returned `nil` error
   on the multi-doc block. **Fix:** decode exactly one document via
   `yaml.NewDecoder` + `Decode(&v)`, then require the next `Decode` to return
   `io.EOF` (else reject as multiple-documents / propagate the error). Does not
   over-reject a normal single-document verdict (a leading `---` is still one
   document). All existing `ParseVerdict` tests stay green.

2. **[deep-scan, info, test robustness] `hubd/internal/orchestrator/orchestrator_test.go` — integration test did not pin the escalation reason.**
   `TestTickGateEnforcesPlatformRequirements` asserted only `stage=="escalated"`
   and `merged==0`. It was non-vacuous today (the fixture is built so the
   requirements check is the sole escalation source), but a future fixture change
   or a new unrelated gate check could silently mask a plumbing regression while
   the test still passed. **Fix:** the test now also asserts the escalation
   comment contains `platform requirements not met: always-use-rls (missing)`,
   pinning the reason to the requirements check.

## Clean lenses (0 findings each): specialist, simplifier, security

Independently verified across the three: the end-to-end chain closes
(`requirements` column → `scanProject`/`unmarshalRequirements` → `p.Requirements`
→ `GateInput` → `unmetRequirements` matched by id against ParseVerdict-validated
`v.Requirements`); `Decide`'s new check is dead-last with all existing
short-circuits byte-for-byte unmoved and `v` non-nil-guarded; `unmetRequirements`
is deterministic (iterates the project slice, map only for lookup) and dup-safe;
`ParseVerdict` fails closed on empty/dup id, out-of-domain status/via, and every
malformed YAML shape probed (non-list, null, scalar element, and — post-fix —
multi-document); the escalation `Reason` carries only kebab-slug ids + fixed enum
statuses to a private-repo issue comment (no injection/leak); no new dependency;
requirements loaded fresh every tick (no stale-cache fail-open); single production
`Decide` caller wired with `p.Requirements`.

## 🗣️ NEEDS DISCUSSION: none. 🚫 REFUTED: none. 📝 NITPICKS: none.

## Operational note (documented, not a defect)

Until **epic-03** ships (the runner actually emitting the `requirements:` verdict
block and running `check_cmd`), any project that *has* platform requirements will
have its epics fail the gate **closed** as `(missing)` — the runner never reports
them. This is the intended safe drift direction; projects with *no* platform
requirements are unaffected (backward compatible). Surfaced by the specialist and
security lenses.

**Fixed 2, 0 need discussion, 0 nitpicks, 0 refuted. `make test` green; `go vet` clean.**
