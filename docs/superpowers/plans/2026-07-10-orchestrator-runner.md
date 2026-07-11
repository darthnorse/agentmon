# Orchestrator Runner — Implementation Plan (Sub-project 2 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make agents execute the orchestrator's kickoff commands and carry runner stage reports back to the hub — loopback report intake + ack-on-next-drain buffered store on the agent, command execution on session create (both ends), KillSession wiring for Cancel/Retry, the `agentmon report` / `doctor` / `import-epics` / `install-skills` CLI, and installer distribution of the runner skills.

**Architecture:** New agent package `agent/internal/report` (store + intake + drain handlers, mirroring the `/hook` middleware), a new tmux helper (pane→session resolution), a command parameter threaded through `tmux.CreateSession` → `api.CreateSessionHandler` → hub `ServerCreateSessionHandler`, the ack-cursor drain protocol through `registry.Client` → `orchestrator.drainReports`, `KillSession` added to the orchestrator's `AgentAPI` seam, and four new `agentmon-agent` subcommands reached via an installed `agentmon` symlink. Skills are embedded in the agent binary (`agent/internal/runnerfiles`) — the fleet update loop distributes them.
Design doc (authoritative for WHY): `docs/superpowers/specs/2026-07-10-orchestrator-runner-design.md`. "Design doc §N" below refers to it.

**Tech Stack:** Go 1.26, stdlib `net/http`/`http.ServeMux`/`embed`, `github.com/BurntSushi/toml` (already an agent dep). **No new module dependencies.**

## Global Constraints

- Branch: all work on `feat/orchestrator-runner`. **Never push. Never add a Co-Authored-By or any other trailer.** Commit messages exactly as given per task.
- Workspace: `go.work` at repo root spans `shared/`, `agent/`, `hubd/`. Before EVERY commit run the full gate and require green:
  `cd /root/agentmon/shared && go build ./... && go test ./... && cd /root/agentmon/agent && go build ./... && go test ./... && cd /root/agentmon/hubd && go build ./... && go test ./...`
  (If the Go build cache is read-only in your sandbox: `export GOCACHE=/tmp/agentmon-go-cache` first.)
- Touch only files the current task lists. The three files under `agent/internal/runnerfiles/files/` (two Claude skills + one Codex playbook) **already exist on this branch, authored separately — never create, edit, or reformat them**; Task 17 only embeds and installs them.
- Where a task anchors to existing code ("mirror X at file:line"), open that file first — this plan is authoritative for WHERE to look, the code on disk for exact names.
- **Stop-don't-improvise:** any mismatch between this plan and the repo — a signature, a path, a test that fails for an unexplainable reason — STOP the task, record the mismatch, and report. Trivial mechanical fixes (missing import, gofmt) excepted.
- **Checkpoint stops are hard stops:** Tasks 6, 12, and 16 end with a CHECKPOINT step. STOP there, report (tasks completed, suite status), and WAIT for explicit fix instructions or an explicit "continue". Do not begin the next task on your own.
- JSON error bodies on the agent: `{"error":"…"}` (the `writeJSONError` shape in `agent/internal/api/sessions.go:266`).
- All timestamps: RFC3339 UTC strings; injected clocks (`func() time.Time` / `func() string`) for testability.
- Commit style: `feat(agent): …` / `feat(hub): …` / `feat(shared): …` / `docs: …`.

## Shared type registry (single source of truth for cross-task names)

| Name | Defined in | Shape |
|---|---|---|
| `shared.OrchestratorReportBatch` | Task 1 | `{Instance string "json:instance"; Cursor uint64 "json:cursor"; Reports []OrchestratorReport "json:reports"}` |
| `tmux.SessionNameForPane` | Task 2 | `func(ctx context.Context, run Runner, socket, pane string) (string, error)` |
| `report.Store` | Task 3 | `NewStore(instance string, max int) *Store`; `Add(target string, r shared.OrchestratorReport)`; `Drain(target, instance string, ack uint64) shared.OrchestratorReportBatch` |
| `report.NewInstanceID` | Task 3 | `func() string` (16 hex chars) |
| `report.DefaultCap` | Task 3 | `const = 256` |
| `hooks.SocketFromTmux` | Task 4 | exported rename of `socketFromTmux(tmuxEnv string) string` |
| `hooks.MatchTarget` | Task 4 | `func(cfg config.Config, socket string) (config.Target, bool)` (was unexported, returned label string) |
| `report.SessionResolver` | Task 4 | `func(ctx context.Context, socket, pane string) (string, error)` |
| `report.IntakeHandler` | Task 4 | `func(cfg config.Config, st *Store, resolve SessionResolver, now func() time.Time) http.HandlerFunc` |
| `report.DrainHandler` | Task 5 | `func(cfg config.Config, st *Store) http.HandlerFunc` |
| `tmux.CreateSession` (new sig) | Task 7 | `func(ctx context.Context, run Runner, socket, name, cwd, command string) error` |
| `api.SessionCreator` (new sig) | Task 8 | `func(ctx context.Context, socket, name, cwd, command string) error` |
| `registry.Client.DrainReports` (new sig) | Task 10 | `func(ctx, srv db.Server, target, instance string, ack uint64) (shared.OrchestratorReportBatch, error)` |
| `orchestrator.AgentAPI` (new) | Task 11 | adds `KillSession(ctx, srv db.Server, target, name string) error`; `DrainReports` takes Task 10's signature |
| `orchestrator.drainAck` | Task 11 | `struct{ Instance string; Cursor uint64 }`; field `Orchestrator.ackState map[string]drainAck` |
| `orchestrator.(*Orchestrator).killEpicSession` | Task 12 | `func(ctx context.Context, e db.Epic, phase string)` |
| `reportMain` / `postReport` / `repoFromGit` / `normalizeRepoURL` | Task 13 | `report_cli.go`: `postReport(cfgPath string, payload map[string]any, dryRun bool) (string, error)`; `repoFromGit(dir string) (string, error)`; `normalizeRepoURL(u string) (string, error)` |
| `epicfile.Epic` / `Parse` / `StampIssue` | Task 14 | `Epic{Path, Title string; Labels, BlockedBy []string; Issue int; Body string}`; `Parse(path string) (Epic, error)`; `StampIssue(path string, n int) error` |
| `cmdRunner` / `execRunner` | Task 15 | `type cmdRunner func(dir, name string, args ...string) (string, error)` in `import_epics_cli.go` |
| `importEpicsMain` / `importEpics` / `resolveRef` | Task 15 | `importEpics(args []string, stdout io.Writer, run cmdRunner) error` |
| `doctorMain` / `doctorRun` | Task 16 | `doctorRun(args []string, stdout io.Writer, run cmdRunner, look func(string) (string, error), home func() (string, error)) error` |
| `runnerfiles.InstallSkills` | Task 17 | `func(home string) ([]string, error)` |
| `installSkillsMain` | Task 17 | `func(args []string, stdout io.Writer) error` |

---

### Task 1: `shared` — OrchestratorReportBatch

**Files:**
- Modify: `shared/orchestrator.go` (append at end)
- Test: `shared/orchestrator_test.go` (append; keep all pre-existing tests unchanged)

**Interfaces:**
- Consumes: `shared.OrchestratorReport` (exists).
- Produces: `shared.OrchestratorReportBatch` — the drain wire format for Tasks 3, 5, 10, 11.

- [x] **Step 1: Write the failing test**

Append to `shared/orchestrator_test.go`:

```go
func TestOrchestratorReportBatchJSONShape(t *testing.T) {
	b, err := json.Marshal(OrchestratorReportBatch{
		Instance: "a1b2", Cursor: 7,
		Reports: []OrchestratorReport{{Repo: "o/r", Epic: 3, Stage: EpicPlanning, Session: "epic-p-3", Ts: "t"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"instance":"a1b2"`, `"cursor":7`, `"reports":[{`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}
```

If `strings` or `encoding/json` are not already imported in the test file, add them.

- [x] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/shared && go test ./... -run TestOrchestratorReportBatchJSONShape`
Expected: FAIL — `undefined: OrchestratorReportBatch`.

- [x] **Step 3: Write the type**

Append to `shared/orchestrator.go`:

```go
// OrchestratorReportBatch is one drain response (ack-on-next-drain protocol).
// Instance identifies the agent store's lifetime (minted at agent start): an
// ack whose instance does not match the store's current one deletes nothing,
// so a hub cursor that predates an agent restart can never delete fresh
// reports. Cursor is the highest buffered seq contained in Reports (0 when
// empty); the hub echoes instance+cursor on its NEXT drain to acknowledge —
// at-least-once delivery, duplicates rejected by the hub's guarded transitions.
type OrchestratorReportBatch struct {
	Instance string               `json:"instance"`
	Cursor   uint64               `json:"cursor"`
	Reports  []OrchestratorReport `json:"reports"`
}
```

- [x] **Step 4: Run the full gate**

