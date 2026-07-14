# Cost / Usage Tracking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture per-`(epic, attempt, stage-boundary, provider, model)` token usage across all runner sessions, store it durably, and display token/cost/time per epic and per project.

**Architecture:** The agent aggregates each attempt's token usage across all its transcripts (parent + child `codex exec` + `/multi-review` subagents) as each stage report passes its loopback intake, plus a final snapshot at session retire. Snapshots ride the existing report drain to the hub, which upserts them into an append-only `epic_usage` ledger. Per-stage deltas, notional cost, and wall-clock time are derived at read and exposed through the board DTOs + two on-demand endpoints.

**Tech Stack:** Go (3 modules via `go.work`: `shared`, `agent`, `hubd`), SQLite (embedded migrations), React 18 + TS + Vite + TanStack Query (web).

**Spec:** `docs/superpowers/specs/2026-07-14-cost-tracking-design.md` (read §4 before starting).

## Global Constraints

- **Go test gate:** `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...` must pass (or `make test`).
- **Web gate:** `cd web && npm run typecheck && npm run test:run` must pass.
- **`web/src/lib/contracts.ts` hand-mirrors the Go types** — a new field must traverse DB → API DTO → contract → UI in the same task set.
- **Commits:** conventional prefixes (`feat(agent):`, `feat(hubd):`, `feat(web):`, `test:`). **NEVER add a `Co-Authored-By:` / AI-attribution trailer.**
- **Token-bucket invariants (every parser/aggregator/pricing task depends on these):**
  - Four **disjoint** buckets: `input` (fresh, cache-excluded), `output`, `cache_read`, `cache_write`. `total = input+output+cache_read+cache_write`.
  - **Claude:** dedup usage rows by `message.id` **globally across all files of an attempt** (rows repeat up to 7×). `input=input_tokens`, `output=output_tokens`, `cache_read=cache_read_input_tokens`, `cache_write=cache_creation_input_tokens`. Model = `message.model`.
  - **Codex:** `input_tokens` **includes** `cached_input_tokens`; store `cache_read=cached_input_tokens` and `input=input_tokens − cached_input_tokens`. `output=output_tokens`, `cache_write=0`. Use the **last** `event_msg`/`token_count` record's `payload.info.total_token_usage`. Model = `payload.model`. Sum across an attempt's separate rollouts (distinct sessions, no shared ids).
- **Best-effort capture:** any capture/parse error yields no usage and MUST NOT turn a report into a 400 or block a stage transition. Usage is narrative; stage is authoritative.
- **Deploy order:** rebuild hub first, then agents (`usage` is backward-additive).

**Suggested review checkpoints** (>5-task plan; checkpoint at real seams, not per-task): after Task 7 (capture works end-to-end on the agent), Task 10 (storage + reap), Task 12 (derivation), Task 15 (API+contracts), and a final whole-branch review.

---

## Task 1: Shared `Usage` wire type

**Files:**
- Modify: `shared/orchestrator.go`
- Test: `shared/orchestrator_test.go`

**Interfaces:**
- Produces: `shared.Usage{Provider,Model string; Input,Output,CacheRead,CacheWrite int64}`; `OrchestratorReport.Usage []Usage` (json `usage,omitempty`).

- [ ] **Step 1: Write the failing test** — append to `shared/orchestrator_test.go`:

```go
func TestOrchestratorReportUsageOmitempty(t *testing.T) {
	// Backward-additive: a report without usage marshals with NO "usage" key.
	b, _ := json.Marshal(OrchestratorReport{Repo: "o/r", Epic: 1, Stage: EpicPlanning})
	if strings.Contains(string(b), "usage") {
		t.Fatalf("empty usage must be omitted, got %s", b)
	}
	// Round-trips when present.
	in := OrchestratorReport{Repo: "o/r", Epic: 1, Stage: EpicReviewing,
		Usage: []Usage{{Provider: "claude", Model: "claude-opus-4-8", Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40}}}
	b, _ = json.Marshal(in)
	var out OrchestratorReport
	if err := json.Unmarshal(b, &out); err != nil || len(out.Usage) != 1 || out.Usage[0].CacheRead != 30 {
		t.Fatalf("round-trip failed: %v %+v", err, out)
	}
}
```

Ensure `import` block has `encoding/json` and `strings`.

- [ ] **Step 2: Run — expect FAIL** (`Usage` undefined):
`GOCACHE=/tmp/agentmon-go-cache go test ./shared/ -run TestOrchestratorReportUsage -v`

- [ ] **Step 3: Implement** — in `shared/orchestrator.go`, add the type and field:

```go
// Usage is one (provider,model) cumulative token snapshot attached to a stage
// report. Buckets are DISJOINT: total = Input+Output+CacheRead+CacheWrite.
// Input is fresh input only (cache excluded) for BOTH providers.
type Usage struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
}
```

