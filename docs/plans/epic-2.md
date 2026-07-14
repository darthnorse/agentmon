# Requirements Merge Gate & Verdict Enforcement — Epic #2 Implementation Plan

> **For the epic-pipeline runner:** execute this plan task-by-task, in order, per epic-pipeline Step 5 — tick each `- [ ]` checkbox as you complete it (the ticks are your resume state), and run the `CHECKPOINT` review where marked. Steps are bite-sized (2–5 min); every code step shows the exact code.

**Goal:** Make a project's platform requirements enforceable at the deterministic merge gate: extend the runner `Verdict` with a validated per-requirement results block, and make `Decide` fail closed unless every platform requirement declared on the project is reported `met`.

**Architecture:** Three tightly-coupled changes in the `orchestrator` package plus one plumbing line. (1) `verdict.go` gains a `VerdictRequirement` value type and a `Requirements` slice on `Verdict`; `ParseVerdict` validates each entry (non-empty id, `status ∈ {met,unmet,uncertain}`, `via ∈ {cmd,review}`, no duplicate ids) and fails closed on anything out of domain, exactly as it already does for a bad schema or negative counts. (2) `gate.go` gains a `GateInput.Requirements []db.Requirement` field, a new `unmetRequirements` helper mirroring the existing `missingReviews` pattern, and one appended check in `Decide` (dead last, preserving all existing short-circuits). (3) `orchestrator.go` passes `p.Requirements` into `GateInput` next to `RequiredReviews`. The gate iterates the *project's* requirement set (the authoritative source) and demands a matching `met` — a requirement absent from the verdict fails closed as `(missing)`, the safe drift direction.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, standard `testing`. No new dependencies.

## Global Constraints

- **Build/test gate (must be green before EVERY commit — including docs-only commits):** `make test`
  — equivalently `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...`
  (use the `GOCACHE=` prefix; the default cache is read-only in the runner sandbox).
  Expected tail: `ok  	agentmon/hubd/internal/orchestrator` and `ok`/`no test files` for every other package, **no `FAIL`**.