Run the Global Constraints gate command.
Expected: PASS everywhere (hubd still compiles — nothing references the new type yet).

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add shared/ && git commit -m "feat(shared): OrchestratorReportBatch — ack-on-next-drain wire format"
```

---

### Task 2: `tmux` — SessionNameForPane

**Files:**
- Create: `agent/internal/tmux/session_name.go`
- Test: `agent/internal/tmux/session_name_test.go`

**Interfaces:**
- Consumes: `tmux.Runner`, `with`, `socketArgs` (all in `agent/internal/tmux/discovery.go:27,144,150`).
- Produces: `SessionNameForPane(ctx, run, socket, pane) (string, error)` — Task 4's production resolver.

- [x] **Step 1: Write the failing tests**

Create `agent/internal/tmux/session_name_test.go` (the `recordRunner` helper already exists in `create_test.go`, same package):

```go
package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSessionNameForPaneArgArray(t *testing.T) {
	var got []string
	run := recordRunner([]byte("epic-proj-16\n"), nil, &got)
	name, err := SessionNameForPane(context.Background(), run, "agentmon", "%5")
	if err != nil || name != "epic-proj-16" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	want := []string{"-L", "agentmon", "display-message", "-p", "-t", "%5", "#{session_name}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestSessionNameForPaneDefaultSocket(t *testing.T) {
	var got []string
	run := recordRunner([]byte("s1\n"), nil, &got)
	if _, err := SessionNameForPane(context.Background(), run, "", "%0"); err != nil {
		t.Fatal(err)
	}
	if got[0] != "display-message" {
		t.Fatalf("default socket must add no -L flag: %#v", got)
	}
}

func TestSessionNameForPaneErrors(t *testing.T) {
	run := recordRunner(nil, errors.New("can't find pane %9"), new([]string))
	if _, err := SessionNameForPane(context.Background(), run, "", "%9"); err == nil {
		t.Fatal("runner error must propagate")
	}
	empty := recordRunner([]byte("\n"), nil, new([]string))
	if _, err := SessionNameForPane(context.Background(), empty, "", "%1"); err == nil {
		t.Fatal("empty session name must error")
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/tmux/ -run TestSessionNameForPane`
Expected: FAIL — `undefined: SessionNameForPane`.

- [x] **Step 3: Implement**

Create `agent/internal/tmux/session_name.go`:

```go
package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SessionNameForPane resolves the session that owns pane (an exact %N pane
// id — the caller validates with ValidatePaneID) on the given socket, via the
// arg-array Runner (no shell). This is the report intake's server-side session
// stamp: the CLI's own session claim would be unauthenticated, so the agent
// asks tmux which session the calling pane actually belongs to (design doc §3).
func SessionNameForPane(ctx context.Context, run Runner, socket, pane string) (string, error) {
	out, err := run(ctx, with(socketArgs(socket), "display-message", "-p", "-t", pane, "#{session_name}")...)
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w", err)
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", errors.New("tmux returned an empty session name")
	}
	return name, nil
}
```

- [x] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/tmux/ -run TestSessionNameForPane` → PASS, then the full gate → PASS.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): tmux.SessionNameForPane — pane-to-session resolution for the report intake"
```

---

### Task 3: `report` — buffered Store with ack semantics

**Files:**
- Create: `agent/internal/report/store.go`
- Test: `agent/internal/report/store_test.go`

**Interfaces:**
- Consumes: `shared.OrchestratorReport`, `shared.OrchestratorReportBatch` (Task 1).
- Produces: `NewStore(instance, max) *Store`, `(*Store).Add(target, r)`, `(*Store).Drain(target, instance, ack) shared.OrchestratorReportBatch`, `NewInstanceID() string`, `DefaultCap`.

- [x] **Step 1: Write the failing tests**

Create `agent/internal/report/store_test.go`:

```go
package report

import (
	"fmt"
	"testing"

	"agentmon/shared"
)

func rep(epic int) shared.OrchestratorReport {
	return shared.OrchestratorReport{Repo: "o/r", Epic: epic, Stage: shared.EpicPlanning, Session: "s", Ts: "t"}
}

func TestDrainReturnsBufferedWithCursor(t *testing.T) {
	s := NewStore("inst", 10)
	s.Add("default", rep(1))
	s.Add("default", rep(2))
	b := s.Drain("default", "", 0)
	if b.Instance != "inst" || b.Cursor != 2 || len(b.Reports) != 2 || b.Reports[0].Epic != 1 {
		t.Fatalf("batch = %+v", b)
	}
	// Nothing acked yet: a re-drain redelivers the same batch.
	b2 := s.Drain("default", "", 0)
	if b2.Cursor != 2 || len(b2.Reports) != 2 {
		t.Fatalf("redelivery batch = %+v", b2)
	}
}

func TestAckDeletesOnlyMatchingInstanceTargetAndSeq(t *testing.T) {
	s := NewStore("inst", 10)
	s.Add("default", rep(1))
	s.Add("other", rep(2))
	s.Add("default", rep(3))
	// Wrong instance: deletes nothing.
	if b := s.Drain("default", "stale", 3); len(b.Reports) != 2 {
		t.Fatalf("stale instance must not delete: %+v", b)
	}
	// Right instance, ack seq 1: deletes only default's seq 1; seq 3 remains.
	b := s.Drain("default", "inst", 1)
	if len(b.Reports) != 1 || b.Reports[0].Epic != 3 || b.Cursor != 3 {
		t.Fatalf("batch = %+v", b)
	}
	// The other target was untouched.
	if b := s.Drain("other", "inst", 0); len(b.Reports) != 1 || b.Reports[0].Epic != 2 {
		t.Fatalf("other target = %+v", b)
	}
}

func TestEmptyDrainHasZeroCursor(t *testing.T) {
	s := NewStore("inst", 10)
	b := s.Drain("default", "", 0)
	if b.Cursor != 0 || b.Reports == nil || len(b.Reports) != 0 {
		t.Fatalf("empty batch = %+v", b)
	}
}

func TestOverflowDropsOldest(t *testing.T) {
	s := NewStore("inst", 3)
	for i := 1; i <= 5; i++ {
		s.Add("default", rep(i))
	}
	b := s.Drain("default", "", 0)
	if len(b.Reports) != 3 || b.Reports[0].Epic != 3 || b.Reports[2].Epic != 5 {
		t.Fatalf("overflow batch = %+v", b)
	}
}

func TestNewInstanceID(t *testing.T) {
	a, b := NewInstanceID(), NewInstanceID()
	if len(a) != 16 || a == b {
		t.Fatalf("instance ids: %q %q", a, b)
	}
	if _, err := fmt.Sscanf(a, "%x", new(uint64)); err != nil {
		t.Fatalf("not hex: %q", a)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/report/`
Expected: FAIL to build — package does not exist yet.

- [x] **Step 3: Implement the store**

Create `agent/internal/report/store.go`:

```go
// Package report implements the agent's orchestrator-report path: a loopback
// POST buffers runner stage reports; the hub drains them over its poll channel
// with an ack-on-next-drain cursor protocol (design doc §3–§4). The buffer is
// in-memory by design (D7): an agent restart loses at most a poll interval of
// reports, and GitHub reconcile covers the gap.
package report

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"

	"agentmon/shared"
)

// DefaultCap bounds the buffer (mirrors the hub's maxPendingReports).
const DefaultCap = 256

type entry struct {
	seq    uint64
	target string
	r      shared.OrchestratorReport
}

// Store buffers reports until the hub acknowledges receipt. Drain(target,
// instance, ack) first deletes that target's entries with seq <= ack IF
// instance matches this store's lifetime id (a stale instance from before an
// agent restart must never delete fresh reports — D14), then returns every
// remaining entry for the target. At-least-once: a batch the hub never acks
// is simply redelivered; the hub's guarded transitions reject duplicates.
type Store struct {
	mu       sync.Mutex
	instance string
	max      int
	nextSeq  uint64
	entries  []entry
}

func NewStore(instance string, max int) *Store {
	if max <= 0 {
		max = DefaultCap
	}
	return &Store{instance: instance, max: max, nextSeq: 1}
}

// NewInstanceID mints the random 16-hex-char store-lifetime identifier.
// crypto/rand.Read never fails on supported platforms (Go ≥1.24 guarantee).
func NewInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add buffers one report for target. On overflow the OLDEST entry is dropped:
// the newest report carries the freshest stage, and intermediate history is
// recoverable from GitHub state (D7).
func (s *Store) Add(target string, r shared.OrchestratorReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry{seq: s.nextSeq, target: target, r: r})
	s.nextSeq++
	if len(s.entries) > s.max {
		d := s.entries[0]
		s.entries = s.entries[1:]
		log.Printf("report: buffer full — dropped oldest (target=%q epic=%d stage=%s seq=%d)", d.target, d.r.Epic, d.r.Stage, d.seq)
	}
}

// Drain implements the ack-on-next-drain protocol for one target.
func (s *Store) Drain(target, instance string, ack uint64) shared.OrchestratorReportBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if instance == s.instance && ack > 0 {
		kept := s.entries[:0]
		for _, e := range s.entries {
			if e.target == target && e.seq <= ack {
				continue
			}
			kept = append(kept, e)
		}
		s.entries = kept
	}
	batch := shared.OrchestratorReportBatch{Instance: s.instance, Reports: []shared.OrchestratorReport{}}
	for _, e := range s.entries {
		if e.target != target {
			continue
		}
		batch.Reports = append(batch.Reports, e.r)
		if e.seq > batch.Cursor {
			batch.Cursor = e.seq
		}
	}
	return batch
}
```

- [x] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/report/` → PASS, then the full gate → PASS.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): report.Store — buffered orchestrator reports with ack-on-next-drain semantics"
```

---

### Task 4: hooks exports + report IntakeHandler

**Files:**
- Modify: `agent/internal/hooks/hooks.go` (export two helpers; `MatchTarget` returns `config.Target`)
- Create: `agent/internal/report/intake.go`
- Test: `agent/internal/report/intake_test.go`

**Interfaces:**
- Consumes: `hooks.SocketFromTmux` / `hooks.MatchTarget` (renamed here), `tmux.ValidatePaneID` (`agent/internal/tmux/control.go:58`), `report.Store` (Task 3), `shared.ReportableStage`.
- Produces: `report.SessionResolver`, `report.IntakeHandler`, package-local `writeError` (Task 5 reuses it).

- [x] **Step 1: Export the two `/hook` resolution helpers**

In `agent/internal/hooks/hooks.go`:
1. Rename `socketFromTmux` → `SocketFromTmux` (function at line 107; update its doc comment first word and BOTH call sites: `HookHandler` line 68 and the `epochFromTmux` sibling is unrelated — only `socketFromTmux` calls change).
2. Change `matchTarget` (line 123) to:

```go
// MatchTarget maps a tmux socket name to its configured target. The default
// socket is named "default" on disk but configured as SocketName "". Exported
// for the report intake, which needs the target's SocketName (tmux calls) as
// well as its Label (store key).
func MatchTarget(cfg config.Config, socket string) (config.Target, bool) {
	if socket == "" {
		return config.Target{}, false
	}
	for _, t := range cfg.Targets {
		if t.SocketName == socket || (socket == "default" && t.SocketName == "") {
			return t, true
		}
	}
	return config.Target{}, false
}
```

3. Update `HookHandler` accordingly (lines 68–70, 82–90): `t, matched := MatchTarget(cfg, socket)` and `Target: t.Label` in the `state.Event` literal.

Run: `cd /root/agentmon/agent && go build ./... && go test ./internal/hooks/`
Expected: PASS (the hooks tests exercise the handler, not the unexported helpers directly).

- [x] **Step 2: Write the failing intake tests**

Create `agent/internal/report/intake_test.go`:

```go
package report

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/agent/internal/config"
)

func testCfg() config.Config {
	return config.Config{Targets: []config.Target{{Label: "default", SocketName: "agentmon"}}}
}

func okResolver(session string) SessionResolver {
	return func(_ context.Context, socket, pane string) (string, error) {
		if socket != "agentmon" || pane != "%5" {
			return "", errors.New("unexpected resolver args")
		}
		return session, nil
	}
}