Add to `OrchestratorReport` (after `Ts`): `Usage []Usage `json:"usage,omitempty"``.

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add shared/ && git commit -m "feat(shared): Usage wire type on OrchestratorReport"`

---

## Task 2: Capture-spike — confirm subagent transcripts + lock fixtures

**Goal:** Empirically confirm where `/multi-review` subagent transcripts land and capture minimal real-shape fixtures. This is investigation + fixture creation, not TDD.

**Files:**
- Create: `agent/internal/usage/testdata/claude_dup.jsonl`
- Create: `agent/internal/usage/testdata/codex_rollout.jsonl`
- Modify (append findings): `docs/superpowers/specs/2026-07-14-cost-tracking-design.md` (§10)

- [ ] **Step 1: Confirm subagent transcript behavior.** During or after a real pipeline run, in the runner's project dir, check whether `/multi-review` lens subagents write separate `.jsonl` files and whether any usage is duplicated into the parent:

```bash
python3 - <<'PY'
import json,glob,os
files=glob.glob(os.path.expanduser('~/.claude/projects/*agentmon*/*.jsonl'))
sc=sum(1 for f in files for l in open(f) if '"isSidechain":true' in l)
print("files",len(files),"isSidechain rows",sc)  # expect sc==0 => subagents NOT inlined; separate files
PY
```

Record the result in spec §10. **The aggregator's global `message.id` dedup (Task 5) makes the exact file location non-blocking** — any in-scope file's usage is counted once regardless.

- [ ] **Step 2: Write the Claude fixture** `agent/internal/usage/testdata/claude_dup.jsonl` — 3 rows, one `message.id` REPEATED (the exact defect: must dedup) and a second id; one non-assistant row to ignore:

```
{"type":"assistant","timestamp":"2026-07-14T10:00:00Z","message":{"id":"msg_A","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000,"cache_creation_input_tokens":10}}}
{"type":"assistant","timestamp":"2026-07-14T10:00:01Z","message":{"id":"msg_A","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000,"cache_creation_input_tokens":10}}}
{"type":"user","timestamp":"2026-07-14T10:00:02Z","message":{"role":"user"}}
{"type":"assistant","timestamp":"2026-07-14T10:00:03Z","message":{"id":"msg_B","model":"claude-opus-4-8","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
```

Deduped totals for this file: input `107`, output `53`, cache_read `1000`, cache_write `10`.

- [ ] **Step 3: Write the Codex fixture** `agent/internal/usage/testdata/codex_rollout.jsonl` — two `token_count` records (cumulative grows); the LAST `total_token_usage` is authoritative:

```
{"timestamp":"2026-07-14T10:00:00Z","type":"event_msg","payload":{"type":"other"}}
{"timestamp":"2026-07-14T10:00:05Z","type":"event_msg","payload":{"model":"gpt-5.6-sol","type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":400,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":520},"last_token_usage":{"input_tokens":500,"cached_input_tokens":400,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":520}}}}
{"timestamp":"2026-07-14T10:00:09Z","type":"event_msg","payload":{"model":"gpt-5.6-sol","type":"token_count","info":{"total_token_usage":{"input_tokens":1200,"cached_input_tokens":1000,"output_tokens":30,"reasoning_output_tokens":8,"total_tokens":1230},"last_token_usage":{"input_tokens":700,"cached_input_tokens":600,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":710}}}}
```

Expected normalized: model `gpt-5.6-sol`, cache_read `1000`, input `1200−1000=200`, output `30`, cache_write `0`.

- [ ] **Step 4: Commit** — `git add agent/internal/usage/testdata docs/ && git commit -m "test(agent): capture-spike fixtures + subagent-transcript finding"`

---

## Task 3: Claude transcript parser

**Files:**
- Create: `agent/internal/usage/usage.go` (shared types), `agent/internal/usage/claude.go`
- Test: `agent/internal/usage/claude_test.go`

**Interfaces:**
- Produces: `type MsgUsage struct{ ID, Provider, Model string; Input, Output, CacheRead, CacheWrite int64 }`; `func ParseClaude(r io.Reader) ([]MsgUsage, error)` — one entry per raw usage row (NOT deduped; the aggregator dedups by ID globally).

- [ ] **Step 1: Write the failing test** `agent/internal/usage/claude_test.go`:

```go
func TestParseClaudeRawRows(t *testing.T) {
	f, err := os.Open("testdata/claude_dup.jsonl")
	if err != nil { t.Fatal(err) }
	defer f.Close()
	got, err := ParseClaude(f)
	if err != nil { t.Fatal(err) }
	if len(got) != 3 { t.Fatalf("want 3 usage rows (2 dup + 1), got %d", len(got)) }
	if got[0].ID != "msg_A" || got[0].Model != "claude-opus-4-8" || got[0].CacheRead != 1000 || got[0].CacheWrite != 10 {
		t.Fatalf("bad first row: %+v", got[0])
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`ParseClaude` undefined): `GOCACHE=/tmp/agentmon-go-cache go test ./agent/internal/usage/ -run Claude -v`

- [ ] **Step 3: Implement** — `agent/internal/usage/usage.go`:

```go
package usage

// MsgUsage is one raw provider usage record. Buckets are disjoint (fresh input).
type MsgUsage struct {
	ID         string // message id (Claude) — dedup key; "" for Codex
	Provider   string
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}
```

`agent/internal/usage/claude.go`:

```go
package usage

import (
	"bufio"
	"encoding/json"
	"io"
)

type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseClaude returns one MsgUsage per usage-bearing row (dedup happens globally
// in Aggregate). Malformed lines are skipped — capture is best-effort.
func ParseClaude(r io.Reader) ([]MsgUsage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript rows can be large
	var out []MsgUsage
	for sc.Scan() {
		var l claudeLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Message.Usage == nil {
			continue
		}
		u := l.Message.Usage
		out = append(out, MsgUsage{ID: l.Message.ID, Provider: "claude", Model: l.Message.Model,
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
	}
	return out, sc.Err()
}
```

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add agent/internal/usage && git commit -m "feat(agent): Claude transcript usage parser"`

---

## Task 4: Codex rollout parser

**Files:**
- Create: `agent/internal/usage/codex.go`
- Test: `agent/internal/usage/codex_test.go`

**Interfaces:**
- Produces: `func ParseCodex(r io.Reader) (MsgUsage, bool, error)` — the LAST cumulative total, normalized (cached⊂input); bool=false when the rollout has no token_count record.

- [ ] **Step 1: Write the failing test** `agent/internal/usage/codex_test.go`:

```go
func TestParseCodexLastCumulative(t *testing.T) {
	f, _ := os.Open("testdata/codex_rollout.jsonl")
	defer f.Close()
	got, ok, err := ParseCodex(f)
	if err != nil || !ok { t.Fatalf("want ok, got ok=%v err=%v", ok, err) }
	// cache_read=1000, input=1200-1000=200, output=30, cache_write=0
	if got.Model != "gpt-5.6-sol" || got.CacheRead != 1000 || got.Input != 200 || got.Output != 30 || got.CacheWrite != 0 {
		t.Fatalf("bad normalization: %+v", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — `agent/internal/usage/codex.go`:

```go
package usage

import (
	"bufio"
	"encoding/json"
	"io"
)

type codexLine struct {
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  struct {
			Total struct {
				Input  int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// ParseCodex returns the LAST token_count record's cumulative total, normalized:
// input_tokens INCLUDES cached, so fresh input = input-cached, cache_read=cached.
func ParseCodex(r io.Reader) (MsgUsage, bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var last MsgUsage
	var found bool
	for sc.Scan() {
		var l codexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Payload.Type != "token_count" {
			continue
		}
		t := l.Payload.Info.Total
		last = MsgUsage{Provider: "codex", Model: l.Payload.Model,
			Input: t.Input - t.Cached, Output: t.Output, CacheRead: t.Cached, CacheWrite: 0}
		found = true
	}
	return last, found, sc.Err()
}
```

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add agent/internal/usage && git commit -m "feat(agent): Codex rollout usage parser"`

---

## Task 5: Source resolution (fd-binding + child enumeration)

**Files:**
- Create: `agent/internal/usage/sources.go`
- Test: `agent/internal/usage/sources_test.go`

**Interfaces:**
- Produces:
  - `type Sources struct{ Claude, Codex []string }` (absolute file paths)
  - `func openTranscriptFDs(pid int) []string` — `.jsonl` files open by pid or any descendant (via `/proc/<pid>/fd`).
  - `func enumerateChildRollouts(codexRoot, worktree string, since time.Time) []string` — Codex rollouts whose recorded `cwd` == worktree and mtime ≥ since.

- [ ] **Step 1: Write the failing test** `agent/internal/usage/sources_test.go` (fd-binding is testable against THIS process's own open file):

```go
func TestOpenTranscriptFDsFindsOpenJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")
	f, _ := os.Create(p)
	defer f.Close()
	got := openTranscriptFDs(os.Getpid())
	found := false
	for _, g := range got { if g == p { found = true } }
	if !found { t.Fatalf("expected to find open %s in %v", p, got) }
}

func TestEnumerateChildRolloutsFiltersByCwd(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "07", "14"); os.MkdirAll(day, 0o755)
	mine := filepath.Join(day, "rollout-a.jsonl")
	os.WriteFile(mine, []byte(`{"payload":{"cwd":"/wt/epic7"}}`+"\n"), 0o644)
	other := filepath.Join(day, "rollout-b.jsonl")
	os.WriteFile(other, []byte(`{"payload":{"cwd":"/wt/epic9"}}`+"\n"), 0o644)
	got := enumerateChildRollouts(root, "/wt/epic7", time.Time{})
	if len(got) != 1 || got[0] != mine { t.Fatalf("want only %s, got %v", mine, got) }
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — `agent/internal/usage/sources.go`:

```go
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Sources struct{ Claude, Codex []string }

// openTranscriptFDs returns .jsonl files held open by pid or any descendant.
// This binds the PARENT transcript to the exact runner process, which is
// session-safe even when concurrent attempts share a project dir.
func openTranscriptFDs(pid int) []string {
	seen := map[string]bool{}
	for _, p := range append([]int{pid}, descendants(pid)...) {
		entries, _ := os.ReadDir("/proc/" + strconv.Itoa(p) + "/fd")
		for _, e := range entries {
			target, err := os.Readlink("/proc/" + strconv.Itoa(p) + "/fd/" + e.Name())
			if err == nil && strings.HasSuffix(target, ".jsonl") {
				seen[target] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen { out = append(out, f) }
	return out
}

// descendants walks /proc for children of pid (one level of process tree is
// enough: tmux pane_pid -> shell -> runner).
func descendants(pid int) []int {
	var out []int
	procs, _ := os.ReadDir("/proc")
	for _, pe := range procs {
		cpid, err := strconv.Atoi(pe.Name())
		if err != nil { continue }
		b, err := os.ReadFile("/proc/" + pe.Name() + "/stat")
		if err != nil { continue }
		// stat: "pid (comm) state ppid ..." — ppid is field 4, but comm may hold spaces.
		if r := strings.LastIndex(string(b), ")"); r > 0 {
			fields := strings.Fields(string(b)[r+1:])
			if len(fields) >= 2 && fields[1] == strconv.Itoa(pid) {
				out = append(out, cpid)
				out = append(out, descendants(cpid)...)
			}
		}
	}
	return out
}

// enumerateChildRollouts returns Codex rollouts under codexRoot whose recorded
// cwd == worktree and mtime >= since.
func enumerateChildRollouts(codexRoot, worktree string, since time.Time) []string {
	var out []string
	filepath.WalkDir(codexRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") { return nil }
		if fi, e := d.Info(); e == nil && fi.ModTime().Before(since) { return nil }
		if rolloutCwd(p) == worktree { out = append(out, p) }
		return nil
	})
	return out
}

func rolloutCwd(path string) string {
	f, err := os.Open(path); if err != nil { return "" }
	defer f.Close()
	sc := bufio.NewScanner(f); sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var l struct{ Payload struct{ Cwd string `json:"cwd"` } `json:"payload"` }
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Payload.Cwd != "" { return l.Payload.Cwd }
	}
	return ""
}
```

Note: exact Codex `cwd` JSON path is confirmed in Task 2; adjust `rolloutCwd`'s struct if the spike shows a different location.

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add agent/internal/usage && git commit -m "feat(agent): source resolution (fd-binding + child rollout enumeration)"`

---

## Task 6: Aggregator (combine sources → per-(provider,model) Usage)

**Files:**
- Create: `agent/internal/usage/aggregate.go`
- Test: `agent/internal/usage/aggregate_test.go`

**Interfaces:**
- Consumes: `ParseClaude`, `ParseCodex`, `Sources`, `shared.Usage`.
- Produces: `func Aggregate(s Sources) []shared.Usage` — global `message.id` dedup for Claude, sum per model; per-rollout Codex totals summed per model.

- [ ] **Step 1: Write the failing test** `agent/internal/usage/aggregate_test.go`:

```go
func TestAggregateDedupAndPerModel(t *testing.T) {
	// Two Claude files sharing msg_A must count it ONCE; Codex summed separately.
	s := Sources{
		Claude: []string{"testdata/claude_dup.jsonl", "testdata/claude_dup.jsonl"},
		Codex:  []string{"testdata/codex_rollout.jsonl"},
	}
	got := Aggregate(s)
	var claude, codex *shared.Usage
	for i := range got {
		switch got[i].Provider { case "claude": claude = &got[i]; case "codex": codex = &got[i] }
	}
	if claude == nil || claude.Input != 107 || claude.Output != 53 || claude.CacheRead != 1000 || claude.CacheWrite != 10 {
		t.Fatalf("claude dedup wrong: %+v", claude) // msg_A once + msg_B, despite the file listed twice
	}
	if codex == nil || codex.Input != 200 || codex.CacheRead != 1000 {
		t.Fatalf("codex wrong: %+v", codex)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — `agent/internal/usage/aggregate.go`:

```go
package usage

import (
	"os"

	"agentmon/shared"
)

// Aggregate sums usage across all of an attempt's sources into one entry per
// (provider,model). Claude rows are deduped by message.id GLOBALLY (rows repeat,
// and the same file may be enumerated twice); Codex rollouts are distinct
// sessions summed by their cumulative totals.
func Aggregate(s Sources) []shared.Usage {
	type key struct{ provider, model string }
	acc := map[key]*shared.Usage{}
	add := func(m MsgUsage) {
		k := key{m.Provider, m.Model}
		u := acc[k]
		if u == nil { u = &shared.Usage{Provider: m.Provider, Model: m.Model}; acc[k] = u }
		u.Input += m.Input; u.Output += m.Output; u.CacheRead += m.CacheRead; u.CacheWrite += m.CacheWrite
	}

	seen := map[string]bool{} // global Claude message.id dedup
	for _, p := range s.Claude {
		f, err := os.Open(p); if err != nil { continue }
		rows, _ := ParseClaude(f); f.Close()
		for _, r := range rows {
			if r.ID != "" && seen[r.ID] { continue }
			if r.ID != "" { seen[r.ID] = true }
			add(r)
		}
	}
	for _, p := range s.Codex {
		f, err := os.Open(p); if err != nil { continue }
		u, ok, _ := ParseCodex(f); f.Close()
		if ok { add(u) }
	}

	out := make([]shared.Usage, 0, len(acc))
	for _, u := range acc { out = append(out, *u) }
	return out
}
```

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add agent/internal/usage && git commit -m "feat(agent): usage aggregator with global message.id dedup"`

---

## Task 7: Capturer + intake enrichment (best-effort, never-400)

**Files:**
- Create: `agent/internal/usage/capturer.go`
- Modify: `agent/internal/report/intake.go`, and the wiring in `agent/cmd/agentmon-agent/main.go` (where `IntakeHandler` is constructed)
- Test: `agent/internal/report/intake_test.go`

**Interfaces:**
- Consumes: `Aggregate`, `Sources`, tmux pane inspection.
- Produces: `report.UsageCapturer func(ctx context.Context, socket, pane string) []shared.Usage`; `IntakeHandler(cfg, st, resolve, capture, now)`.

- [ ] **Step 1: Write the failing test** — extend `agent/internal/report/intake_test.go`: capture output is attached, and a capturer that panics/returns nil never breaks the 200:

```go
func TestIntakeAttachesUsageBestEffort(t *testing.T) {
	st := NewStore("inst")
	cap := func(ctx context.Context, socket, pane string) []shared.Usage {
		return []shared.Usage{{Provider: "claude", Model: "m", Output: 5}}
	}
	h := IntakeHandler(testCfg(), st, stubResolve("sess"), cap, fixedNow)
	// ... POST a valid planning report for a configured pane (reuse existing helper) ...
	rec := doValidReport(t, h)
	if rec.Code != 200 { t.Fatalf("want 200, got %d", rec.Code) }
	reps := st.Drain("inst", 0).Reports
	if len(reps) != 1 || len(reps[0].Usage) != 1 || reps[0].Usage[0].Output != 5 {
		t.Fatalf("usage not attached: %+v", reps)
	}
}

func TestIntakeNilCapturerStillSucceeds(t *testing.T) {
	h := IntakeHandler(testCfg(), NewStore("i"), stubResolve("s"), nil, fixedNow)
	if rec := doValidReport(t, h); rec.Code != 200 { t.Fatalf("nil capturer broke intake: %d", rec.Code) }
}
```

(Use/adapt the existing intake_test helpers for building a valid request; if none exist, factor `doValidReport`/`stubResolve` from the current tests.)

- [ ] **Step 2: Run — expect FAIL** (signature mismatch).
- [ ] **Step 3: Implement.** In `intake.go`, add `capture UsageCapturer` param and type:

```go
// UsageCapturer returns the reporting session's cumulative usage, best-effort.
// It is called AFTER session resolution and its result is advisory: a nil
// return (or nil capturer) leaves the report usage-less. It must never error.
type UsageCapturer func(ctx context.Context, socket, pane string) []shared.Usage
```

Change the signature to `IntakeHandler(cfg config.Config, st *Store, resolve SessionResolver, capture UsageCapturer, now func() time.Time)`. After `rep` is built and before `st.Add`, when not a dry-run:

```go
if capture != nil {
	func() {
		defer func() { _ = recover() }() // capture is best-effort; never break intake
		rep.Usage = capture(ctx, t.SocketName, pane)
	}()
}
```

`agent/internal/usage/capturer.go`:

```go
package usage

import (
	"context"
	"os"
	"time"

	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// NewCapturer builds a report.UsageCapturer. paneInfo returns (pid, cwd, command)
// for a pane; production binds tmux.PaneInfo.
func NewCapturer(paneInfo func(ctx context.Context, socket, pane string) (pid int, cwd, command string, err error)) func(context.Context, string, string) []shared.Usage {
	claudeRoot := os.ExpandEnv("$HOME/.claude/projects")
	codexRoot := os.ExpandEnv("$HOME/.codex/sessions")
	return func(ctx context.Context, socket, pane string) []shared.Usage {
		pid, cwd, cmd, err := paneInfo(ctx, socket, pane)
		if err != nil { return nil }
		s := Sources{}
		// Parent transcript: bound to the runner process's open fd (session-safe).
		for _, f := range openTranscriptFDs(pid) {
			if isCodexPath(f, codexRoot) { s.Codex = append(s.Codex, f) } else { s.Claude = append(s.Claude, f) }
		}
		// Child sessions in the worktree (codex exec, subagents). since=epoch is a
		// safe over-scope for v1 serial runs; Task-2 finding may tighten the window.
		_ = cmd
		s.Codex = append(s.Codex, enumerateChildRollouts(codexRoot, cwd, time.Time{})...)
		s.Claude = append(s.Claude, enumerateChildTranscripts(claudeRoot, cwd, time.Time{})...)
		return Aggregate(s)
	}
}
```

Add `isCodexPath` (prefix match on codexRoot) and `enumerateChildTranscripts` (mirror `enumerateChildRollouts` but for Claude project dirs matching the encoded cwd; global dedup in `Aggregate` makes over-inclusion safe) to `sources.go`, plus `tmux.PaneInfo` in the tmux package returning `#{pane_pid}`, `#{pane_current_path}`, `#{pane_current_command}` via `display-message -p`. Wire `NewCapturer(tmux.PaneInfo)` into `IntakeHandler` at the main.go construction site.

- [ ] **Step 4: Run** — `GOCACHE=/tmp/agentmon-go-cache go test ./agent/...` — expect PASS.
- [ ] **Step 5: Commit** — `git add agent/ && git commit -m "feat(agent): enrich stage reports with best-effort usage capture"`

---

## Task 8: `epic_usage` migration + DB layer

**Files:**
- Create: `hubd/internal/db/migrations/0010_epic_usage.sql`, `hubd/internal/db/usage.go`
- Test: `hubd/internal/db/usage_test.go`

**Interfaces:**
- Produces:
  - `type UsageRow struct{ ProjectID, ProjectName, Repo string; IssueNumber, Attempt int; Stage, CapturedAt, Provider, Model string; Input, Output, CacheRead, CacheWrite int64 }`
  - `func (d *DB) UpsertUsage(ctx, UsageRow) error` (idempotent on the UNIQUE key)
  - `func (d *DB) ListEpicUsage(ctx, projectID string, issue int) ([]UsageRow, error)`
  - `func (d *DB) ListProjectUsage(ctx, projectID string) ([]UsageRow, error)`

- [ ] **Step 1: Write the failing test** `hubd/internal/db/usage_test.go`:

```go
func TestUpsertUsageIdempotent(t *testing.T) {
	d := newTestDB(t) // existing helper
	row := db.UsageRow{ProjectID: "p", ProjectName: "P", Repo: "o/r", IssueNumber: 7, Attempt: 1,
		Stage: "reviewing", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "m", Output: 50}
	if err := d.UpsertUsage(ctx, row); err != nil { t.Fatal(err) }
	row.Output = 99 // same key, corrected value (recovery on redelivery)
	if err := d.UpsertUsage(ctx, row); err != nil { t.Fatal(err) }
	rows, _ := d.ListEpicUsage(ctx, "p", 7)
	if len(rows) != 1 || rows[0].Output != 99 { t.Fatalf("want 1 row output=99, got %+v", rows) }
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** the migration `0010_epic_usage.sql`:

```sql
CREATE TABLE epic_usage (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  project_name  TEXT NOT NULL,
  repo          TEXT NOT NULL,
  issue_number  INTEGER NOT NULL,
  attempt       INTEGER NOT NULL,
  stage         TEXT NOT NULL,
  captured_at   TEXT NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  input_tokens        INTEGER NOT NULL,
  output_tokens       INTEGER NOT NULL,
  cache_read_tokens   INTEGER NOT NULL,
  cache_write_tokens  INTEGER NOT NULL,
  UNIQUE(project_id, issue_number, attempt, stage, captured_at, provider, model)
);
CREATE INDEX idx_epic_usage_project ON epic_usage(project_id);
CREATE INDEX idx_epic_usage_epic ON epic_usage(project_id, issue_number);
```

`usage.go`: `UpsertUsage` uses `INSERT ... ON CONFLICT(project_id,issue_number,attempt,stage,captured_at,provider,model) DO UPDATE SET input_tokens=excluded.input_tokens, output_tokens=excluded..., ...` with a fresh `uuid.NewString()` id on insert. `ListEpicUsage`/`ListProjectUsage` select ordered by `attempt, captured_at`. Follow the scan pattern in `epics.go`.

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add hubd/internal/db && git commit -m "feat(hubd): epic_usage ledger table + DB layer"`

---

## Task 9: Hub applyReport upsert (unconditional, best-effort)

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (`applyReport`)
- Test: `hubd/internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `db.UpsertUsage`, `shared.OrchestratorReport.Usage`.

- [ ] **Step 1: Write the failing test** — a report carrying usage writes ledger rows even when the stage transition is a no-op (redelivery recovery):

```go
func TestApplyReportUpsertsUsageEvenOnNoopTransition(t *testing.T) {
	// Arrange an epic already in "reviewing"; deliver a reviewing report w/ usage.
	// The transition is a no-op, but usage MUST still be upserted.
	// ... build orchestrator with fake DB (existing test harness) ...
	rep := shared.OrchestratorReport{Repo: "o/r", Epic: 7, Stage: shared.EpicReviewing,
		Session: "P-7", Ts: "2026-07-14T10:00:00Z",
		Usage: []shared.Usage{{Provider: "claude", Model: "m", Output: 42}}}
	o.applyReport(ctx, proj, rep)
	rows, _ := o.d.DB.ListEpicUsage(ctx, proj.ID, 7)
	if len(rows) != 1 || rows[0].Output != 42 { t.Fatalf("usage not upserted on noop: %+v", rows) }
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — in `applyReport`, after resolving the epic `e` and BEFORE/around the transition, add (runs regardless of transition outcome):

```go
o.upsertUsage(ctx, p, e, r) // unconditional; best-effort inside
```

and:

```go
func (o *Orchestrator) upsertUsage(ctx context.Context, p db.Project, e db.Epic, r shared.OrchestratorReport) {
	for _, u := range r.Usage {
		row := db.UsageRow{ProjectID: p.ID, ProjectName: p.Name, Repo: p.Repo,
			IssueNumber: e.IssueNumber, Attempt: e.Attempt, Stage: string(r.Stage),
			CapturedAt: r.Ts, Provider: u.Provider, Model: u.Model,
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
		if err := o.d.DB.UpsertUsage(ctx, row); err != nil {
			log.Printf("orchestrator[%s]: usage upsert epic #%d: %v", p.Name, e.IssueNumber, err)
		}
	}
}
```

(Usage error is logged and swallowed — never blocks the transition. Attempt is `e.Attempt`, stable under `tickMu`.)

- [ ] **Step 4: Run — expect PASS** (`./hubd/...`).
- [ ] **Step 5: Commit** — `git add hubd/internal/orchestrator && git commit -m "feat(hubd): upsert usage snapshots on report apply"`

---

## Task 10: Reap snapshot (capture-then-kill returns terminal usage)

**Files:**
- Modify: `agent/internal/api/sessions.go` (kill handler → capture-then-kill), `shared/session.go` (kill response type if needed), `hubd/internal/registry/client.go` (`KillSession` returns usage), `hubd/internal/orchestrator/orchestrator.go` (`killEpicSession` stores it)
- Test: `hubd/internal/orchestrator/orchestrator_test.go`, `agent/internal/api/sessions_test.go`

**Interfaces:**
- Produces: agent `POST kill` responds `{"usage": []shared.Usage}`; `registry.Client.KillSession(...) ([]shared.Usage, error)`; orchestrator stores a terminal boundary with `stage = e.Stage`, `captured_at = now`.

- [ ] **Step 1: Write the failing test** — killing a merged epic's session persists a terminal usage row:

```go
func TestKillEpicSessionStoresTerminalUsage(t *testing.T) {
	// Fake Agents.KillSession returns []shared.Usage{{codex, m, output:7}}.
	// After finishMerged, ListEpicUsage has a row at stage=e.Stage.
	// ... existing merge-path harness ...
	rows, _ := o.d.DB.ListEpicUsage(ctx, p.ID, e.IssueNumber)
	if !hasRow(rows, "codex", 7) { t.Fatalf("terminal usage not stored: %+v", rows) }
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement.**
  - Agent kill handler: before `kill-session`, run the same capturer against the target session's pane; return `{"usage": [...]}`. (Reuse `usage.NewCapturer`; resolve the session's active pane via existing tmux discovery.)
  - `registry.Client.KillSession` decodes the usage from the response body; update its interface in `orchestrator.go`'s `Agents` interface and all fakes.
  - `killEpicSession`: on a confirmed kill returning usage, call `o.upsertUsage(ctx, p, e, shared.OrchestratorReport{Stage: shared.EpicStage(e.Stage), Ts: o.d.Now(), Usage: usage})`. Keep the existing return-bool semantics.

- [ ] **Step 4: Run — expect PASS** (`./agent/... ./hubd/...`).
- [ ] **Step 5: Commit** — `git add agent/ hubd/ shared/ && git commit -m "feat: capture terminal usage snapshot at session reap"`

---

## Task 11: Rate card + cost

**Files:**
- Create: `hubd/internal/orchestrator/pricing.go`
- Test: `hubd/internal/orchestrator/pricing_test.go`

**Interfaces:**
- Produces: `type Rate struct{ In, Out, CacheRead, CacheWrite float64 }`; `func CostUSD(input, output, cacheRead, cacheWrite int64, model string) *float64` (nil for unknown model).

- [ ] **Step 1: Write the failing test:**

```go
func TestCostKnownAndUnknownModel(t *testing.T) {
	// opus: In=15,Out=75,CacheRead=1.5,CacheWrite=18.75 $/Mtok
	c := CostUSD(1_000_000, 1_000_000, 1_000_000, 1_000_000, "claude-opus-4-8")
	if c == nil || math.Abs(*c-(15+75+1.5+18.75)) > 1e-9 { t.Fatalf("got %v", c) }
	if CostUSD(1, 1, 1, 1, "who-dis") != nil { t.Fatal("unknown model must be nil cost") }
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** `pricing.go` — a `map[string]Rate` (owner sets real notional values; start with `claude-opus-4-8`, `claude-haiku-4-5-20251001`, `gpt-5.6-sol`) and `CostUSD` returning `(input*r.In + output*r.Out + cacheRead*r.CacheRead + cacheWrite*r.CacheWrite)/1e6`, nil if model absent.

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add hubd/internal/orchestrator/pricing* && git commit -m "feat(hubd): notional-cost rate card"`

---

## Task 12: Derivation helper (boundaries → EpicUsage)

**Files:**
- Create: `hubd/internal/orchestrator/usage_derive.go`
- Test: `hubd/internal/orchestrator/usage_derive_test.go`

**Interfaces:**
- Consumes: `[]db.UsageRow`, `db.Epic`, `CostUSD`.
- Produces: `type TokenTotals`, `ModelUsage`, `UsageStage`, `UsageAttempt`, `EpicUsage` (Go mirrors of the contract in spec §4.7); `func DeriveEpicUsage(rows []db.UsageRow, e db.Epic) EpicUsage`.

- [ ] **Step 1: Write the failing test** — recurring stages + baseline-0 + per-model + interval attribution:

```go
// u builds a claude/one-model boundary row with cumulative input `cum`.
func u(stage, ts string, cum int64) db.UsageRow {
	return db.UsageRow{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: stage,
		CapturedAt: ts, Provider: "claude", Model: "m", Input: cum}
}

func TestDeriveRecurringStagesBaselineZero(t *testing.T) {
	// One attempt; claude parent cumulative input across boundaries (monotonic),
	// with a checkpoint LOOP: reviewing then implementing again then final reviewing.
	rows := []db.UsageRow{
		u("planning", "t0", 100), u("implementing", "t1", 300), u("reviewing", "t2", 350),
		u("implementing", "t3", 600), u("reviewing", "t4", 900),
	}
	got := DeriveEpicUsage(rows, db.Epic{IssueNumber: 7, Attempt: 1, Stage: "merged"})
	a := got.Attempts[0]
	// interval i = cum[i]-cum[i-1] (cum[-1]=0), attributed to stage[i]:
	//   planning     = 100-0                 = 100
	//   implementing = (300-100)+(600-350)   = 450
	//   reviewing    = (350-300)+(900-600)   = 350
	if stageInput(a, "planning") != 100 || stageInput(a, "implementing") != 450 || stageInput(a, "reviewing") != 350 {
		t.Fatalf("per-stage attribution wrong: %+v", a.Stages)
	}
	if a.Tokens.Input != 900 { // attempt total = final cumulative
		t.Fatalf("attempt total = 900, got %d", a.Tokens.Input)
	}
	if a.Outcome != "merged" { t.Fatalf("last attempt outcome = epic stage, got %q", a.Outcome) }
}

// stageInput sums Input across a stage's models in a derived attempt (test helper).
func stageInput(a UsageAttempt, stage string) int64 {
	for _, s := range a.Stages { if s.Stage == stage { return s.Tokens.Input } }
	return 0
}
```

- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** the rule: group rows by `attempt`; within an attempt group by `(provider,model)`; order that model's boundaries by `captured_at`; **baseline 0** so interval `i` = `cum[i] - cum[i-1]` (cum[-1]=0) attributed to `stage[i]`; sum per stage across models; attempt total = Σ final cumulative per model; cost via `CostUSD`; durations from `captured_at` deltas; `outcome` = last attempt→`e.Stage`, priors→`"retried"`; `is_lower_bound` = attempt has no terminal boundary matching a retired/merged stage. Epic total = Σ attempts.

- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add hubd/internal/orchestrator/usage_derive* && git commit -m "feat(hubd): per-stage/attempt usage derivation"`

---

## Task 13: Inline rollup on board DTOs

**Files:**
- Modify: `hubd/internal/db/usage.go` (grouped rollup query), `hubd/internal/api/orchestrator.go` (`epicDTO`, `projectDTO`, assembly)
- Test: `hubd/internal/api/orchestrator_test.go`

**Interfaces:**
- Produces: `func (d *DB) EpicUsageRollups(ctx, projectID string) (map[int]UsageRollup, error)` (issue→{tokens,cost,ms}); `epicDTO.Usage *usageRollupDTO`, `projectDTO.Usage *usageRollupDTO`.

- [ ] **Step 1: Write the failing test** — board epic DTO carries the rollup when ledger rows exist. (Assert `dto.Usage.Tokens` matches the summed final cumulative.)
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — `EpicUsageRollups` does ONE grouped query per project (final cumulative per (issue,attempt,provider,model) → sum), reusing `DeriveEpicUsage` totals or a lighter SUM. Add `usageRollupDTO{Tokens int64; Cost *float64; DurationMs int64}` json `usage,omitempty`; populate in `toEpicDTO`/`projectOut` from a per-project rollup map computed once in the board handler.
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add hubd/internal/{db,api} && git commit -m "feat(hubd): inline usage rollup on board DTOs"`

---

## Task 14: Usage detail endpoints

**Files:**
- Modify: `hubd/internal/api/orchestrator.go` (or new `usage_api.go`), router registration
- Test: `hubd/internal/api/orchestrator_test.go`

**Interfaces:**
- Produces: `GET /orchestrator/epics/{id}/usage` → `EpicUsage`; `GET /orchestrator/projects/{id}/usage` → project by-stage + by-model summary. Both behind `authz.OrchestratorView`.

- [ ] **Step 1: Write the failing test** — the epic endpoint returns the derived per-attempt/stage/model breakdown for a seeded ledger; 404 for unknown epic; 403 unauthenticated.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — handlers call `db.ListEpicUsage`/`ListProjectUsage` + `DeriveEpicUsage`/a project aggregation, marshal the DTO family. Register routes next to the existing epic-plan route (same auth pattern).
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** — `git add hubd/internal/api && git commit -m "feat(hubd): epic + project usage detail endpoints"`

---

## Task 15: Contracts (`contracts.ts`)

**Files:**
- Modify: `web/src/lib/contracts.ts`
- Test: `cd web && npm run typecheck`

- [ ] **Step 1: Add the DTO family** exactly mirroring spec §4.7: `TokenTotals`, `ModelUsage`, `UsageStage`, `UsageAttempt`, `EpicUsage`, `UsageRollup`; add `usage?: UsageRollup` to `EpicDTO` and `ProjectDTO`; add `EpicUsageResponse = EpicUsage` and a `ProjectUsageResponse` shape matching Task 14.
- [ ] **Step 2: Run** `npm run typecheck` — expect PASS (no consumers yet).
- [ ] **Step 3: Commit** — `git add web/src/lib/contracts.ts && git commit -m "feat(web): usage DTO contracts"`

---

## Task 16: Epic card usage line

**Files:**
- Modify: `web/src/components/board/EpicCard.tsx`
- Create: `web/src/lib/usage-format.ts` (`fmtTokens`, `fmtCost`, `fmtDuration`)
- Test: `web/src/components/board/EpicCard.test.tsx` (or existing), `web/src/lib/usage-format.test.ts`

- [ ] **Step 1: Write failing tests** — `fmtTokens(1_240_000)==="1.24M"`, `fmtCost(3.4)==="~$3.40"`, `fmtCost(null)==="$—"`, `fmtDuration(2280000)==="38m"`; card renders `1.24M tok · ~$3.40 · 38m` when `usage` present and nothing when absent.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** the formatters + a compact line in `EpicCard` reading `epic.usage`.
- [ ] **Step 4: Run** `npm run test:run` — expect PASS.
- [ ] **Step 5: Commit** — `git add web/src && git commit -m "feat(web): epic card usage line"`

---

## Task 17: Epic drawer breakdown (lazy-fetched)

**Files:**
- Modify: `web/src/components/board/EpicDrawer.tsx`, `web/src/hooks/useEpicActions.ts` (or a new `useEpicUsage.ts` query hook)
- Test: `web/src/components/board/EpicDrawer.test.tsx`

- [ ] **Step 1: Write the failing test** — on drawer open the component queries `/orchestrator/epics/{id}/usage` and renders per-attempt (outcome + total) → per-stage (tokens/cost/time) → per-model, with `≥` on `is_lower_bound`.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** a `useEpicUsage(id)` TanStack Query hook (enabled when the drawer is open) + a `UsageBreakdown` subcomponent following the existing drawer/query patterns. Apply `dataviz` guidance for any stage bars.
- [ ] **Step 4: Run** `npm run test:run` — expect PASS.
- [ ] **Step 5: Commit** — `git add web/src && git commit -m "feat(web): epic drawer per-attempt/stage/model usage"`

---

## Task 18: Project-page rollup

**Files:**
- Modify: the project view component (`web/src/components/board/…` project detail) + a `useProjectUsage.ts` hook
- Test: matching `*.test.tsx`

- [ ] **Step 1: Write the failing test** — project view shows total tokens/$/time + a by-stage and by-model summary from `/orchestrator/projects/{id}/usage`.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** the hook + summary UI (bars per `dataviz`).
- [ ] **Step 4: Run** full web gate `npm run typecheck && npm run test:run` — expect PASS.
- [ ] **Step 5: Commit** — `git add web/src && git commit -m "feat(web): project-page usage rollup"`

---

## Final verification

- [ ] `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...` — all green.
- [ ] `cd web && npm run typecheck && npm run test:run` — all green.
- [ ] `make build` — SPA + agents embed cleanly.
- [ ] Whole-branch review (`/multi-review <merge-base>..HEAD --codex`).
- [ ] Deploy note: rebuild hub first, then agents.
