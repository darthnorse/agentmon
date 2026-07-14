# Requirements Merge Gate & Verdict Enforcement — Epic #2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking — the ticks are the resume state.

**Goal:** Make a project's platform requirements enforceable at the deterministic merge gate: extend the runner `Verdict` with a per-requirement results block, and make `Decide` fail closed unless every platform requirement declared on the project is reported `met`.

**Architecture:** Three tightly-coupled changes in the `orchestrator` package plus one plumbing line. (1) `verdict.go` gains a `VerdictRequirement` value type and a `Requirements` slice on `Verdict`, parsed from the same fenced-yaml block. (2) `gate.go` gains a `GateInput.Requirements []db.Requirement` field, a new `unmetRequirements` helper that mirrors the existing `missingReviews` pattern, and one appended check in `Decide` (dead last, preserving all existing short-circuits). (3) `orchestrator.go` passes `p.Requirements` into `GateInput` next to `RequiredReviews`. The gate iterates the *project's* requirement set (the authoritative source) and demands a matching `met` — a requirement absent from the verdict fails closed as `(missing)`, the safe drift direction.

**Tech Stack:** Go 1.x, `gopkg.in/yaml.v3`, standard `testing`. No new dependencies.

## Global Constraints

- **Build/test gate (must be green before EVERY commit):** `make test`
  — equivalently `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...`
  (use the `GOCACHE=` prefix; the default cache is read-only in the runner sandbox).
  Expected tail: `ok  	agentmon/hubd/internal/orchestrator` and `ok`/`no test files` for every other package, **no `FAIL`**.