- **Vet must pass** (CI runs it): `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/...` → no output.
- **Keep the diff gofmt-clean:** run `gofmt -w` on every edited `.go` file before committing; `gofmt -l hubd/internal/orchestrator/` must print nothing. (Adding `[]VerdictRequirement` widens the `Verdict` struct's tag-alignment column — gofmt realigns all its tags; paste the pre-aligned block below and let gofmt confirm.)
- **Commit style:** conventional prefixes (`feat(gate):`, `test(gate):`, `docs:`). **NEVER** add a `Co-Authored-By:` / AI-attribution trailer.
- **Scope discipline:** touch ONLY `hubd/internal/orchestrator/{verdict.go,gate.go,orchestrator.go}`, their `_test.go` siblings, this plan, `docs/reviews/epic-2-*.md`, and one learnings line in `CLAUDE.md`. Do NOT touch epic-01's storage/UI or epic-03's runner-side verdict production. **Backward compatibility is an acceptance criterion:** a project with no platform requirements MUST behave exactly as today — the existing `gate_test.go` / `verdict_test.go` / `orchestrator_test.go` cases stay byte-for-byte unchanged (verified: existing verdicts carry no `requirements:` block, so the new validation loop is a no-op for them).

## Design decisions (from the epic body — restated so the implementer needn't re-read the issue)

- **Two tiers, two severities.** Platform requirements (`Project.Requirements`) = STOP: they fail-close the gate. Epic-level requirements = FLAG: they surface only through the existing review / unresolved-findings path and NEVER block via this new check. **Only `Project.Requirements` feeds the gate** — this epic wires nothing epic-level into `Decide`.
- **The project is authoritative, not the verdict.** `Decide` iterates the project's platform requirements and demands a matching `met` in the verdict — never the reverse. Consequence (accepted for v1, documented in code + `CLAUDE.md`): a requirement added to a project *after* its epics were imported — so the runner never reported it — makes the gate fail **closed** on the missing id. That is the safe drift direction.
- **Matching is by `id`** (the stable slug from epic-01's `db.Requirement.ID`).
- **Trust model unchanged.** The verdict is self-reported data the gate treats as data-not-argument. A `met` is trusted the same way `tests.passed` already is — this epic does NOT re-run or authenticate check-commands (that is epic-03). `Via` is parsed, validated as in-domain, and carried for humans; the gate does not *branch* on it (a `met` is trusted regardless of whether via is `cmd` or `review`), but an out-of-domain via still fails the whole verdict closed as malformed (next bullet).
- **Enum/shape validation lives in `ParseVerdict` — RESOLVED via cross-model review (was flagged ambiguous).** The AC says `Requirements` is parsed "with the same fail-closed malformed-input handling as the rest of `ParseVerdict`," and constrains `status ∈ {met,unmet,uncertain}` and `via ∈ {cmd,review}`. An out-of-domain `status`/`via`, an empty `id`, or a **duplicate `id`** is therefore malformed input and is rejected by `ParseVerdict` (returning an error → the gate's existing "missing or malformed verdict" escalation), exactly as a bad schema version or a negative count is. Rationale: (a) it is the literal reading of "same fail-closed malformed handling"; (b) it closes two fail-open holes a laxer reading leaves — a `met` with a garbage/absent `via` would otherwise merge (the gate ignores via), and duplicate ids under a last-write-wins map would let a contradictory `unmet`→`met` pair merge; (c) it does NOT weaken the AC's requirement that `Decide` name offending ids — the *valid-but-not-met* categories (`unmet`, `uncertain`) and *missing* ids are still surfaced per-id by `Decide`. With validation upstream, `Decide` only ever sees in-domain, dup-free requirements, so `unmetRequirements` stays a clean two-branch diff. *(This reverses an earlier draft that put semantics only in `Decide`; the cross-model reviewer correctly flagged the fail-open. Recorded for human confirmation at the plan-gate.)*
- **CAPITALIZED-JSON caveat.** The `Verdict` struct carries only yaml tags, so `json.Marshal` (used by `SetEpicVerdict`) emits `Requirements` (capitalized). Nothing in this epic reads persisted verdict JSON back, but any future reader must use the capitalized key. Documented as a learnings line in Task 4.
- **Generalize `missingReviews`, don't fork it.** The new `unmetRequirements` helper mirrors `missingReviews`'s shape (pure function, diffs required-vs-reported, returns the offenders for the reason string) rather than inventing a parallel mechanism.

## File Structure (dispositions verified against the repo at `origin/main` = `f7bf2c0`)

| File | Disposition | Responsibility |
|---|---|---|
| `hubd/internal/orchestrator/verdict.go` | **Modify** | Add `VerdictRequirement` type + `Verdict.Requirements` field + `ParseVerdict` validation. |
| `hubd/internal/orchestrator/verdict_test.go` | **Modify** | Add `TestParseVerdictRequirements` + `TestParseVerdictRejectsBadRequirements`. Existing tests unchanged. |
| `hubd/internal/orchestrator/gate.go` | **Modify** | Add `db` import, `GateInput.Requirements`, `unmetRequirements`, one appended `Decide` check. |
| `hubd/internal/orchestrator/gate_test.go` | **Modify** | Add `db` import + `TestDecideRequirements`. Existing tests unchanged. |
| `hubd/internal/orchestrator/orchestrator.go` | **Modify** | Pass `p.Requirements` into the one `GateInput` at line ~646. |
| `hubd/internal/orchestrator/orchestrator_test.go` | **Modify** | Add `TestTickGateEnforcesPlatformRequirements` (end-to-end plumbing coverage). Existing tests unchanged. |
| `docs/plans/epic-2.md` | (this file) | The plan/resume state. |
| `docs/reviews/epic-2-*.md` | **Create** (checkpoint/final) | Review evidence. |
| `CLAUDE.md` | **Modify** (Task 4) | One learnings line: requirements drift direction + capitalized-JSON key. |

**Reference facts pinned from the repo (do not re-derive):**
- `db.Requirement` is `struct { ID string \`json:"id"\`; Text string \`json:"text"\`; CheckCmd string \`json:"check_cmd,omitempty"\` }` in `hubd/internal/db/projects.go`.
- `Project.Requirements` is `[]db.Requirement` (epic-01, merged in PR #4). `db.UpdateProject` persists `requirements` (and preserves `max_parallel`/`paused`/`require_ci`/`pinned`, which it does not rewrite).
- `db` import path: `agentmon/hubd/internal/db`. No import cycle — `db` does not import `orchestrator`, and `orchestrator.go`/`orchestrator_test.go` already import `db`.
- The ONLY non-test `GateInput`/`Decide` construction site is `orchestrator.go:646`.
- `cleanVerdict()` (in `gate_test.go`) returns `Epic:15`, all four reviews, `Tests.Passed:10`, no requirements — reuse it; leave `Epic` unset (0) in new cases so the epic-binding check is skipped.
- Run-loop test harness in `orchestrator_test.go`: `newTestOrch(t, gh, ag)` creates project `p1` (repo `o/r`, no requirements); `fakeGH{issues, prs, checks}`; `TestReportsAdvanceAndGateMerges` is the pattern to mirror (two `o.Tick(ctx)` calls: spawn, then drain→pr_open→gate). A gate escalation sets `epic.Stage == "escalated"` and leaves `gh.merged` empty.

---

## Task 1: Verdict schema + validation — `VerdictRequirement`, `Requirements`, fail-closed parse (TDD)

**Files:**
- Modify: `hubd/internal/orchestrator/verdict.go`
- Test: `hubd/internal/orchestrator/verdict_test.go`

**Interfaces:**
- Produces: `type VerdictRequirement struct { ID, Status, Via string }` (yaml tags `id`/`status`/`via`) and `Verdict.Requirements []VerdictRequirement` (yaml tag `requirements`). `ParseVerdict` now rejects out-of-domain requirement entries. Consumed by Task 2 (`unmetRequirements`, gate tests) and Task 3 (integration test).

- [x] **Step 1: Write the failing happy-path + malformed tests.** Append both to `hubd/internal/orchestrator/verdict_test.go`:

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

func TestParseVerdictRejectsBadRequirements(t *testing.T) {
	base := "```yaml\nagentmon-verdict: v1\nepic: 1\ntests: { passed: 1, failed: 0 }\nrequirements:\n"
	cases := map[string]string{
		"invalid status": "  - { id: rls, status: done, via: cmd }\n",
		"invalid via":     "  - { id: rls, status: met, via: magic }\n",
		"empty id":        "  - { id: \"\", status: met, via: cmd }\n",
		"duplicate id":    "  - { id: rls, status: met, via: cmd }\n  - { id: rls, status: unmet, via: review }\n",
	}
	for name, reqs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseVerdict(base + reqs + "```"); err == nil {
				t.Fatalf("%s must fail closed", name)
			}
		})
	}
}
```

- [x] **Step 2: Run the tests to verify they FAIL (compile error — the field/type don't exist yet).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run 'TestParseVerdictRequirements|TestParseVerdictRejectsBadRequirements' 2>&1 | tail`
Expected: FAIL — build error `undefined: VerdictRequirement` / `v.Requirements undefined`.

- [x] **Step 3: Add the type, field, and validation.** In `hubd/internal/orchestrator/verdict.go`, add the `VerdictRequirement` type immediately after the `VerdictTests` type (after line ~25):

```go
// VerdictRequirement is the runner's self-reported result for one platform
// requirement, keyed by the project Requirement's stable ID (epic-01's slug).
// Status is met | unmet | uncertain; Via records how it was certified —
// cmd (a check-command exit code) or review (a reviewer's judgment) — and is
// carried for humans only: the gate trusts a `met` regardless of Via (v1 trust
// model: PR body editable only by owner + runners). ParseVerdict validates all
// three fields, so anything reaching the gate is in-domain and dup-free.
type VerdictRequirement struct {
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
	Via    string `yaml:"via"`
}
```

Then replace the whole `Verdict` struct (gofmt realigns the tag column because `[]VerdictRequirement` is the new widest type):

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

Finally, in `ParseVerdict`, add the validation loop immediately AFTER the existing negative-counts check and immediately BEFORE `return &v, nil`:

```go
		// Requirements are validated like the rest of the verdict: fail closed on
		// anything out of the v1 domain so a malformed self-report never reads as
		// clean. Duplicate ids are rejected because a last-write-wins map in the
		// gate would let a contradictory pair (e.g. unmet then met) merge — the
		// gate must escalate on ambiguity, not resolve it. Status/via enums mirror
		// the project-side Requirement contract from epic-01.
		seen := make(map[string]bool, len(v.Requirements))
		for _, r := range v.Requirements {
			if r.ID == "" {
				return nil, fmt.Errorf("orchestrator: verdict requirement with empty id")
			}
			if seen[r.ID] {
				return nil, fmt.Errorf("orchestrator: duplicate verdict requirement id %q", r.ID)
			}
			seen[r.ID] = true
			switch r.Status {
			case "met", "unmet", "uncertain":
			default:
				return nil, fmt.Errorf("orchestrator: invalid requirement status %q for id %q", r.Status, r.ID)
			}
			switch r.Via {
			case "cmd", "review":
			default:
				return nil, fmt.Errorf("orchestrator: invalid requirement via %q for id %q", r.Via, r.ID)
			}
		}
```

- [x] **Step 4: gofmt + run the tests to verify they PASS.**

Run: `gofmt -w hubd/internal/orchestrator/verdict.go && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run 'TestParseVerdictRequirements|TestParseVerdictRejectsBadRequirements' -v 2>&1 | tail -20`
Expected: `TestParseVerdictRequirements` PASS and all four `TestParseVerdictRejectsBadRequirements` subtests PASS, then `ok`.

- [x] **Step 5: Full gate green (existing verdict tests must be untouched), then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.

```bash
git add hubd/internal/orchestrator/verdict.go hubd/internal/orchestrator/verdict_test.go
git commit -m "feat(verdict): parse and validate per-requirement results block"
```

---

## Task 2: Gate enforcement — `GateInput.Requirements`, `unmetRequirements`, `Decide` check (TDD)

**Files:**
- Modify: `hubd/internal/orchestrator/gate.go`
- Test: `hubd/internal/orchestrator/gate_test.go`

**Interfaces:**
- Consumes: `VerdictRequirement`, `Verdict.Requirements` (Task 1); `db.Requirement` (repo).
- Produces: `GateInput.Requirements []db.Requirement`; `unmetRequirements(required []db.Requirement, reported []VerdictRequirement) []string`. Consumed by Task 3 (plumbing + integration test).

- [x] **Step 1: Write the failing test.** Append to `hubd/internal/orchestrator/gate_test.go` (and add the `db` import — see Step 4):

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
		wait   bool
		reason string
	}{
		{"all met merges", GateInput{Verdict: withReqs(allMet...), Requirements: reqs, ChecksGreen: true}, true, false, ""},
		{"no platform reqs unchanged", GateInput{Verdict: cleanVerdict(), Requirements: nil, ChecksGreen: true}, true, false, ""},
		{"one unmet escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "unmet", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "wcag (unmet)"},
		{"uncertain escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "uncertain", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "met", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "always-use-rls (uncertain)"},
		{"absent from verdict escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "wcag (missing)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Merge != c.merge || got.Wait != c.wait {
				t.Fatalf("merge/wait = %v/%v, got %+v", c.merge, c.wait, got)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("reason %q missing %q", got.Reason, c.reason)
			}
		})
	}
}
```

- [x] **Step 2: Run the test to verify it FAILS.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestDecideRequirements 2>&1 | tail`
Expected: FAIL — build error `unknown field Requirements in struct literal` (and `db` undefined until the import is added in Step 4).