func intakePost(t *testing.T, st *Store, resolve SessionResolver, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC) }
	h := IntakeHandler(testCfg(), st, resolve, now)
	r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	r.Header.Set("X-AgentMon-Pane", "%5")
	r.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/agentmon,123,0")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestIntakeBuffersServerStampedReport(t *testing.T) {
	st := NewStore("i", 10)
	w := intakePost(t, st, okResolver("epic-proj-16"), "/orchestrator/report",
		`{"repo":"o/r","epic":16,"stage":"planning","note":"n"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	b := st.Drain("default", "", 0)
	if len(b.Reports) != 1 {
		t.Fatalf("buffered = %+v", b)
	}
	r := b.Reports[0]
	if r.Session != "epic-proj-16" || r.Ts != "2026-07-10T14:00:00Z" || r.Epic != 16 || r.Note != "n" {
		t.Fatalf("report = %+v", r)
	}
}

func TestIntakeDryRunValidatesWithoutBuffering(t *testing.T) {
	st := NewStore("i", 10)
	w := intakePost(t, st, okResolver("s1"), "/orchestrator/report?dry_run=1",
		`{"repo":"o/r","epic":1,"stage":"planning"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"session":"s1"`) {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if b := st.Drain("default", "", 0); len(b.Reports) != 0 {
		t.Fatalf("dry_run must not buffer: %+v", b)
	}
}

func TestIntakeRejections(t *testing.T) {
	st := NewStore("i", 10)
	cases := []struct {
		name, body string
		resolve    SessionResolver
	}{
		{"non-reportable stage", `{"repo":"o/r","epic":1,"stage":"merged"}`, okResolver("s")},
		{"zero epic", `{"repo":"o/r","epic":0,"stage":"planning"}`, okResolver("s")},
		{"bad json", `{`, okResolver("s")},
		{"resolver failure", `{"repo":"o/r","epic":1,"stage":"planning"}`,
			func(_ context.Context, _, _ string) (string, error) { return "", errors.New("no pane") }},
	}
	for _, c := range cases {
		if w := intakePost(t, st, c.resolve, "/orchestrator/report", c.body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code %d body %s", c.name, w.Code, w.Body)
		}
	}
}

func TestIntakeRejectsUnknownSocketOrBadPane(t *testing.T) {
	st := NewStore("i", 10)
	h := IntakeHandler(testCfg(), st, okResolver("s"), nil)
	r := httptest.NewRequest(http.MethodPost, "/orchestrator/report", strings.NewReader(`{"epic":1,"stage":"planning"}`))
	r.Header.Set("X-AgentMon-Pane", "%5")
	r.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/othersock,1,0")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown socket: code %d", w.Code)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/orchestrator/report", strings.NewReader(`{"epic":1,"stage":"planning"}`))
	r2.Header.Set("X-AgentMon-Pane", "not-a-pane")
	r2.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/agentmon,1,0")
	w2 := httptest.NewRecorder()
	h(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("bad pane: code %d", w2.Code)
	}
}
```

- [x] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/report/`
Expected: FAIL — `undefined: IntakeHandler`, `undefined: SessionResolver`.

- [x] **Step 4: Implement the intake**

Create `agent/internal/report/intake.go`:

```go
package report

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/hooks"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// maxReportBody caps the intake body — a small JSON object (aligned with the
// agent's maxCreateBody). The note rides inside this cap.
const maxReportBody = 8 << 10

// reportTmuxTimeout bounds the session-resolution shell-out (mirrors
// api.agentTmuxTimeout). var so tests can shorten it.
var reportTmuxTimeout = 10 * time.Second

// SessionResolver resolves the session name owning a pane on a socket — the
// DI seam for IntakeHandler (production binds tmux.SessionNameForPane).
type SessionResolver func(ctx context.Context, socket, pane string) (string, error)

type intakeBody struct {
	Repo  string `json:"repo"`
	Epic  int    `json:"epic"`
	Stage string `json:"stage"`
	Note  string `json:"note"`
	PR    int    `json:"pr"`
}

// IntakeHandler serves POST /orchestrator/report — loopback + hook-token
// (mounted behind the same middleware as /hook). Unlike /hook, which soft-
// drops so a coding agent never stalls, intake failures are HARD 400s: the
// report CLI is load-bearing and must know. Session and Ts are stamped
// SERVER-SIDE — the CLI's session claim would be unauthenticated, so the
// agent resolves the calling pane's session via tmux instead (design doc §3).
// ?dry_run=1 validates everything (including session resolution) without
// buffering — the doctor's connectivity probe.
func IntakeHandler(cfg config.Config, st *Store, resolve SessionResolver, now func() time.Time) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		pane := r.Header.Get("X-AgentMon-Pane")
		socket := hooks.SocketFromTmux(r.Header.Get("X-AgentMon-Tmux"))
		t, matched := hooks.MatchTarget(cfg, socket)
		if !tmux.ValidatePaneID(pane) || !matched {
			writeError(w, http.StatusBadRequest, "report must originate from a tmux pane on a configured target")
			return
		}
		var body intakeBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBody)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.Epic <= 0 {
			writeError(w, http.StatusBadRequest, "epic must be a positive issue number")
			return
		}
		if !shared.ReportableStage(shared.EpicStage(body.Stage)) {
			writeError(w, http.StatusBadRequest, "stage is not runner-reportable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), reportTmuxTimeout)
		defer cancel()
		session, err := resolve(ctx, t.SocketName, pane)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot resolve tmux session for pane")
			return
		}
		rep := shared.OrchestratorReport{
			Repo: body.Repo, Epic: body.Epic, Stage: shared.EpicStage(body.Stage),
			Note: body.Note, PR: body.PR, Session: session,
			Ts: now().UTC().Format(time.RFC3339),
		}
		if r.URL.Query().Get("dry_run") != "1" {
			st.Add(t.Label, rep)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"session": session})
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

- [x] **Step 5: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/report/ ./internal/hooks/` → PASS, then the full gate → PASS.

- [x] **Step 6: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): orchestrator report intake — loopback POST with server-side session stamping"
```

---

### Task 5: `report` — DrainHandler

**Files:**
- Create: `agent/internal/report/drain.go`
- Test: `agent/internal/report/drain_test.go`

**Interfaces:**
- Consumes: `report.Store` (Task 3), `config.Config.ResolveTarget` (`agent/internal/config/config.go:60`), `writeError` (Task 4).
- Produces: `DrainHandler(cfg, st) http.HandlerFunc` — mounted in Task 6, dialed by Task 10.

- [x] **Step 1: Write the failing tests**

Create `agent/internal/report/drain_test.go`:

```go
package report

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/shared"
)

func drainGet(t *testing.T, st *Store, url string) *httptest.ResponseRecorder {
	t.Helper()
	h := DrainHandler(testCfg(), st)
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func TestDrainHandlerAckThenReturnRemainder(t *testing.T) {
	st := NewStore("inst", 10)
	st.Add("default", rep(1))
	st.Add("default", rep(2))

	w := drainGet(t, st, "/orchestrator/reports?target=default")
	var b shared.OrchestratorReportBatch
	if err := json.NewDecoder(w.Body).Decode(&b); err != nil || w.Code != 200 {
		t.Fatalf("code %d err %v", w.Code, err)
	}
	if b.Instance != "inst" || b.Cursor != 2 || len(b.Reports) != 2 {
		t.Fatalf("batch = %+v", b)
	}

	st.Add("default", rep(3))
	w2 := drainGet(t, st, "/orchestrator/reports?target=default&instance=inst&ack=2")
	var b2 shared.OrchestratorReportBatch
	_ = json.NewDecoder(w2.Body).Decode(&b2)
	if len(b2.Reports) != 1 || b2.Reports[0].Epic != 3 || b2.Cursor != 3 {
		t.Fatalf("post-ack batch = %+v", b2)
	}
}

func TestDrainHandlerEmptyIsJSONArrayNotNull(t *testing.T) {
	st := NewStore("inst", 10)
	w := drainGet(t, st, "/orchestrator/reports")
	if !strings.Contains(w.Body.String(), `"reports":[]`) {
		t.Fatalf("empty drain must encode []: %s", w.Body)
	}
}

func TestDrainHandlerErrors(t *testing.T) {
	st := NewStore("inst", 10)
	if w := drainGet(t, st, "/orchestrator/reports?target=nope"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown target: code %d", w.Code)
	}
	if w := drainGet(t, st, "/orchestrator/reports?ack=banana"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad ack: code %d", w.Code)
	}
}
```

Note: `testCfg` and `rep` come from Tasks 3–4's test files (same package). An empty `?target=` resolves to the FIRST configured target (`ResolveTarget` semantics) — that is why `TestDrainHandlerEmptyIsJSONArrayNotNull` works without a target param.

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/report/`
Expected: FAIL — `undefined: DrainHandler`.

- [x] **Step 3: Implement**

Create `agent/internal/report/drain.go`:

```go
package report

import (
	"encoding/json"
	"net/http"
	"strconv"

	"agentmon/agent/internal/config"
)

// DrainHandler serves GET /orchestrator/reports?target=&instance=&ack= —
// hub-bearer-authed (mounted behind api.RequireBearer; NOT loopback — the hub
// dials it). Ack-on-next-drain: instance+ack acknowledge (and delete) the
// batch the hub received on its PREVIOUS poll; the response carries everything
// still buffered for the target (design doc §4). GET-with-deletion is
// deliberate and matches the protocol: the deletion is the ack of already-
// delivered data, never of the data being returned.
func DrainHandler(cfg config.Config, st *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeError(w, http.StatusNotFound, "unknown target")
			return
		}
		var ack uint64
		if s := r.URL.Query().Get("ack"); s != "" {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid ack cursor")
				return
			}
			ack = v
		}
		batch := st.Drain(t.Label, r.URL.Query().Get("instance"), ack)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(batch)
	}
}
```

- [x] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/report/` → PASS, then the full gate → PASS.

- [x] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): report drain endpoint — ack-on-next-drain protocol"
```

---

### Task 6: wire the report routes in the agent main + CHECKPOINT 1

**Files:**
- Modify: `agent/cmd/agentmon-agent/main.go`

**Interfaces:**
- Consumes: everything from Tasks 2–5.
- Produces: live routes `GET /orchestrator/reports` (bearer) and `POST /orchestrator/report` (loopback+token).

- [x] **Step 1: Wire the store and routes**

In `agent/cmd/agentmon-agent/main.go`:

1. Add `"agentmon/agent/internal/report"` to imports.
2. After the `machine := state.New(nil)` line (main.go:81), add:

```go
	reportStore := report.NewStore(report.NewInstanceID(), report.DefaultCap)
	resolveSession := func(ctx context.Context, socket, pane string) (string, error) {
		return tmux.SessionNameForPane(ctx, tmux.ExecRunner, socket, pane)
	}
```

3. After the `mux.Handle("GET /state", …)` line (main.go:90), add:

```go
	mux.Handle("GET /orchestrator/reports", api.RequireBearer(cfg.HubToken, report.DrainHandler(cfg, reportStore)))
```

4. Inside the `if cfg.HookToken != ""` block (main.go:104–112), after the `/hook` mount, add:

```go
		mux.Handle("POST /orchestrator/report", hooks.RequireLoopback(hooks.RequireHookAuth(cfg.HookToken, report.IntakeHandler(cfg, reportStore, resolveSession, nil))))
		log.Printf("orchestrator report intake enabled at POST /orchestrator/report")
```

(The drain endpoint stays mounted regardless of HookToken — with the intake disabled it simply serves empty batches, and the hub cannot tell a hookless agent from a quiet one, which is fine.)

- [x] **Step 2: Run the full gate**

Run the Global Constraints gate command.
Expected: PASS.

- [x] **Step 3: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): mount orchestrator report intake + drain routes"
```

- [x] **Step 4: CHECKPOINT 1 — STOP**

Report: tasks 1–6 committed, full gate green. WAIT for explicit fix instructions or "continue". Do NOT begin Task 7.

---

### Task 7: `tmux.CreateSession` — optional command

**Files:**
- Modify: `agent/internal/tmux/create.go:26-39` (CreateSession)
- Modify: `agent/internal/tmux/create_test.go` (existing CreateSession calls gain a `""` arg)

**Interfaces:**
- Consumes: existing `with`/`socketArgs`/`Runner`.
- Produces: `CreateSession(ctx, run, socket, name, cwd, command string) error` — Task 8's exec target.

- [ ] **Step 1: Write the failing test**

Append to `agent/internal/tmux/create_test.go`:

```go
func TestCreateSessionWithCommandArgArray(t *testing.T) {
	var got []string
	run := recordRunner(nil, nil, &got)
	cmd := `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline 16"`
	if err := CreateSession(context.Background(), run, "mysock", "epic-p-16", "/w", cmd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	want := []string{"-L", "mysock", "new-session", "-d", "-s", "epic-p-16", "-c", "/w", cmd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
```

- [ ] **Step 2: Update the existing tests' calls and verify compile failure**

Every existing `CreateSession(` call in `create_test.go` gains a trailing `""` argument (empty command — e.g. `CreateSession(context.Background(), run, "mysock", "proj", "/tmp", "")`). Their `want` argv arrays stay EXACTLY as they are — that is the regression assertion that an empty command changes nothing.

Run: `cd /root/agentmon/agent && go test ./internal/tmux/ -run TestCreateSession`
Expected: FAIL to build — signature mismatch (and `main.go` will fail the build too; that call site is fixed in Task 8, so use the package-scoped test run above, not the full gate, for this red step).

- [ ] **Step 3: Implement**

In `agent/internal/tmux/create.go`, change `CreateSession` to:

```go
// CreateSession starts a new detached tmux session named name with working
// directory cwd on the given socket, via the arg-array Runner seam (no shell —
// name, cwd, and command are positional args, never interpolated; §13.6).
//
// A non-empty command becomes the session's shell-command argument: tmux runs
// it via `sh -c`, and the session ENDS when it exits — which is exactly the
// runner contract's normal exit (report pr_open, then quit; design doc §5).
// Empty command → the user's default shell, byte-for-byte today's behavior.
//
// The caller MUST have already validated name (shared.ValidateSessionName) and
// cwd (ValidateCwd); CreateSession is the exec boundary, not the policy boundary.
// A tmux "duplicate session" failure is mapped to ErrSessionExists.
func CreateSession(ctx context.Context, run Runner, socket, name, cwd, command string) error {
	args := with(socketArgs(socket), "new-session", "-d", "-s", name, "-c", cwd)
	if command != "" {
		args = append(args, command)
	}
	out, err := run(ctx, args...)
	if err != nil {
		if isDuplicateSession(out) || isDuplicateSession([]byte(err.Error())) {
			return ErrSessionExists
		}
		return fmt.Errorf("tmux new-session: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
```

Update the ONE production call site in `agent/cmd/agentmon-agent/main.go:71-73` to pass the extra parameter through (the closure's own signature changes in Task 8; for THIS task just add a trailing `""`):

```go
	createSession := func(ctx context.Context, socket, name, cwd string) error {
		return tmux.CreateSession(ctx, tmux.ExecRunner, socket, name, cwd, "")
	}
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/tmux/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): tmux.CreateSession takes an optional session command"
```

---

### Task 8: agent CreateSessionHandler accepts Command

**Files:**
- Modify: `agent/internal/api/sessions.go:93-153` (SessionCreator type + CreateSessionHandler)
- Modify: `agent/cmd/agentmon-agent/main.go:71-73` (closure signature)
- Modify: `agent/internal/api/sessions_test.go` (creator fakes gain the param; the rejection test is REPLACED)

**Interfaces:**
- Consumes: `tmux.CreateSession` (Task 7 signature).
- Produces: `api.SessionCreator = func(ctx, socket, name, cwd, command string) error`; the handler forwards `req.Command`.

- [ ] **Step 1: Replace the rejection test with a forwarding test**

In `agent/internal/api/sessions_test.go`: DELETE `TestCreateSessionHandlerCommandRejected400` (line 322) entirely and add:

```go
func TestCreateSessionHandlerForwardsCommand(t *testing.T) {
	dir := t.TempDir()
	var gotName, gotCwd, gotCommand string
	create := func(_ context.Context, _, name, cwd, command string) error {
		gotName, gotCwd, gotCommand = name, cwd, command
		return nil
	}
	cfg := config.Config{Targets: []config.Target{{Label: "default", SocketName: "agentmon"}}, SessionDirs: []string{dir}}
	body := fmt.Sprintf(`{"name":"epic-p-16","cwd":%q,"command":"claude \"/epic-pipeline 16\""}`, dir)
	r := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(body))
	w := httptest.NewRecorder()
	CreateSessionHandler(cfg, create)(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if gotName != "epic-p-16" || gotCwd == "" || gotCommand != `claude "/epic-pipeline 16"` {
		t.Fatalf("creator got name=%q cwd=%q command=%q", gotName, gotCwd, gotCommand)
	}
}
```

(Add `fmt`/`strings` imports if missing. `gotCwd` is only checked non-empty because ValidateCwd resolves symlinks — e.g. `/tmp` → `/private/tmp` differences don't matter here.)

Every OTHER fake `SessionCreator` closure in this test file gains the extra `command string` parameter (compiler-guided; behavior unchanged).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/api/ -run TestCreateSessionHandler`
Expected: FAIL to build (signature) — then, after mechanical fake updates, FAIL at the removed-rejection assertion until Step 3.

- [ ] **Step 3: Implement**

In `agent/internal/api/sessions.go`:

1. `SessionCreator` (line 97) becomes:

```go
// SessionCreator creates a detached tmux session named name with working
// directory cwd — and, when command is non-empty, running command as the
// session's shell-command (the session ends when it exits). It is the DI seam
// for CreateSessionHandler (mirrors Discoverer): production binds
// tmux.CreateSession + tmux.ExecRunner; tests inject a fake that records its
// arguments.
type SessionCreator func(ctx context.Context, socket, name, cwd, command string) error
```

2. In `CreateSessionHandler`: DELETE the rejection block (lines 119–122):

```go
		if req.Command != "" {
			writeJSONError(w, http.StatusBadRequest, "custom commands are not supported")
			return
		}
```

3. The create call (line 141) becomes `create(ctx, t.SocketName, req.Name, cwd, req.Command)`.
4. Update the handler's doc comment: replace "a non-empty command is rejected (custom commands are not supported in v1)" with "a non-empty command is executed as the session's shell-command (the orchestrator's kickoff path; authz note in design doc D13 — session-create + send-keys already grant arbitrary exec, so this adds no new capability)".
5. In `agent/cmd/agentmon-agent/main.go`, the closure becomes:

```go
	createSession := func(ctx context.Context, socket, name, cwd, command string) error {
		return tmux.CreateSession(ctx, tmux.ExecRunner, socket, name, cwd, command)
	}
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/api/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): execute CreateSessionRequest.Command — lift the shell-only rejection at the exec boundary"
```

---

### Task 9: hub ServerCreateSessionHandler forwards Command

**Files:**
- Modify: `hubd/internal/api/sessions.go:99-105` (delete the early rejection; comment updates)
- Modify: `hubd/internal/api/sessions_test.go:244-254` (rejection test REPLACED by a forwarding test)

**Interfaces:**
- Consumes: `registry.Client.CreateSession` (already marshals the whole `CreateSessionRequest`, `Command` included — no client change).
- Produces: hub-side New-Session-with-command capability (board uses it in sub-3; the orchestrator's own spawn path never passes through this handler).

- [ ] **Step 1: Replace the rejection test**

In `hubd/internal/api/sessions_test.go`: DELETE `TestServerCreateSessionCommandRejectedIs400` (lines 244–254) and add (it uses the existing `depsWith`, `createReq`, `createListBody` helpers in this file):

```go
// TestServerCreateSessionForwardsCommand: the hub forwards a non-empty command
// to the agent verbatim (the agent is the exec boundary; design doc D13 — no
// new capability beyond existing session-create + send-keys).
func TestServerCreateSessionForwardsCommand(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"newproj"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(createListBody))
		}
	}))
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "default", `{"name":"newproj","command":"claude \"/epic-pipeline 4\""}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if !strings.Contains(gotBody, `"command":"claude \"/epic-pipeline 4\""`) {
		t.Fatalf("agent did not receive the command: %s", gotBody)
	}
}
```

(Add `io` to imports if missing.)

- [ ] **Step 2: Run tests to verify the new one fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestServerCreateSessionForwardsCommand`
Expected: FAIL — hub returns 400 "custom commands are not supported".

- [ ] **Step 3: Implement**

In `hubd/internal/api/sessions.go`:

1. DELETE lines 100–105 (the "Reject custom commands early" comment + block).
2. In the handler doc comment (lines 74–80), replace "the agent enforces the cwd allow-list + rejects custom commands (mapped here from its 400)" with "the agent enforces the cwd allow-list and executes an optional command (design doc D13: no new authz permission — session-create + send-keys already grant arbitrary exec on the target)".

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/hubd && go test ./internal/api/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): forward CreateSessionRequest.Command to the agent — New-Session-with-command"
```

---

### Task 10: registry client — ack-cursor DrainReports

**Files:**
- Modify: `hubd/internal/registry/client.go:64-94` (DrainReports)
- Modify: `hubd/internal/registry/client_test.go:240-270` (both drain tests rewritten)

**Interfaces:**
- Consumes: `shared.OrchestratorReportBatch` (Task 1).
- Produces: `DrainReports(ctx, srv, target, instance string, ack uint64) (shared.OrchestratorReportBatch, error)` — Task 11's transport. **Note:** hubd compiles but `orchestrator.AgentAPI` still declares the OLD signature until Task 11 — so this task must ALSO update that interface line and the orchestrator call site minimally, or the gate fails. To keep the diff coherent, do it here: see Step 3 item 3.

- [ ] **Step 1: Rewrite the drain tests**

Replace `TestDrainReports` and `TestDrainReportsOldAgent404` in `hubd/internal/registry/client_test.go` with:

```go
func TestDrainReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/orchestrator/reports" || q.Get("ack") != "7" || q.Get("instance") != "i1" || q.Get("target") != "tgt" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer btok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"instance":"i1","cursor":9,"reports":[{"repo":"o/r","epic":16,"stage":"implementing","session":"epic-16","ts":"t1"}]}`))
	}))
	defer srv.Close()
	c := NewClient(time.Second)
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "btok"}, "tgt", "i1", 7)
	if err != nil || got.Instance != "i1" || got.Cursor != 9 || len(got.Reports) != 1 || got.Reports[0].Stage != shared.EpicImplementing {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestDrainReportsOldAgent404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClient(time.Second)
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "b"}, "", "", 0)
	if err != nil || got.Instance != "" || len(got.Reports) != 0 {
		t.Fatalf("404 must be tolerated as an empty batch: got %+v err=%v", got, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/registry/ -run TestDrainReports`
Expected: FAIL to build — signature mismatch.

- [ ] **Step 3: Implement**

1. In `hubd/internal/registry/client.go`, replace `DrainReports` (lines 64–94) with:

```go
// DrainReports pulls buffered orchestrator reports from an agent using the
// ack-on-next-drain protocol (design doc §4): instance+ack acknowledge — and
// delete agent-side — the batch received on the PREVIOUS call; the response
// carries everything still buffered for the target. 404 means the agent
// predates the reporter endpoint (sub-project 2): treated as an empty batch,
// so mixed-fleet rollout is safe.
func (c *Client) DrainReports(ctx context.Context, srv db.Server, target, instance string, ack uint64) (shared.OrchestratorReportBatch, error) {
	u := srv.URL + "/orchestrator/reports?ack=" + strconv.FormatUint(ack, 10)
	if instance != "" {
		u += "&instance=" + url.QueryEscape(instance)
	}
	if target != "" {
		u += "&target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return shared.OrchestratorReportBatch{}, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return shared.OrchestratorReportBatch{}, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return shared.OrchestratorReportBatch{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return shared.OrchestratorReportBatch{}, fmt.Errorf("agent %s reports returned %d", srv.ID, resp.StatusCode)
	}
	var out shared.OrchestratorReportBatch
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return shared.OrchestratorReportBatch{}, fmt.Errorf("agent %s reports decode: %w", srv.ID, err)
	}
	return out, nil
}
```

Add `strconv` to the imports.

2. In `hubd/internal/orchestrator/orchestrator.go`, update the `AgentAPI` interface line (line 35) to the new signature:

```go
	DrainReports(ctx context.Context, srv db.Server, target, instance string, ack uint64) (shared.OrchestratorReportBatch, error)
```

3. In `drainReports` (orchestrator.go:241-255), minimally adapt the call so hubd compiles (the FULL ack-state logic lands in Task 11):

```go
	batch, err := o.d.Agents.DrainReports(ctx, srv, p.Target, "", 0)
	if err != nil {
		log.Printf("orchestrator[%s]: reports: %v", p.Name, err)
		return
	}
	for _, r := range batch.Reports {
		o.routeReport(ctx, p, r)
	}
```

4. In `hubd/internal/orchestrator/orchestrator_test.go`, update `fakeAgents.DrainReports` (line 81) to:

```go
func (f *fakeAgents) DrainReports(_ context.Context, _ db.Server, _, instance string, ack uint64) (shared.OrchestratorReportBatch, error) {
	f.drainAcks = append(f.drainAcks, [2]any{instance, ack})
	out := f.reports
	f.reports = nil
	var cur uint64
	if len(out) > 0 {
		cur = uint64(len(out))
	}
	return shared.OrchestratorReportBatch{Instance: "test-instance", Cursor: cur, Reports: out}, nil
}
```

and add the field `drainAcks [][2]any` to the `fakeAgents` struct.

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/hubd && go test ./internal/registry/ ./internal/orchestrator/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): DrainReports speaks the ack-on-next-drain protocol"
```

---

### Task 11: orchestrator — ack state + KillSession in AgentAPI

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (AgentAPI + struct + New + drainReports + pending comment)
- Modify: `hubd/internal/orchestrator/orchestrator_test.go` (fakeAgents KillSession; new ack test)

**Interfaces:**
- Consumes: Task 10's client signature; `registry.Client.KillSession` (exists at `hubd/internal/registry/client.go:169` — the concrete type already satisfies the widened interface).
- Produces: `AgentAPI.KillSession`, `drainAck`, `Orchestrator.ackState` — Task 12 consumes `KillSession`; `fakeAgents.killed []string` + `killErr error` for tests.

- [ ] **Step 1: Write the failing ack test**

Append to `hubd/internal/orchestrator/orchestrator_test.go` (uses the `newTestOrch` harness at line 110, which seeds project `p1` repo `o/r` on server `h1`):

```go
func TestDrainAcksPreviousBatchOnNextPoll(t *testing.T) {
	ag := &fakeAgents{reports: []shared.OrchestratorReport{
		{Repo: "o/r", Epic: 999, Stage: shared.EpicPlanning, Session: "s", Ts: "t"}}}
	o, d := newTestOrch(t, &fakeGH{}, ag, fakeLive{})
	ctx := context.Background()
	p, err := d.GetProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	o.drainReports(ctx, p) // batch of 1 (epic unknown → dropped) — cursor 1 remembered
	o.drainReports(ctx, p) // must echo instance+cursor as the ack
	if len(ag.drainAcks) != 2 {
		t.Fatalf("drains = %d", len(ag.drainAcks))
	}
	if ag.drainAcks[0] != [2]any{"", uint64(0)} {
		t.Fatalf("first drain must ack nothing: %+v", ag.drainAcks[0])
	}
	if ag.drainAcks[1] != [2]any{"test-instance", uint64(1)} {
		t.Fatalf("second drain must ack the first batch: %+v", ag.drainAcks[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run TestDrainAcksPreviousBatchOnNextPoll`
Expected: FAIL — second drain still acks `["", 0]` (Task 10 hardcoded them).

- [ ] **Step 3: Implement**

In `hubd/internal/orchestrator/orchestrator.go`:

1. Add `KillSession` to `AgentAPI` (after DrainReports):

```go
	KillSession(ctx context.Context, srv db.Server, target, name string) error
```

2. Below the interface, add:

```go
// drainAck is the per-(server,target) memory of the last received batch; the
// NEXT drain echoes it as the acknowledgment (design doc §4). In-memory only:
// a hub restart forgets it → ack=0 → the agent redelivers everything unacked →
// guarded transitions reject the duplicates. Guarded by tickMu.
type drainAck struct {
	Instance string
	Cursor   uint64
}
```

3. Add the field `ackState map[string]drainAck` to the `Orchestrator` struct (next to `pending`), and initialize `ackState: map[string]drainAck{}` in `New` (orchestrator.go:104-110, in the composite literal alongside `watermarks`).
4. Replace the Task-10 interim body of `drainReports` with:

```go
func (o *Orchestrator) drainReports(ctx context.Context, p db.Project) {
	srv, ok := o.server(ctx, p, "drain")
	if !ok {
		return
	}
	key := srv.ID + "\x00" + p.Target
	prev := o.ackState[key]
	batch, err := o.d.Agents.DrainReports(ctx, srv, p.Target, prev.Instance, prev.Cursor)
	if err != nil {
		log.Printf("orchestrator[%s]: reports: %v", p.Name, err)
		return
	}
	for _, r := range batch.Reports {
		o.routeReport(ctx, p, r)
	}
	// Remember what this batch delivered; the NEXT drain echoes it as the ack.
	// Storing an empty batch's zero cursor is safe: everything previously acked
	// is already deleted agent-side, and an ack of 0 deletes nothing.
	if batch.Instance != "" {
		o.ackState[key] = drainAck{Instance: batch.Instance, Cursor: batch.Cursor}
	}
}
```

5. Update the `pending` field's comment (orchestrator.go:98-103): replace the parenthetical "(in-memory only: a hub crash loses them — the peek/ack drain protocol in sub-project 2 closes that)" with "(in-memory only: reports are acked on the NEXT drain regardless, so a hub crash with entries still pending loses just those transient-DB-error stragglers — a far narrower window than the pre-ack destructive drain)".
6. In `orchestrator_test.go`, add to `fakeAgents`:

```go
	killed  []string
	killErr error
```

and the method:

```go
func (f *fakeAgents) KillSession(_ context.Context, _ db.Server, _, name string) error {
	f.killed = append(f.killed, name)
	return f.killErr
}
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): orchestrator remembers drain cursors and acks on the next poll; AgentAPI gains KillSession"
```

---

### Task 12: Cancel/Retry retire runner sessions + CHECKPOINT 2

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (killEpicSession; Cancel line ~704; Retry line ~686)
- Test: `hubd/internal/orchestrator/orchestrator_test.go` (append)

**Interfaces:**
- Consumes: `AgentAPI.KillSession` (Task 11), `registry.ErrNoSession` (`hubd/internal/registry/client.go` — import `agentmon/hubd/internal/registry`; no import cycle: registry only imports db/shared/authn-level packages).
- Produces: `killEpicSession(ctx, e, phase)` — best-effort session retirement (design doc D12).

- [ ] **Step 1: Write the failing tests**

Append to `hubd/internal/orchestrator/orchestrator_test.go`:

```go
// spawnEpic16 boots one epic through Tick so it holds a session assignment
// (mirrors TestTickSyncsAndSpawns' setup).
func spawnEpic16(t *testing.T, ag *fakeAgents) (*Orchestrator, *db.DB, db.Epic) {
	t.Helper()
	gh := &fakeGH{issues: map[int]github.Issue{
		16: {Number: 16, Title: "Epic", State: "open", Labels: []string{"agentmon:epic"}},
	}}
	o, d := newTestOrch(t, gh, ag, fakeLive{alive: map[string]bool{}})
	o.Tick(context.Background())
	e, err := d.GetEpicByIssue(context.Background(), "p1", 16)
	if err != nil || e.SessionName == "" {
		t.Fatalf("epic not spawned: %+v err=%v", e, err)
	}
	return o, d, e
}

func TestCancelKillsRunnerSession(t *testing.T) {
	ag := &fakeAgents{}
	o, _, e := spawnEpic16(t, ag)
	if err := o.Cancel(context.Background(), e.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if len(ag.killed) != 1 || ag.killed[0] != e.SessionName {
		t.Fatalf("killed = %v, want [%s]", ag.killed, e.SessionName)
	}
}

func TestRetryKillsPredecessorSession(t *testing.T) {
	ag := &fakeAgents{}
	o, d, e := spawnEpic16(t, ag)
	ctx := context.Background()
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "stalled", "hub", "test", "2026-07-10T14:01:00Z"); err != nil || !ok {
		t.Fatalf("force stall: ok=%v err=%v", ok, err)
	}
	if err := o.Retry(ctx, e.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if len(ag.killed) != 1 || ag.killed[0] != e.SessionName {
		t.Fatalf("killed = %v, want [%s]", ag.killed, e.SessionName)
	}
}

func TestKillFailureDoesNotBlockRetry(t *testing.T) {
	ag := &fakeAgents{killErr: errors.New("agent unreachable")}
	o, d, e := spawnEpic16(t, ag)
	ctx := context.Background()
	if ok, err := d.TransitionEpic(ctx, e.ID, "starting", "stalled", "hub", "test", "2026-07-10T14:01:00Z"); err != nil || !ok {
		t.Fatalf("force stall: ok=%v err=%v", ok, err)
	}
	if err := o.Retry(ctx, e.ID, "user"); err != nil {
		t.Fatalf("retry must be best-effort about the kill: %v", err)
	}
	got, _ := d.GetEpic(ctx, e.ID)
	if got.Stage != "queued" {
		t.Fatalf("stage = %s, want queued", got.Stage)
	}
}
```

(Add `errors` to the test imports if missing. If `ValidTransition(starting, canceled)` or `(starting, stalled)` is not permitted by the shipped state machine, STOP per rule 7 and report — do not invent a different path.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/ -run 'TestCancelKills|TestRetryKills|TestKillFailure'`
Expected: FAIL — `ag.killed` is empty (nothing calls KillSession yet).

- [ ] **Step 3: Implement**

In `hubd/internal/orchestrator/orchestrator.go`:

1. Add the import `"agentmon/hubd/internal/registry"`.
2. Add near Cancel/Retry:

```go
// killEpicSession best-effort retires an epic's runner session (design doc
// D12: Cancel and Retry retire; a mere stall never kills — the human decides,
// and Retry IS that decision). ErrNoSession is success: the session already
// ended, e.g. the normal exit-after-pr_open path. Any other failure is logged
// and swallowed — the state transition already happened and must not be
// blocked by an unreachable agent.
func (o *Orchestrator) killEpicSession(ctx context.Context, e db.Epic, phase string) {
	if e.SessionName == "" {
		return
	}
	p, err := o.d.DB.GetProject(ctx, e.ProjectID)
	if err != nil {
		log.Printf("orchestrator: %s kill: project %s: %v", phase, e.ProjectID, err)
		return
	}
	srv, ok := o.server(ctx, p, phase+"-kill")
	if !ok {
		return
	}
	if err := o.d.Agents.KillSession(ctx, srv, p.Target, e.SessionName); err != nil && !errors.Is(err, registry.ErrNoSession) {
		log.Printf("orchestrator[%s]: %s kill session %q: %v", p.Name, phase, e.SessionName, err)
	}
}
```

3. In `Cancel` (line ~704), after the successful transition (inside the happy path, before `return nil`):

```go
	o.killEpicSession(ctx, e, "cancel")
```

4. In `Retry` (line ~686), after the successful transition to queued (before `o.Wake()`):

```go
	o.killEpicSession(ctx, e, "retry")
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/hubd && go test ./internal/orchestrator/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): Cancel/Retry retire the epic's runner session (best-effort KillSession)"
```

- [ ] **Step 6: CHECKPOINT 2 — STOP**

Report: tasks 7–12 committed, full gate green. WAIT for explicit fix instructions or an explicit "continue". Do NOT begin Task 13.

---

### Task 13: `agentmon report` CLI subcommand

**Files:**
- Create: `agent/cmd/agentmon-agent/report_cli.go`
- Test: `agent/cmd/agentmon-agent/report_cli_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go:38-51` (subcommand switch)

**Interfaces:**
- Consumes: `config.Load`, `shared.ReportableStage`, the intake wire shape (Task 4).
- Produces: `reportMain(args, stdout) error`; helpers `postReport(cfgPath string, payload map[string]any, dryRun bool) (string, error)`, `repoFromGit(dir string) (string, error)`, `normalizeRepoURL(u string) (string, error)` — Task 16 reuses `postReport` and `repoFromGit`.

- [ ] **Step 1: Write the failing tests**

Create `agent/cmd/agentmon-agent/report_cli_test.go`:

```go
package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reportTestServer returns an httptest server and an agent.toml whose listen
// port points at it (mirrors the hook-test pattern: the CLI derives the intake
// URL from the config's listen port).
func reportTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	cfg := fmt.Sprintf("listen = \"127.0.0.1:%s\"\nserver_id = \"t\"\nhook_token = \"htok\"\n", port)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv, cfgPath
}