- **Vet must pass** (CI runs it): `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/...` → no output.
- **Keep the diff gofmt-clean:** run `gofmt -w` on every edited `.go` file before committing; `gofmt -l hubd/internal/orchestrator/` must print nothing. (Adding `[]VerdictRequirement` widens the `Verdict` struct's tag-alignment column — gofmt realigns all its tags; paste the pre-aligned block below and let gofmt confirm.)
- **Commit style:** conventional prefixes (`feat(gate):`, `test(gate):`, `docs:`). **NEVER** add a `Co-Authored-By:` / AI-attribution trailer.
- **Scope discipline:** touch ONLY `hubd/internal/orchestrator/{verdict.go,gate.go,orchestrator.go}`, their `_test.go` siblings, this plan, `docs/reviews/epic-2-*.md`, and one learnings line in `CLAUDE.md`. Do NOT touch epic-01's storage/UI or epic-03's runner-side verdict production. **Backward compatibility is an acceptance criterion:** a project with no platform requirements MUST behave exactly as today — the existing `gate_test.go` / `verdict_test.go` cases stay byte-for-byte unchanged.

## Design decisions (from the epic body — restated so the implementer needn't re-read the issue)

- **Two tiers, two severities.** Platform requirements (`Project.Requirements`) = STOP: they fail-close the gate. Epic-level requirements = FLAG: they surface only through the existing review / unresolved-findings path and NEVER block via this new check. **Only `Project.Requirements` feeds the gate** — this epic wires nothing epic-level into `Decide`.
- **The project is authoritative, not the verdict.** `Decide` iterates the project's platform requirements and demands a matching `met` in the verdict — never the reverse. Consequence (accepted for v1, documented in code + `CLAUDE.md`): a requirement added to a project *after* its epics were imported — so the runner never reported it — makes the gate fail **closed** on the missing id. That is the safe drift direction.
- **Matching is by `id`** (the stable slug from epic-01's `db.Requirement.ID`).
- **Trust model unchanged.** The verdict is self-reported data the gate treats as data-not-argument. A `met` backed by `via: cmd` is trusted exactly as `tests.passed` already is — this epic does NOT re-run or authenticate check-commands (that is epic-03). `Via` is parsed and carried for humans but the gate does not branch on it.
- **CAPITALIZED-JSON caveat.** The `Verdict` struct carries only yaml tags, so `json.Marshal` (used by `SetEpicVerdict`) emits `Requirements` (capitalized). Nothing in this epic reads persisted verdict JSON back, but any future reader must use the capitalized key. This is documented as a learnings line in Task 5.
- **Generalize `missingReviews`, don't fork it.** The new `unmetRequirements` helper mirrors `missingReviews`'s shape (pure function, diffs required-vs-reported, returns the offenders for the reason string) rather than inventing a parallel mechanism.
- **RESOLVED AMBIGUITY — where enum validation lives (flag at plan-gate).** The AC says `Requirements` is parsed "with the same fail-closed malformed-input handling as the rest of `ParseVerdict`." This is readable two ways: (A) `ParseVerdict` hard-rejects the whole verdict on an out-of-enum `status`/`via` (like it rejects a bad schema or negative counts); (B) `ParseVerdict` only inherits the YAML-syntax fail-closed, and `Decide` enforces the `met` semantics, treating any non-`met` status as failing. **This plan takes (B)**, because: the `Decide` AC explicitly locates the requirement decision in `Decide` ("escalates when any platform requirement id … is not present-and-`met`") and demands the reason "names the offending ids (unmet / uncertain / missing)" — a per-id, per-category reason that a whole-verdict rejection in `ParseVerdict` cannot produce. Under (B), the three named categories (`unmet`, `uncertain`, `missing`) and any garbage status ALL fail closed with a specific, honest reason, and existing malformed-YAML fail-closed is untouched. Both readings are safe (fail-closed); (B) is strictly more informative and matches every explicit AC. Recorded here for human confirmation at the plan-gate.

## File Structure (dispositions verified against the repo at `origin/main` = `f7bf2c0`)

| File | Disposition | Responsibility |
|---|---|---|
| `hubd/internal/orchestrator/verdict.go` | **Modify** | Add `VerdictRequirement` type + `Verdict.Requirements` field. |
| `hubd/internal/orchestrator/verdict_test.go` | **Modify** | Add `TestParseVerdictRequirements`. Existing tests unchanged. |
| `hubd/internal/orchestrator/gate.go` | **Modify** | Add `db` import, `GateInput.Requirements`, `unmetRequirements`, one appended `Decide` check. |
| `hubd/internal/orchestrator/gate_test.go` | **Modify** | Add `db` import + `TestDecideRequirements`. Existing tests unchanged. |
| `hubd/internal/orchestrator/orchestrator.go` | **Modify** | Pass `p.Requirements` into the one `GateInput` at line ~646. |
| `docs/plans/epic-2.md` | (this file) | The plan/resume state. |
| `docs/reviews/epic-2-*.md` | **Create** (checkpoint/final) | Review evidence. |
| `CLAUDE.md` | **Modify** (Task 5) | One learnings line: requirements drift direction + capitalized-JSON key. |

**Reference facts pinned from the repo (do not re-derive):**
- `db.Requirement` is `struct { ID string \`json:"id"\`; Text string \`json:"text"\`; CheckCmd string \`json:"check_cmd,omitempty"\` }` in `hubd/internal/db/projects.go`.
- `Project.Requirements` is `[]db.Requirement` (epic-01, merged in PR #4).
- `db` import path: `agentmon/hubd/internal/db`. No import cycle — `db` does not import `orchestrator`, and `orchestrator.go` already imports `db`.
- The ONLY non-test `GateInput`/`Decide` construction site is `orchestrator.go:646`.
- `cleanVerdict()` (in `gate_test.go`) returns `Epic:15`, all four reviews, `Tests.Passed:10`, no requirements — reuse it; leave `Epic` unset (0) in new cases so the epic-binding check is skipped.

---

## Task 1: Verdict schema — `VerdictRequirement` type + `Requirements` field (TDD)

**Files:**
- Modify: `hubd/internal/orchestrator/verdict.go`
- Test: `hubd/internal/orchestrator/verdict_test.go`

**Interfaces:**
- Produces: `type VerdictRequirement struct { ID, Status, Via string }` (yaml tags `id`/`status`/`via`) and `Verdict.Requirements []VerdictRequirement` (yaml tag `requirements`). Consumed by Task 2 (`unmetRequirements`) and Tasks 2/4 (tests).

- [ ] **Step 1: Write the failing test.** Append to `hubd/internal/orchestrator/verdict_test.go`:

```go
func TestParseVerdictRequirements(t *testing.T) {
	body := "```yaml\n" +
		"agentmon-verdict: v1\nepic: 1\n" +
		"requirements:\n" +
		"  - { id: always-use-rls, status: met, via: cmd }\n" +
		"  - { id: wcag, status: uncertain, via: review }\n" +
		"tests: { passed: 1, failed: 0 }\n```"
	v, err := ParseVerdict(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Requirements) != 2 ||
		v.Requirements[0] != (VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"}) ||
		v.Requirements[1] != (VerdictRequirement{ID: "wcag", Status: "uncertain", Via: "review"}) {
		t.Fatalf("requirements = %+v", v.Requirements)
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS (compile error — the field/type don't exist yet).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestParseVerdictRequirements 2>&1 | tail`
Expected: FAIL — build error `undefined: VerdictRequirement` / `v.Requirements undefined`.

- [ ] **Step 3: Add the type and field.** In `hubd/internal/orchestrator/verdict.go`, add the `VerdictRequirement` type immediately after the `VerdictTests` type (after line ~25):

```go
// VerdictRequirement is the runner's self-reported result for one platform
// requirement, keyed by the project Requirement's stable ID (epic-01's slug).
// Status is met | unmet | uncertain; Via records how it was certified —
// cmd (a check-command exit code) or review (a reviewer's judgment) — and is
// carried for humans only: the gate trusts a `met` regardless of Via (v1 trust
// model: PR body editable only by owner + runners).
type VerdictRequirement struct {
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
	Via    string `yaml:"via"`
}
```

Then add the `Requirements` field to the `Verdict` struct. Replace the whole struct (gofmt realigns the tag column because `[]VerdictRequirement` is the new widest type):

```go
// Verdict is the runner's structured self-report, embedded as the last
// ```yaml block of the PR body. The gate treats it as data, not argument.
type Verdict struct {
	Schema           string               `yaml:"agentmon-verdict"`
	Epic             int                  `yaml:"epic"`
	Reviews          []string             `yaml:"reviews"`
	Findings         VerdictFindings      `yaml:"findings"`
	Unresolved       []string             `yaml:"unresolved"`
	Tests            VerdictTests         `yaml:"tests"`
	Requirements     []VerdictRequirement `yaml:"requirements"`
	Uncertain        bool                 `yaml:"uncertain"`
	LearningsUpdated bool                 `yaml:"learnings_updated"`
}
```

Do NOT add any enum validation to `ParseVerdict` — per design decision (B), status semantics are enforced in `Decide` (Task 2). `ParseVerdict` keeps its existing schema/negative-count checks and inherits YAML-syntax fail-closed for a malformed `requirements` block.

- [ ] **Step 4: gofmt + run the test to verify it PASSES.**

Run: `gofmt -w hubd/internal/orchestrator/verdict.go && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestParseVerdictRequirements -v 2>&1 | tail`
Expected: `--- PASS: TestParseVerdictRequirements` then `PASS` / `ok`.

- [ ] **Step 5: Full gate green, then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.

```bash
git add hubd/internal/orchestrator/verdict.go hubd/internal/orchestrator/verdict_test.go
git commit -m "feat(verdict): parse per-requirement results block"
```

---

## Task 2: Gate enforcement — `GateInput.Requirements`, `unmetRequirements`, `Decide` check (TDD)

**Files:**
- Modify: `hubd/internal/orchestrator/gate.go`
- Test: `hubd/internal/orchestrator/gate_test.go`

**Interfaces:**
- Consumes: `VerdictRequirement`, `Verdict.Requirements` (Task 1); `db.Requirement` (repo).
- Produces: `GateInput.Requirements []db.Requirement`; `unmetRequirements(required []db.Requirement, reported []VerdictRequirement) []string`. Consumed by Task 3 (plumbing) and Task 4 (tests).

- [ ] **Step 1: Write the failing test.** Append to `hubd/internal/orchestrator/gate_test.go` (and add the `db` import — see Step 3):

```go
func TestDecideRequirements(t *testing.T) {
	reqs := []db.Requirement{{ID: "always-use-rls", Text: "Always use RLS"}, {ID: "wcag", Text: "WCAG 2.2 AA"}}
	withReqs := func(rs ...VerdictRequirement) *Verdict {
		v := cleanVerdict()
		v.Requirements = rs
		return v
	}
	allMet := []VerdictRequirement{
		{ID: "always-use-rls", Status: "met", Via: "cmd"},
		{ID: "wcag", Status: "met", Via: "review"},
	}
	cases := []struct {
		name   string
		in     GateInput
		merge  bool
		reason string
	}{
		{"all met merges", GateInput{Verdict: withReqs(allMet...), Requirements: reqs, ChecksGreen: true}, true, ""},
		{"no platform reqs unchanged", GateInput{Verdict: cleanVerdict(), Requirements: nil, ChecksGreen: true}, true, ""},
		{"one unmet escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "unmet", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, "wcag (unmet)"},
		{"uncertain escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "uncertain", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "met", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, "always-use-rls (uncertain)"},
		{"absent from verdict escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"}),
			Requirements: reqs, ChecksGreen: true}, false, "wcag (missing)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Merge != c.merge {
				t.Fatalf("merge = %v, got %+v", got.Merge, got)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("reason %q missing %q", got.Reason, c.reason)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS (compile error — `db` import unused / `GateInput.Requirements` and `unmetRequirements` don't exist).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestDecideRequirements 2>&1 | tail`
Expected: FAIL — build error `unknown field Requirements in struct literal` (and `db` undefined until the import is added in Step 3).

- [ ] **Step 3: Add the `db` import, the `GateInput` field, the `Decide` check, and the helper.** In `hubd/internal/orchestrator/gate.go`:

Replace the import block:

```go
import (
	"fmt"
	"slices"
	"strings"

	"agentmon/hubd/internal/db"
)
```

Add the `Requirements` field to `GateInput`, directly after `RequiredReviews`:

```go
type GateInput struct {
	Verdict         *Verdict
	VerdictErr      error
	Epic            int // expected issue number; 0 skips the binding check
	Labels          []string
	RequiredReviews []string
	Requirements    []db.Requirement // project platform requirements; each must be reported `met`
	ChecksGreen     bool
	ChecksPending   bool
}
```

In `Decide`, insert the requirements check as the LAST check, immediately before `return GateResult{Merge: true}` (i.e. after the `missingReviews` block). Do NOT move any existing check — ordering must stay checks-pending → pr-gate → verdict presence → epic binding → CI → uncertainty → unresolved → tests → required reviews → **requirements**:

```go
	if missing := missingReviews(in.RequiredReviews, v.Reviews); len(missing) > 0 {
		return GateResult{Reason: "missing required reviews: " + strings.Join(missing, ", ")}
	}
	if unmet := unmetRequirements(in.Requirements, v.Requirements); len(unmet) > 0 {
		return GateResult{Reason: "platform requirements not met: " + strings.Join(unmet, ", ")}
	}
	return GateResult{Merge: true}
```

Add the helper immediately after `missingReviews`:

```go
// unmetRequirements mirrors missingReviews for platform requirements: given the
// project's platform requirement set and the verdict's self-reported results, it
// returns one "id (category)" token for every requirement not present-and-`met`.
// It fails closed on drift — a requirement the runner never reported (e.g. added
// to the project after its epics were imported) surfaces as "(missing)", the
// safe direction. Via is not consulted: a `met` is trusted regardless of how it
// was certified (v1 trust model). The category is the reported status verbatim,
// so unmet / uncertain / a garbage value are all surfaced honestly in the reason.
func unmetRequirements(required []db.Requirement, reported []VerdictRequirement) []string {
	status := make(map[string]string, len(reported))
	for _, r := range reported {
		status[r.ID] = r.Status
	}
	var unmet []string
	for _, req := range required {
		switch s, ok := status[req.ID]; {
		case !ok:
			unmet = append(unmet, req.ID+" (missing)")
		case s == "met":
			// satisfied
		case s == "":
			unmet = append(unmet, req.ID+" (unspecified)")
		default:
			unmet = append(unmet, req.ID+" ("+s+")")
		}
	}
	return unmet
}
```

- [ ] **Step 4: Add the `db` import to the test file.** In `hubd/internal/orchestrator/gate_test.go`, the import block becomes:

```go
import (
	"strings"
	"testing"

	"agentmon/hubd/internal/db"
)
```

- [ ] **Step 5: gofmt + run the test to verify it PASSES.**

Run: `gofmt -w hubd/internal/orchestrator/gate.go hubd/internal/orchestrator/gate_test.go && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestDecideRequirements -v 2>&1 | tail -20`
Expected: all five subtests `--- PASS`, then `ok`.

- [ ] **Step 6: Verify existing gate/verdict tests still pass unchanged (backward compat).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run 'TestDecide|TestParseVerdict' -v 2>&1 | tail -30`
Expected: `TestDecide`, `TestDecideBindsVerdictToEpic`, and all `TestParseVerdict*` PASS.

- [ ] **Step 7: Full gate + vet green, then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.
Run: `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/... 2>&1 | tail` → no output.

```bash
git add hubd/internal/orchestrator/gate.go hubd/internal/orchestrator/gate_test.go
git commit -m "feat(gate): fail closed unless every platform requirement is met"
```

---

- [ ] **CHECKPOINT 1 — after Task 2 (highest-judgment: gate semantics, ordering, fail-closed drift).**
  This is the seam right after the core enforcement logic and its data layer, before the trivial plumbing. Follow Step 6 of the epic-pipeline:
  1. `agentmon report --epic 2 --stage reviewing`
  2. Segment = `git merge-base HEAD origin/main`..HEAD.
  3. Run `/multi-review <segment-base>..HEAD --codex` in the session. It applies + commits validated FIXes itself; verify the suite is still green afterward.
  4. DISCUSS items → escalate; NITPICKs → record only.
  5. Write the consolidated report to `docs/reviews/epic-2-cp1.md`, tick this box as `- [x] CHECKPOINT 1 — reviewed to <sha>`, and commit both with `docs: epic #2 checkpoint 1 review`.
  6. `agentmon report --epic 2 --stage implementing` and continue.

---

## Task 3: Plumbing — pass `p.Requirements` into `GateInput`

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (the `Decide` call at ~line 646)

**Interfaces:**
- Consumes: `GateInput.Requirements` (Task 2); `db.Project.Requirements` (repo). No new symbols produced.

This is a pure wiring change; it is covered end-to-end by Task 2's `Decide` tests (the run loop constructs the same `GateInput`). No new unit test — the run loop has no isolated harness and a full-orchestrator integration test is out of scope for this epic. (If `go vet`/compile is happy and the `Decide` tests pass, the wiring is exercised.)

- [ ] **Step 1: Add `Requirements: p.Requirements` to the `Decide` call.** Replace the two-line call (currently at ~646–647):

```go
		res := Decide(GateInput{Verdict: v, VerdictErr: verr, Epic: e.IssueNumber, Labels: e.Labels,
			RequiredReviews: p.RequiredReviews, Requirements: p.Requirements, ChecksGreen: green, ChecksPending: pending})
```

- [ ] **Step 2: gofmt + full gate + vet green.**

Run: `gofmt -w hubd/internal/orchestrator/orchestrator.go && GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.
Run: `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/... 2>&1 | tail` → no output.

- [ ] **Step 3: Commit.**

```bash
git add hubd/internal/orchestrator/orchestrator.go
git commit -m "feat(orchestrator): wire project requirements into the merge gate"
```

---

## Task 4: Learnings write-back

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Add one learnings line to `CLAUDE.md`.** Under the `## Conventions` section, immediately after the existing "Verdict JSON keys are CAPITALIZED" bullet, add:

```markdown
- **Platform requirements fail the gate closed:** `Decide` iterates
  `Project.Requirements` and escalates unless each id is reported `met` in the
  verdict's `Requirements` block (`json.Marshal` emits the CAPITALIZED
  `Requirements` key). A requirement added *after* an epic was imported — so the
  runner never reported it — fails **closed** as `(missing)`; that is the
  intended safe drift direction, not a bug.
```

- [ ] **Step 2: Commit.**

```bash
git add CLAUDE.md
git commit -m "docs: platform requirements fail the merge gate closed"
```

---

## Finish (epic-pipeline Step 7 — the final whole-branch review + PR)

- [ ] Rebase onto the moved base: `git fetch origin && git rebase origin/main`. Unresolvable conflict → escalate.
- [ ] `agentmon report --epic 2 --stage reviewing`, then final review: `/multi-review $(git merge-base HEAD origin/main)..HEAD --codex`. Apply FIXes (already committed by the review), escalate DISCUSS items. Commit the report to `docs/reviews/epic-2-final.md`.
- [ ] Full suite one last time: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` — record exact pass/fail counts for the verdict block.
- [ ] Push `epic/2-requirements-merge-gate-verdict` and open the PR with `Closes #2` and the fenced `agentmon-verdict: v1` block (epic 2; `reviews: [specialist, simplifier, deep-scan, codex, cross-model]`; findings from the FINAL review only; `uncertain: false` only if no material doubt remains).
- [ ] `agentmon report --epic 2 --stage pr_open --pr <PR-number>`.

## Self-Review (checked against the epic AC)

- ✅ `Verdict` gains `Requirements` (`{id, status, via}`), parsed from the same fenced-yaml block, inheriting fail-closed malformed handling → Task 1.
- ✅ `Decide` escalates when any platform id is not present-and-`met`; reason names offending ids (unmet / uncertain / missing) → Task 2 (`unmetRequirements` + check) with matching test cases.
- ✅ Merges only when every platform id is `met` and all pre-existing conditions pass → "all met merges" case + appended (non-reordering) check.
- ✅ Existing ordering/short-circuits preserved; check is dead last → Task 2 Step 3 explicitly appends after `missingReviews`, moves nothing.
- ✅ Backward compatible — no-platform-reqs unchanged; existing tests untouched → "no platform reqs unchanged" case + existing tests asserted green in Task 2 Step 6.
- ✅ Run loop passes `Project.Requirements` into `GateInput` next to `RequiredReviews` → Task 3.
- ✅ New `gate_test` cases: all-met→merge, one-unmet→escalate, uncertain→escalate, absent→escalate, no-platform-reqs→unchanged → Task 2 Step 1 (all five present).
- ✅ `make test` green → gate command in Global Constraints, run before every commit.
- ✅ Generalizes `missingReviews` pattern → `unmetRequirements` mirrors it.
- ✅ Trust model unchanged; `Via` carried but not gated → design decisions + helper comment.