- [x] **Step 3: Add the `db` import, the `GateInput` field, the `Decide` check, and the helper.** In `hubd/internal/orchestrator/gate.go`:

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
// A requirement the runner never reported (e.g. added to the project after its
// epics were imported) surfaces as "(missing)" — failing closed, the safe drift
// direction. Via is not consulted: a `met` is trusted regardless of how it was
// certified (v1 trust model). Inputs are pre-validated by ParseVerdict, so a
// present-not-met status is always "unmet" or "uncertain", surfaced verbatim.
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
		case s != "met":
			unmet = append(unmet, req.ID+" ("+s+")")
		}
	}
	return unmet
}
```

- [x] **Step 4: Add the `db` import to the test file.** In `hubd/internal/orchestrator/gate_test.go`, the import block becomes:

```go
import (
	"strings"
	"testing"

	"agentmon/hubd/internal/db"
)
```

- [x] **Step 5: gofmt + run the new test to verify it PASSES.**

Run: `gofmt -w hubd/internal/orchestrator/gate.go hubd/internal/orchestrator/gate_test.go && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestDecideRequirements -v 2>&1 | tail -20`
Expected: all five subtests `--- PASS`, then `ok`.

- [x] **Step 6: Verify existing gate/verdict tests still pass unchanged (backward compat).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run 'TestDecide|TestParseVerdict' -v 2>&1 | tail -40`
Expected: `TestDecide`, `TestDecideBindsVerdictToEpic`, and all `TestParseVerdict*` PASS.