func TestReportPostsToIntake(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,42,0")
	var gotAuth, gotPane, gotTmux, gotBody, gotPath string
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPane = r.Header.Get("X-AgentMon-Pane")
		gotTmux = r.Header.Get("X-AgentMon-Tmux")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session":"epic-p-7"}`))
	})
	var out bytes.Buffer
	err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "pr_open", "--pr", "12", "--repo", "o/r", "--note", "done"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/orchestrator/report" || gotAuth != "Bearer htok" || gotPane != "%3" || gotTmux != "/tmp/tmux-0/agentmon,42,0" {
		t.Fatalf("path=%q auth=%q pane=%q tmux=%q", gotPath, gotAuth, gotPane, gotTmux)
	}
	for _, want := range []string{`"epic":7`, `"stage":"pr_open"`, `"pr":12`, `"repo":"o/r"`, `"note":"done"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body missing %s: %s", want, gotBody)
		}
	}
}

func TestReportValidation(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	var out bytes.Buffer
	if err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "merged", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("hub-derived stage must be rejected client-side")
	}
	if err := reportMain([]string{"--config", cfgPath, "--stage", "planning", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("missing --epic must error")
	}
}

func TestReportRejectionSurfacesBody(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,42,0")
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"stage is not runner-reportable"}`))
	})
	var out bytes.Buffer
	err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "planning", "--repo", "o/r"}, &out)
	if err == nil || !strings.Contains(err.Error(), "runner-reportable") {
		t.Fatalf("err = %v", err)
	}
}