- [x] **Step 7: Full gate + vet green, then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.
Run: `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/... 2>&1 | tail` → no output.

```bash
git add hubd/internal/orchestrator/gate.go hubd/internal/orchestrator/gate_test.go
git commit -m "feat(gate): fail closed unless every platform requirement is met"
```

---

- [x] **CHECKPOINT 1 — reviewed to 50cb5d3** — after Task 2 (highest-judgment: gate semantics, ordering, fail-closed drift + parse-layer validation).
  This is the seam right after the core enforcement logic and its data/validation layer, before the plumbing + its integration test. Follow Step 6 of the epic-pipeline:
  1. `agentmon report --epic 2 --stage reviewing`
  2. Segment = `git merge-base HEAD origin/main`..HEAD.
  3. Run `/multi-review <segment-base>..HEAD --codex` in the session. It applies + commits validated FIXes itself; verify the suite is still green afterward.
  4. DISCUSS items → escalate; NITPICKs → record only.
  5. Write the consolidated report to `docs/reviews/epic-2-cp1.md`, tick this box as `- [x] CHECKPOINT 1 — reviewed to <sha>`, and commit both with `docs: epic #2 checkpoint 1 review`.
  6. `agentmon report --epic 2 --stage implementing` and continue.

---

## Task 3: Plumbing + end-to-end coverage — pass `p.Requirements` into `GateInput` (TDD)

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (the `Decide` call at ~line 646)
- Test: `hubd/internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `GateInput.Requirements` (Task 2); `db.Project.Requirements` (repo). No new symbols produced.

Unlike the `Decide` unit tests (which build `GateInput` directly and so cannot detect a forgotten `Requirements:` field), this task adds a run-loop test that drives the real gate assembly through `o.Tick`, so the plumbing itself is exercised.

- [x] **Step 1: Write the failing integration test.** Append to `hubd/internal/orchestrator/orchestrator_test.go`:

```go
func TestTickGateEnforcesPlatformRequirements(t *testing.T) {
	// A verdict that never reports a project platform requirement must fail the
	// gate closed. This exercises the run-loop plumbing (Requirements:
	// p.Requirements) that the direct Decide unit tests cannot see.
	verdictNoReqs := "```yaml\nagentmon-verdict: v1\nepic: 16\nreviews: [codex]\n" +
		"findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{
		issues: map[int]github.Issue{16: {Number: 16, State: "open", Labels: []string{"agentmon:epic"}}},
		prs:    map[int]github.PullRequest{61: {Number: 61, State: "open", Body: verdictNoReqs, HeadSHA: "s", HeadRef: "epic/16-x"}},
		checks: map[string][]github.CheckRun{"s": {{Name: "ci", Status: "completed", Conclusion: "success"}}},
	}
	ag := &fakeAgents{sessions: []string{sessionName(16)}}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()

	// Attach a platform requirement to the default project before any tick.
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p.Requirements = []db.Requirement{{ID: "always-use-rls", Text: "Always use RLS"}}
	if ok, err := d.UpdateProject(ctx, p); err != nil || !ok {
		t.Fatalf("attach requirement: ok=%v err=%v", ok, err)
	}

	o.Tick(ctx) // sync + spawn → starting
	ag.reports = []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 16, Stage: shared.EpicPROpen, PR: 61, Session: sessionName(16), Ts: "t"},
	}
	o.Tick(ctx) // drain → pr_open → gate

	e, _ := d.GetEpicByIssue(ctx, "p1", 16)
	if e.Stage != "escalated" {
		t.Fatalf("unreported platform requirement must escalate, stage = %q", e.Stage)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("gate must not merge with an unmet requirement: merged = %v", gh.merged)
	}
}
```

- [x] **Step 2: Run the test to verify it FAILS (the plumbing isn't wired yet, so the gate can't see the requirement and merges).**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestTickGateEnforcesPlatformRequirements -v 2>&1 | tail`
Expected: FAIL — `stage = "merged"` / `merged = [61]` (the requirement is invisible to the gate until Step 3).

- [x] **Step 3: Add `Requirements: p.Requirements` to the `Decide` call.** Replace the two-line call (currently at ~646–647):

```go
		res := Decide(GateInput{Verdict: v, VerdictErr: verr, Epic: e.IssueNumber, Labels: e.Labels,
			RequiredReviews: p.RequiredReviews, Requirements: p.Requirements, ChecksGreen: green, ChecksPending: pending})
```

- [x] **Step 4: gofmt + run the integration test to verify it PASSES.**

Run: `gofmt -w hubd/internal/orchestrator/orchestrator.go && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run TestTickGateEnforcesPlatformRequirements -v 2>&1 | tail`
Expected: `--- PASS: TestTickGateEnforcesPlatformRequirements`, then `ok`.

- [x] **Step 5: Full gate + vet green, then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.
Run: `GOCACHE=/tmp/agentmon-go-cache go vet ./hubd/... 2>&1 | tail` → no output.

```bash
git add hubd/internal/orchestrator/orchestrator.go hubd/internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): wire project requirements into the merge gate"
```

---

## Task 4: Learnings write-back

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** none (docs only).

- [x] **Step 1: Add one learnings line to `CLAUDE.md`.** Under the `## Conventions` section, immediately after the existing "Verdict JSON keys are CAPITALIZED" bullet, add:

```markdown
- **Platform requirements fail the gate closed:** `Decide` iterates
  `Project.Requirements` and escalates unless each id is reported `met` in the
  verdict's `Requirements` block (`json.Marshal` emits the CAPITALIZED
  `Requirements` key). `ParseVerdict` rejects out-of-domain `status`/`via`,
  empty ids, and duplicate ids (fail-closed, like a bad schema). A requirement
  added *after* an epic was imported — so the runner never reported it — fails
  **closed** as `(missing)`; that is the intended safe drift direction, not a bug.
```

- [x] **Step 2: Full gate green (docs-only, but the constraint says every commit), then commit.**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` → no `FAIL`.

```bash
git add CLAUDE.md
git commit -m "docs: platform requirements fail the merge gate closed"
```

---

## Finish (epic-pipeline Step 7 — the final whole-branch review + PR)

- [x] Rebase onto the moved base: `git fetch origin && git rebase origin/main`. Unresolvable conflict → escalate.
- [x] `agentmon report --epic 2 --stage reviewing`, then final review: `/multi-review $(git merge-base HEAD origin/main)..HEAD --codex`. Apply FIXes (already committed by the review), escalate DISCUSS items. Commit the report to `docs/reviews/epic-2-final.md`.
- [x] Full suite one last time: `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/... 2>&1 | tail` — record exact pass/fail counts for the verdict block.
- [x] Push `epic/2-requirements-merge-gate-verdict` and open the PR with `Closes #2` and the fenced `agentmon-verdict: v1` block (epic 2; `reviews: [specialist, simplifier, deep-scan, codex, cross-model]`; findings from the FINAL review only; `uncertain: false` only if no material doubt remains).
- [x] `agentmon report --epic 2 --stage pr_open --pr <PR-number>`.

## Self-Review (checked against the epic AC)

- ✅ `Verdict` gains `Requirements` (`{id, status, via}`), parsed from the same fenced-yaml block, with the same fail-closed malformed-input handling as the rest of `ParseVerdict` (enum + non-empty-id + no-dup validation) → Task 1.
- ✅ `Decide` escalates when any platform id is not present-and-`met`; reason names offending ids (unmet / uncertain / missing) → Task 2 (`unmetRequirements` + check) with matching test cases.
- ✅ Merges only when every platform id is `met` and all pre-existing conditions pass → "all met merges" case + appended (non-reordering) check.
- ✅ Existing ordering/short-circuits preserved; check is dead last → Task 2 Step 3 explicitly appends after `missingReviews`, moves nothing.
- ✅ Backward compatible — no-platform-reqs unchanged; existing tests untouched → "no platform reqs unchanged" case + existing tests asserted green in Task 2 Step 6 (existing verdicts carry no `requirements:` block, so validation is a no-op for them).
- ✅ Run loop passes `Project.Requirements` into `GateInput` next to `RequiredReviews` → Task 3, with an end-to-end `o.Tick` test that fails without the wiring.
- ✅ New `gate_test` cases: all-met→merge, one-unmet→escalate, uncertain→escalate, absent→escalate, no-platform-reqs→unchanged → Task 2 Step 1 (all five present, each asserting `merge` AND `wait`).
- ✅ `make test` green → gate command in Global Constraints, run before every commit.
- ✅ Generalizes `missingReviews` pattern → `unmetRequirements` mirrors it.
- ✅ Trust model unchanged; `Via` validated-and-carried but not gated on → design decisions + helper comment.
- ✅ Fail-open holes closed (cross-model review): garbage/absent `via` on a `met`, and duplicate-id last-write-wins → both rejected in `ParseVerdict` with `TestParseVerdictRejectsBadRequirements`.