func TestReportOutsideTmuxFailsFast(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	var out bytes.Buffer
	if err := reportMain([]string{"--config", cfgPath, "--epic", "1", "--stage", "planning", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("must fail outside tmux")
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/name.git":     "owner/name",
		"https://github.com/owner/name.git": "owner/name",
		"https://github.com/owner/name":     "owner/name",
		"ssh://git@github.com/owner/name":   "owner/name",
	}
	for in, want := range cases {
		got, err := normalizeRepoURL(in)
		if err != nil || got != want {
			t.Fatalf("%s → %q err=%v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/srv/git/x", "https://github.com/onlyowner", ""} {
		if _, err := normalizeRepoURL(bad); err == nil {
			t.Fatalf("%q must error", bad)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/ -run 'TestReport|TestNormalize'`
Expected: FAIL to build — `undefined: reportMain`.

- [ ] **Step 3: Implement**

Create `agent/cmd/agentmon-agent/report_cli.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"agentmon/agent/internal/config"
	"agentmon/shared"
)

// reportMain runs `agentmon report --epic N --stage S [--note …] [--pr N]
// [--repo owner/name]` — the runner contract's stage-report verb (design doc
// §7). It POSTs to the local agent's loopback intake; the agent stamps the
// session name server-side, so no session flag exists here by design.
func reportMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	epic := fs.Int("epic", 0, "epic issue number (required)")
	stage := fs.String("stage", "", "planning|implementing|reviewing|pr_open|escalated (required)")
	note := fs.String("note", "", "optional note (escalation reason, checkpoint summary)")
	pr := fs.Int("pr", 0, "PR number (use with --stage pr_open)")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *epic <= 0 {
		return fmt.Errorf("--epic is required (positive issue number)")
	}
	if !shared.ReportableStage(shared.EpicStage(*stage)) {
		return fmt.Errorf("--stage must be one of: planning, implementing, reviewing, pr_open, escalated")
	}
	r := *repo
	if r == "" {
		var err error
		if r, err = repoFromGit("."); err != nil {
			return err
		}
	}
	body, err := postReport(*cfgPath, map[string]any{
		"repo": r, "epic": *epic, "stage": *stage, "note": *note, "pr": *pr,
	}, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "reported epic %d stage %s (%s)\n", *epic, *stage, strings.TrimSpace(body))
	return nil
}

// postReport delivers one payload to the local intake (dryRun → ?dry_run=1,
// validate-only). Returns the response body. Shared with the doctor.
func postReport(cfgPath string, payload map[string]any, dryRun bool) (string, error) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", fmt.Errorf("agentmon report must run inside a tmux pane ($TMUX_PANE is empty)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if cfg.HookToken == "" {
		return "", fmt.Errorf("hook_token not configured; the report intake is disabled")
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := "http://127.0.0.1:" + port + "/orchestrator/report"
	if dryRun {
		u += "?dry_run=1"
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentMon-Pane", pane)
	req.Header.Set("X-AgentMon-Tmux", os.Getenv("TMUX"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post report: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("report rejected: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return string(respBody), nil
}

// repoFromGit derives owner/name from dir's git remote origin.
func repoFromGit(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", fmt.Errorf("cannot read git remote origin (pass --repo owner/name): %w", err)
	}
	return normalizeRepoURL(strings.TrimSpace(string(out)))
}

// normalizeRepoURL reduces a git remote URL to "owner/name". Handles
// git@host:owner/name(.git), https://host/owner/name(.git), ssh://git@host/owner/name.
func normalizeRepoURL(u string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if i := strings.Index(s, "://"); i >= 0 { // URL form: strip scheme, then host
		s = s[i+3:]
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	} else if i := strings.Index(s, ":"); i >= 0 { // scp-like git@host:owner/name
		s = s[i+1:]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot derive owner/name from remote %q — pass --repo owner/name", u)
	}
	return parts[0] + "/" + parts[1], nil
}
```

In `agent/cmd/agentmon-agent/main.go`, add to the subcommand switch (after the `hook-test` case):

```go
		case "report":
			if err := reportMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): agentmon report subcommand — posts runner stage reports to the loopback intake"
```

---

### Task 14: `epicfile` — parse + stamp epic files

**Files:**
- Create: `agent/internal/epicfile/epicfile.go`
- Test: `agent/internal/epicfile/epicfile_test.go`

**Interfaces:**
- Consumes: stdlib only.
- Produces: `epicfile.Epic`, `Parse(path) (Epic, error)`, `StampIssue(path, n) error` — Task 15's file layer.

- [ ] **Step 1: Write the failing tests**

Create `agent/internal/epicfile/epicfile_test.go`:

```go
package epicfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sample = `---
title: Mobile session keep-alive
labels: agentmon:epic, pipeline:light
blocked-by: epic-01, #12
---
## Scope
Keep panes mounted.

Acceptance: no flash on switch.
`

func TestParse(t *testing.T) {
	e, err := Parse(write(t, "epic-02-keepalive.md", sample))
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Mobile session keep-alive" || e.Issue != 0 {
		t.Fatalf("epic = %+v", e)
	}
	if !reflect.DeepEqual(e.Labels, []string{"agentmon:epic", "pipeline:light"}) {
		t.Fatalf("labels = %v", e.Labels)
	}
	if !reflect.DeepEqual(e.BlockedBy, []string{"epic-01", "#12"}) {
		t.Fatalf("blocked-by = %v", e.BlockedBy)
	}
	if !strings.HasPrefix(e.Body, "## Scope") || !strings.Contains(e.Body, "no flash") {
		t.Fatalf("body = %q", e.Body)
	}
}

func TestParseBracketsTolerated(t *testing.T) {
	e, err := Parse(write(t, "epic-01-x.md", "---\ntitle: T\nlabels: [a, b]\n---\nbody"))
	if err != nil || !reflect.DeepEqual(e.Labels, []string{"a", "b"}) {
		t.Fatalf("labels = %v err=%v", e.Labels, err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no front-matter": "just text",
		"unknown key":     "---\ntitle: T\nassignee: bob\n---\n",
		"no title":        "---\nlabels: a\n---\n",
		"unclosed":        "---\ntitle: T\n",
		"bad issue":       "---\ntitle: T\nissue: soon\n---\n",
	}
	for name, content := range cases {
		if _, err := Parse(write(t, "epic-09-e.md", content)); err == nil {
			t.Fatalf("%s: must error", name)
		}
	}
}

func TestStampIssueInsertsAndReplaces(t *testing.T) {
	p := write(t, "epic-03-s.md", sample)
	if err := StampIssue(p, 41); err != nil {
		t.Fatal(err)
	}
	e, err := Parse(p)
	if err != nil || e.Issue != 41 {
		t.Fatalf("issue = %d err=%v", e.Issue, err)
	}
	if e.Title != "Mobile session keep-alive" || !strings.Contains(e.Body, "## Scope") {
		t.Fatalf("stamp corrupted the file: %+v", e)
	}
	if err := StampIssue(p, 42); err != nil { // replace, not duplicate
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Count(string(raw), "issue:") != 1 {
		t.Fatalf("duplicate issue lines:\n%s", raw)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/epicfile/`
Expected: FAIL to build — package does not exist.

- [ ] **Step 3: Implement**

Create `agent/internal/epicfile/epicfile.go`:

```go
// Package epicfile parses the docs/plan/epic-NN-<slug>.md files emitted by
// the plan-epics skill and stamps created issue numbers back into them: the
// file is the epic's birth certificate, and a stamped file is skipped on
// re-import (design doc §10). The front-matter is a deliberately strict
// key: value format — NOT YAML — so a typo'd dial fails the import instead of
// silently dropping.
package epicfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Epic struct {
	Path      string
	Title     string
	Labels    []string
	BlockedBy []string // raw refs: "#12", "12", or a sibling file ref "epic-01"
	Issue     int      // 0 until stamped
	Body      string
}

// Parse reads one epic file. Contract:
//
//	---
//	title: Session keep-alive          (required)
//	labels: agentmon:epic, plan-gate   (optional; commas; [ ] tolerated)
//	blocked-by: epic-01, #12           (optional; commas; [ ] tolerated)
//	issue: 42                          (absent until stamped by import)
//	---
//	<markdown body: scope, acceptance criteria, constraints, decisions>
func Parse(path string) (Epic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Epic{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Epic{}, fmt.Errorf("%s: missing front-matter open '---'", path)
	}
	e := Epic{Path: path}
	i := 1
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			break
		}
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return Epic{}, fmt.Errorf("%s: bad front-matter line %q", path, line)
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "title":
			e.Title = val
		case "labels":
			e.Labels = splitList(val)
		case "blocked-by":
			e.BlockedBy = splitList(val)
		case "issue":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return Epic{}, fmt.Errorf("%s: bad issue number %q", path, val)
			}
			e.Issue = n
		default:
			return Epic{}, fmt.Errorf("%s: unknown front-matter key %q", path, strings.TrimSpace(key))
		}
	}
	if i == len(lines) {
		return Epic{}, fmt.Errorf("%s: missing front-matter close '---'", path)
	}
	if e.Title == "" {
		return Epic{}, fmt.Errorf("%s: title is required", path)
	}
	e.Body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
	return e, nil
}

func splitList(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// StampIssue rewrites the file with `issue: n` in the front-matter, replacing
// an existing issue line or inserting one before the closing ---.
func StampIssue(path string, n int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	stamp := fmt.Sprintf("issue: %d", n)
	replaced := false
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "issue:") {
			lines[i] = stamp
			replaced = true
			continue
		}
		if trimmed == "---" {
			if !replaced {
				lines = append(lines[:i], append([]string{stamp}, lines[i:]...)...)
			}
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("%s: missing front-matter close '---'", path)
}
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/epicfile/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): epicfile — strict epic front-matter parser with issue stamp-back"
```

---

### Task 15: `agentmon import-epics` subcommand

**Files:**
- Create: `agent/cmd/agentmon-agent/import_epics_cli.go`
- Test: `agent/cmd/agentmon-agent/import_epics_cli_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go` (switch case)

**Interfaces:**
- Consumes: `epicfile` (Task 14), `repoFromGit` (Task 13).
- Produces: `cmdRunner`/`execRunner` (Task 16 reuses), `importEpicsMain`, `importEpics`, `resolveRef`.
- Emits issue bodies whose dependency lines match the hub's parser: `Blocked-by: #a, #b` (`hubd/internal/orchestrator/sync.go:16` regex).

- [ ] **Step 1: Write the failing tests**

Create `agent/cmd/agentmon-agent/import_epics_cli_test.go`:

```go
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/agent/internal/epicfile"
)

type ghCall struct {
	name string
	args []string
}

// fakeGH returns URLs for successive `gh issue create` calls and records everything.
func fakeGH(t *testing.T, calls *[]ghCall, issueNums ...int) cmdRunner {
	t.Helper()
	i := 0
	return func(_ string, name string, args ...string) (string, error) {
		*calls = append(*calls, ghCall{name, args})
		if name == "gh" && len(args) > 1 && args[0] == "issue" && args[1] == "create" {
			if i >= len(issueNums) {
				t.Fatal("more creates than expected")
			}
			n := issueNums[i]
			i++
			return fmt.Sprintf("https://github.com/o/r/issues/%d\n", n), nil
		}
		return "", nil
	}
}

func writeEpics(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"epic-01-auth.md":  "---\ntitle: Auth\nlabels: agentmon:epic\n---\nAuth body",
		"epic-02-model.md": "---\ntitle: Model\nlabels: agentmon:epic\nblocked-by: epic-01\n---\nModel body",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportCreatesStampsAndLinks(t *testing.T) {
	dir := writeEpics(t)
	var calls []ghCall
	var out bytes.Buffer
	err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls, 41, 42))
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := epicfile.Parse(filepath.Join(dir, "epic-01-auth.md"))
	e2, _ := epicfile.Parse(filepath.Join(dir, "epic-02-model.md"))
	if e1.Issue != 41 || e2.Issue != 42 {
		t.Fatalf("stamped issues: %d %d", e1.Issue, e2.Issue)
	}
	var sawEdit bool
	for _, c := range calls {
		if c.args[0] == "issue" && c.args[1] == "edit" {
			sawEdit = true
			joined := strings.Join(c.args, " ")
			if !strings.Contains(joined, "42") || !strings.Contains(joined, "Blocked-by: #41") {
				t.Fatalf("edit call wrong: %v", c.args)
			}
		}
	}
	if !sawEdit {
		t.Fatal("no gh issue edit for the blocked-by pass")
	}
}

func TestImportSkipsStampedFiles(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: Done\nissue: 7\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-done.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls)); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.args[1] == "create" {
			t.Fatal("stamped file must not be re-created")
		}
	}
}

func TestImportDryRunCallsNothing(t *testing.T) {
	dir := writeEpics(t)
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r", "--dry-run"}, &out, fakeGH(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run must not call gh: %v", calls)
	}
}

func TestImportUnresolvableRefErrors(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntitle: X\nblocked-by: epic-99\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "epic-01-x.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	var out bytes.Buffer
	if err := importEpics([]string{"--dir", dir, "--repo", "o/r"}, &out, fakeGH(t, &calls, 50)); err == nil {
		t.Fatal("unresolvable blocked-by ref must error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/ -run TestImport`
Expected: FAIL to build — `undefined: importEpics`, `undefined: cmdRunner`.

- [ ] **Step 3: Implement**

Create `agent/cmd/agentmon-agent/import_epics_cli.go`:

```go
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentmon/agent/internal/epicfile"
)

// cmdRunner runs one external command in dir and returns its stdout — the DI
// seam that keeps the gh/git flows testable. Shared with the doctor (Task 16).
type cmdRunner func(dir, name string, args ...string) (string, error)

func execRunner(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(ee.Stderr))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func importEpicsMain(args []string, stdout io.Writer) error {
	return importEpics(args, stdout, execRunner)
}

var issueURLRe = regexp.MustCompile(`/issues/(\d+)\s*$`)

// importEpics turns docs/plan/epic-*.md files into GitHub issues (design doc
// §10). Idempotent: files already stamped with `issue: N` are skipped, and the
// blocked-by pass recomputes each body from the FILE, so re-runs converge
// instead of accumulating. Two phases because refs may point forward: create
// everything first, then rewrite Blocked-by lines once all numbers exist.
func importEpics(args []string, stdout io.Writer, run cmdRunner) error {
	fs := flag.NewFlagSet("import-epics", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dir := fs.String("dir", "docs/plan", "directory holding epic-*.md files")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	dryRun := fs.Bool("dry-run", false, "print planned gh calls without making them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r := *repo
	if r == "" {
		var err error
		if r, err = repoFromGit("."); err != nil {
			return err
		}
	}
	paths, err := filepath.Glob(filepath.Join(*dir, "epic-*.md"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no epic-*.md files in %s", *dir)
	}
	sort.Strings(paths)
	epics := make([]*epicfile.Epic, 0, len(paths))
	for _, p := range paths {
		e, err := epicfile.Parse(p)
		if err != nil {
			return err
		}
		epics = append(epics, &e)
	}
	// Phase 1: create + stamp.
	for _, e := range epics {
		if e.Issue != 0 {
			fmt.Fprintf(stdout, "= %s already imported as #%d\n", filepath.Base(e.Path), e.Issue)
			continue
		}
		ghArgs := []string{"issue", "create", "--repo", r, "--title", e.Title, "--body", e.Body}
		for _, l := range e.Labels {
			ghArgs = append(ghArgs, "--label", l)
		}
		if *dryRun {
			fmt.Fprintf(stdout, "[dry-run] gh %s\n", strings.Join(ghArgs, " "))
			continue
		}
		out, err := run(".", "gh", ghArgs...)
		if err != nil {
			return fmt.Errorf("create %s: %w", e.Path, err)
		}
		m := issueURLRe.FindStringSubmatch(strings.TrimSpace(out))
		if m == nil {
			return fmt.Errorf("create %s: cannot parse issue number from gh output %q", e.Path, out)
		}
		e.Issue, _ = strconv.Atoi(m[1])
		if err := epicfile.StampIssue(e.Path, e.Issue); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "+ %s → #%d\n", filepath.Base(e.Path), e.Issue)
	}
	// Phase 2: resolve blocked-by refs and write the dependency lines the hub
	// parses (ParseBlockedBy: "Blocked-by: #a, #b").
	for _, e := range epics {
		if len(e.BlockedBy) == 0 || e.Issue == 0 {
			continue
		}
		nums := make([]string, 0, len(e.BlockedBy))
		for _, ref := range e.BlockedBy {
			n, err := resolveRef(ref, epics)
			if err != nil {
				return fmt.Errorf("%s: %w", e.Path, err)
			}
			nums = append(nums, "#"+strconv.Itoa(n))
		}
		body := e.Body + "\n\nBlocked-by: " + strings.Join(nums, ", ")
		if *dryRun {
			fmt.Fprintf(stdout, "[dry-run] gh issue edit %d --body … (Blocked-by: %s)\n", e.Issue, strings.Join(nums, ", "))
			continue
		}
		if _, err := run(".", "gh", "issue", "edit", strconv.Itoa(e.Issue), "--repo", r, "--body", body); err != nil {
			return fmt.Errorf("edit #%d: %w", e.Issue, err)
		}
		fmt.Fprintf(stdout, "~ #%d Blocked-by: %s\n", e.Issue, strings.Join(nums, ", "))
	}
	return nil
}

// resolveRef maps a blocked-by ref to an issue number: "#12"/"12" directly;
// "epic-01" by unique basename-prefix match against the sibling files.
func resolveRef(ref string, epics []*epicfile.Epic) (int, error) {
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil && n > 0 {
		return n, nil
	}
	var match *epicfile.Epic
	for _, e := range epics {
		base := strings.TrimSuffix(filepath.Base(e.Path), ".md")
		if base == ref || strings.HasPrefix(base, ref+"-") {
			if match != nil {
				return 0, fmt.Errorf("blocked-by ref %q is ambiguous", ref)
			}
			match = e
		}
	}
	if match == nil {
		return 0, fmt.Errorf("blocked-by ref %q matches no epic file", ref)
	}
	if match.Issue == 0 {
		return 0, fmt.Errorf("blocked-by ref %q resolves to unstamped %s", ref, filepath.Base(match.Path))
	}
	return match.Issue, nil
}
```

In `main.go`, add the switch case:

```go
		case "import-epics":
			if err := importEpicsMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): agentmon import-epics — idempotent epic-file → GitHub issue import with stamp-back"
```

---

### Task 16: `agentmon doctor` subcommand + CHECKPOINT 3

**Files:**
- Create: `agent/cmd/agentmon-agent/doctor_cli.go`
- Test: `agent/cmd/agentmon-agent/doctor_cli_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go` (switch case)

**Interfaces:**
- Consumes: `cmdRunner`/`execRunner` (Task 15), `postReport`/`repoFromGit` (Task 13), `github.com/BurntSushi/toml` (existing agent dep).
- Produces: `doctorRun(args, stdout, run, look, home) error` — spec §5/§12 host validation.

- [ ] **Step 1: Write the failing tests**

Create `agent/cmd/agentmon-agent/doctor_cli_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorEnv fakes every doctor dependency: run succeeds for the listed command
// prefixes, look finds the listed binaries, home is a temp dir.
func doctorEnv(t *testing.T, bins []string, failPrefixes ...string) (cmdRunner, func(string) (string, error), func() (string, error), string) {
	t.Helper()
	run := func(_ string, name string, args ...string) (string, error) {
		full := name + " " + strings.Join(args, " ")
		for _, p := range failPrefixes {
			if strings.HasPrefix(full, p) {
				return "", errors.New("boom: " + full)
			}
		}
		return "ok", nil
	}
	look := func(bin string) (string, error) {
		for _, b := range bins {
			if b == bin {
				return "/usr/bin/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
	h := t.TempDir()
	return run, look, func() (string, error) { return h, nil }, h
}

// seedSkills writes the files the doctor expects for the given providers.
func seedSkills(t *testing.T, home string, claude, codex bool) {
	t.Helper()
	if claude {
		p := filepath.Join(home, ".claude", "commands")
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "epic-pipeline.md"), []byte("skill"), 0o644)
	}
	if codex {
		p := filepath.Join(home, ".codex", "prompts")
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "epic-pipeline.md"), []byte("playbook"), 0o644)
		_ = os.WriteFile(filepath.Join(home, ".codex", "config.toml"),
			[]byte("[sandbox_workspace_write]\nwritable_roots = [\""+filepath.Join(mustGetwd(t), ".git")+"\"]\nnetwork_access = true\n"), 0o644)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func doctorReporterOK(t *testing.T) string {
	t.Helper()
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,1,0")
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dry_run") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session":"doctor"}`))
	})
	return cfgPath
}

func TestDoctorAllGreen(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"claude"})
	seedSkills(t, h, true, false)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err != nil {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
	for _, want := range []string{"✓ gh auth", "✓ git fetch origin main", "✓ reporter dry-run", "✓ claude epic-pipeline skill", "– codex binary"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestDoctorFailsOnBrokenCheck(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"claude"}, "git fetch")
	seedSkills(t, h, true, false)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ git fetch origin main") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}

func TestDoctorNoProvidersFails(t *testing.T) {
	run, look, home, _ := doctorEnv(t, nil)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	if err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home); err == nil {
		t.Fatal("no provider binaries must fail the doctor")
	}
}

func TestDoctorCodexConfigChecked(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// Break the config: network_access missing.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("[sandbox_workspace_write]\nwritable_roots = []\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex sandbox config") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/ -run TestDoctor`
Expected: FAIL to build — `undefined: doctorRun`.

- [ ] **Step 3: Implement**

Create `agent/cmd/agentmon-agent/doctor_cli.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// doctorMain runs `agentmon doctor [--base main] [--repo o/r] [--config p]` in
// a project workdir: validates the spec-§12 host prerequisites (gh auth, repo
// access, base-branch fetch, reporter connectivity, provider binaries, skills,
// Codex sandbox config). Run it inside a monitored tmux session — the reporter
// probe needs a resolvable pane. Hub-dispatched doctors + board display are
// sub-project 3; this is the tool they will call.
func doctorMain(args []string, stdout io.Writer) error {
	return doctorRun(args, stdout, execRunner, exec.LookPath, os.UserHomeDir)
}

type doctorCheck struct {
	name string
	err  error
	skip string
}

func doctorRun(args []string, stdout io.Writer, run cmdRunner, look func(string) (string, error), home func() (string, error)) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	base := fs.String("base", "main", "base branch to verify fetching")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var checks []doctorCheck
	add := func(name string, err error) { checks = append(checks, doctorCheck{name: name, err: err}) }
	skip := func(name, why string) { checks = append(checks, doctorCheck{name: name, skip: why}) }

	r := *repo
	if r == "" {
		var err error
		r, err = repoFromGit(".")
		add("repo derivation (cwd is a clone)", err)
	}
	_, err := run(".", "gh", "auth", "status")
	add("gh auth", err)
	if r != "" {
		_, err = run(".", "gh", "repo", "view", r, "--json", "viewerPermission")
		add("gh repo access ("+r+")", err)
	}
	_, err = run(".", "git", "fetch", "origin", *base)
	add("git fetch origin "+*base, err)

	_, err = postReport(*cfgPath, map[string]any{
		"repo": "doctor/doctor", "epic": 1, "stage": "planning", "note": "doctor dry-run",
	}, true)
	add("reporter dry-run", err)

	claudeBin, codexBin := false, false
	if _, err := look("claude"); err == nil {
		claudeBin = true
		add("claude binary", nil)
	} else {
		skip("claude binary", "not detected")
	}
	if _, err := look("codex"); err == nil {
		codexBin = true
		add("codex binary", nil)
	} else {
		skip("codex binary", "not detected")
	}
	if !claudeBin && !codexBin {
		add("provider binaries", fmt.Errorf("neither claude nor codex on PATH"))
	}
	if h, herr := home(); herr != nil {
		add("home dir", herr)
	} else {
		if claudeBin {
			add("claude epic-pipeline skill", statFile(filepath.Join(h, ".claude", "commands", "epic-pipeline.md")))
		}
		if codexBin {
			add("codex epic-pipeline prompt", statFile(filepath.Join(h, ".codex", "prompts", "epic-pipeline.md")))
			add("codex sandbox config", checkCodexConfig(filepath.Join(h, ".codex", "config.toml")))
		}
	}

	failed := 0
	for _, c := range checks {
		switch {
		case c.skip != "":
			fmt.Fprintf(stdout, "– %s: %s\n", c.name, c.skip)
		case c.err != nil:
			failed++
			fmt.Fprintf(stdout, "✗ %s: %v\n", c.name, c.err)
		default:
			fmt.Fprintf(stdout, "✓ %s\n", c.name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", failed)
	}
	fmt.Fprintln(stdout, "doctor: all checks passed")
	return nil
}

func statFile(p string) error {
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("missing %s (run: agentmon install-skills)", p)
	}
	return nil
}

// codexConfig is the subset of ~/.codex/config.toml the doctor validates
// (spec §12: without these, runner sessions cannot commit or pass test gates).
type codexConfig struct {
	SandboxWorkspaceWrite struct {
		WritableRoots []string `toml:"writable_roots"`
		NetworkAccess bool     `toml:"network_access"`
	} `toml:"sandbox_workspace_write"`
}

func checkCodexConfig(path string) error {
	var c codexConfig
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !c.SandboxWorkspaceWrite.NetworkAccess {
		return fmt.Errorf("%s: [sandbox_workspace_write] network_access must be true (httptest loopback binds)", path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// NOTE: checked against the MAIN clone's .git; a worktree's .git is a file
	// pointing into it, so covering the clone covers its worktrees.
	gitDir := filepath.Join(cwd, ".git")
	for _, root := range c.SandboxWorkspaceWrite.WritableRoots {
		if root == gitDir || root == cwd {
			return nil
		}
	}
	return fmt.Errorf("%s: writable_roots must include %s (else no branches/commits)", path, gitDir)
}
```

In `main.go`, add the switch case:

```go
		case "doctor":
			if err := doctorMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): agentmon doctor — validates gh auth, clone, reporter, providers, codex sandbox"
```

- [ ] **Step 6: CHECKPOINT 3 — STOP**

Report: tasks 13–16 committed, full gate green. WAIT for explicit fix instructions or an explicit "continue". Do NOT begin Task 17.

---

### Task 17: `runnerfiles` — embedded skills + `install-skills`

**Files:**
- Create: `agent/internal/runnerfiles/runnerfiles.go`
- Test: `agent/internal/runnerfiles/runnerfiles_test.go`
- Create: `agent/cmd/agentmon-agent/install_skills_cli.go`
- Modify: `agent/cmd/agentmon-agent/main.go` (switch case)
- **Already present on the branch (do NOT create or edit):** `agent/internal/runnerfiles/files/claude/epic-pipeline.md`, `agent/internal/runnerfiles/files/claude/plan-epics.md`, `agent/internal/runnerfiles/files/codex/epic-pipeline.md`. If any is missing, STOP per rule 7 and report — do not write placeholder content.

**Interfaces:**
- Consumes: the three authored skill files; stdlib `embed`.
- Produces: `runnerfiles.InstallSkills(home) ([]string, error)`, `installSkillsMain` — Task 18's installer target.

- [ ] **Step 1: Write the failing test**

Create `agent/internal/runnerfiles/runnerfiles_test.go`:

```go
package runnerfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSkillsWritesAllThree(t *testing.T) {
	home := t.TempDir()
	written, err := InstallSkills(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".claude", "commands", "epic-pipeline.md"),
		filepath.Join(home, ".claude", "commands", "plan-epics.md"),
		filepath.Join(home, ".codex", "prompts", "epic-pipeline.md"),
	}
	if len(written) != len(want) {
		t.Fatalf("written = %v", written)
	}
	for i, p := range want {
		if written[i] != p {
			t.Fatalf("written[%d] = %s, want %s", i, written[i], p)
		}
		disk, err := os.ReadFile(p)
		if err != nil || len(disk) == 0 {
			t.Fatalf("%s: %v (len %d)", p, err, len(disk))
		}
		embedded, err := fsys.ReadFile(installs[i].src)
		if err != nil || string(disk) != string(embedded) {
			t.Fatalf("%s does not match embedded content", p)
		}
	}
}

func TestInstallSkillsRequiresHome(t *testing.T) {
	if _, err := InstallSkills(""); err == nil {
		t.Fatal("empty home must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/agent && go test ./internal/runnerfiles/`
Expected: FAIL to build — package `runnerfiles.go` does not exist (only `files/` do).

- [ ] **Step 3: Implement**

Create `agent/internal/runnerfiles/runnerfiles.go`:

```go
// Package runnerfiles embeds the runner skills (authored in this repo,
// reviewed like code — design doc D4/D5) and installs them into a user's
// provider directories. Distribution rides the agent binary: the fleet update
// loop re-runs install.sh, which re-runs `agentmon-agent install-skills`, so a
// skill edit reaches every host with the next agent update — the workflow
// lives in versioned markdown, never in protocol.
package runnerfiles

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed files/claude/epic-pipeline.md files/claude/plan-epics.md files/codex/epic-pipeline.md
var fsys embed.FS

// installs maps each embedded skill to its destination under $HOME. Order is
// part of the contract (tests + install output).
var installs = []struct{ src, dstDir, dstName string }{
	{"files/claude/epic-pipeline.md", filepath.Join(".claude", "commands"), "epic-pipeline.md"},
	{"files/claude/plan-epics.md", filepath.Join(".claude", "commands"), "plan-epics.md"},
	{"files/codex/epic-pipeline.md", filepath.Join(".codex", "prompts"), "epic-pipeline.md"},
}

// InstallSkills writes every embedded skill under home (0755 dirs, 0644 files
// — they are prompts, not secrets) and returns the written paths.
// Unconditional for both providers: a file for an absent provider is harmless
// and becomes live the moment that provider is installed.
func InstallSkills(home string) ([]string, error) {
	if home == "" {
		return nil, fmt.Errorf("home directory is required")
	}
	var written []string
	for _, in := range installs {
		b, err := fsys.ReadFile(in.src)
		if err != nil {
			return written, fmt.Errorf("embedded %s: %w", in.src, err)
		}
		dir := filepath.Join(home, in.dstDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		dst := filepath.Join(dir, in.dstName)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return written, err
		}
		written = append(written, dst)
	}
	return written, nil
}
```

Create `agent/cmd/agentmon-agent/install_skills_cli.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"agentmon/agent/internal/runnerfiles"
)

// installSkillsMain runs `agentmon install-skills [--home DIR]` — writes the
// embedded runner skills into the user's provider dirs. The installer invokes
// it via runuser with an explicit --home (runuser does not reset $HOME).
func installSkillsMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("install-skills", flag.ContinueOnError)
	fs.SetOutput(stdout)
	home := fs.String("home", "", "home directory to install into (default: current user's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	h := *home
	if h == "" {
		var err error
		if h, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	written, err := runnerfiles.InstallSkills(h)
	for _, p := range written {
		fmt.Fprintf(stdout, "installed %s\n", p)
	}
	return err
}
```

In `main.go`, add the switch case:

```go
		case "install-skills":
			if err := installSkillsMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/agent && go test ./internal/runnerfiles/ ./cmd/agentmon-agent/` → PASS, then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add agent/ && git commit -m "feat(agent): embed runner skills in the binary + agentmon install-skills"
```

---

### Task 18: installer distributes the runner CLI + skills

**Files:**
- Modify: `hubd/internal/api/install.sh.tmpl`
- Modify: `hubd/internal/api/install_test.go` (append)

**Interfaces:**
- Consumes: `agentmon-agent install-skills --home` (Task 17).
- Produces: `/usr/local/bin/agentmon` symlink + per-user skills on every install AND update run.

- [ ] **Step 1: Write the failing test**

Append to `hubd/internal/api/install_test.go` (mirrors `TestInstallScriptDefaultsToDedicatedSocket` at line 51):

```go
func TestInstallScriptInstallsRunnerFiles(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	for _, want := range []string{
		`ln -sfn /usr/local/bin/agentmon-agent /usr/local/bin/agentmon`,
		`install-skills --home`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	// Both the update path and the fresh path must install runner files, so a
	// fleet UPDATE delivers new skills too (the whole point of binary-embedded
	// distribution). Two call sites of the same function.
	if strings.Count(body, "install_runner_files\n") < 2 {
		t.Fatal("install_runner_files must run on both the update and fresh paths")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestInstallScriptInstallsRunnerFiles`
Expected: FAIL — strings missing.

- [ ] **Step 3: Implement**

In `hubd/internal/api/install.sh.tmpl`:

1. Next to the `maybe_install_hooks` function definition (its `local home_dir…` body starts around line 152), add:

```bash
# Runner CLI + skills: the `agentmon` symlink gives runner sessions the
# report/doctor/import-epics CLI; install-skills writes the binary-embedded
# epic-pipeline / plan-epics prompts into the run user's provider dirs.
# Idempotent; runs on update too — that is how skill edits reach the fleet.
install_runner_files() {
  ln -sfn /usr/local/bin/agentmon-agent /usr/local/bin/agentmon
  local home_dir
  home_dir="$(getent passwd "$RUN_USER" | cut -d: -f6)"
  [ -n "$home_dir" ] || home_dir="${HOME:-/root}"
  if runuser -u "$RUN_USER" -- /usr/local/bin/agentmon-agent install-skills --home "$home_dir" >/dev/null; then
    echo "✓ runner skills installed for '$RUN_USER' (epic-pipeline, plan-epics)."
  else
    echo "warning: runner skill install failed — re-run: runuser -u $RUN_USER -- agentmon-agent install-skills" >&2
  fi
}
```

2. On the UPDATE path: immediately before the `maybe_install_hooks # re-run with --hooks…` line (line ~311), add a line:

```bash
install_runner_files
```

3. On the FRESH path: immediately before the final `maybe_install_hooks` (line ~373), add a line:

```bash
install_runner_files
```

4. In the dry-run block (the `case "$HOOKS_MODE" in` echo section, line ~260), add before the hooks case:

```bash
  echo "[dry-run] would install the /usr/local/bin/agentmon symlink + runner skills for '$RUN_USER'"
```

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `cd /root/agentmon/hubd && go test ./internal/api/` → PASS (including `TestInstallScriptBashSyntax`, which shellchecks the rendered script), then the full gate → PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon && git add hubd/ && git commit -m "feat(hub): installer distributes the agentmon CLI symlink + runner skills on install and update"
```

---

### Task 19: docs — config reference + README

**Files:**
- Modify: `deploy/agent.example.toml` (append)
- Modify: `README.md` (Agent config reference section, line ~249)

- [ ] **Step 1: Document the runner surfaces**

Append to `deploy/agent.example.toml`:

```toml
# hook_token (written by the installer when hooks are provisioned) also gates
# the orchestrator report intake: `agentmon report` posts runner stage reports
# to the loopback endpoint it enables.
# hook_token = "file:/etc/agentmon/hook_token"
```

In `README.md`, inside the "### Agent (`agent.toml`)" reference section (line ~249), append this block after the existing config keys (outer fence shown with four backticks only to survive THIS plan document — the README gets the content between them verbatim):

````markdown
**Runner CLI (orchestrator hosts).** The installer symlinks `agentmon` →
`agentmon-agent` and installs the runner skills (`epic-pipeline`, `plan-epics`)
into the run user's `~/.claude/commands/` and `~/.codex/prompts/`. Inside a
monitored session:

```bash
agentmon report --epic 16 --stage implementing   # runner stage reports
agentmon doctor                                  # validate a project host (gh auth, clone, reporter, providers)
agentmon import-epics --dir docs/plan            # epic files → GitHub issues (idempotent)
```
````

- [ ] **Step 2: Run the full gate**

Run the Global Constraints gate command.
Expected: PASS (docs only — the gate guards against accidental code drift).

- [ ] **Step 3: Commit**

```bash
cd /root/agentmon && git add deploy/ README.md && git commit -m "docs: runner CLI + hook_token reporter note in config reference"
```

---

## End of run

Write the implementation report to `docs/superpowers/orchestrator-runner-implementation-report.md` (mirror the structure of `docs/superpowers/orchestrator-core-implementation-report.md`: per-task status, deviations, rule-7 stops and their resolutions, test counts) and commit it as `docs: orchestrator runner implementation report`. Then STOP and report completion. The final whole-branch cross-provider review, fixes, and the merge gate are handled outside this plan.

## Out of scope for this plan

- The CONTENT of the three skill files (authored separately on this branch before Task 17; Global Constraints forbid editing them).
- Hub-dispatched doctor sessions + project-page doctor display; board New-Session-with-command UI (sub-project 3).
- Report-buffer persistence, run/attempt tokens, merged-worktree cleanup (design doc non-goals).

## Deployment note

Deploy the hub first (its DrainReports treats agent 404 as an empty batch, so a mixed fleet is safe), then agents via the fleet update loop — the installer now also delivers the `agentmon` symlink and skills on the UPDATE path. Everything stays inert until a project + `github.token` are configured; acceptance is the toy-repo ritual (register → doctor → 3-epic run).
