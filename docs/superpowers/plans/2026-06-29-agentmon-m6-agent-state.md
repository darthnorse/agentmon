# M6 — Agent-side Claude state detection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the agent a token-gated Claude Code hook intake that derives per-session state (blocked/done/working/idle/unknown) and exposes it on `GET /sessions`, plus a hook installer CLI.

**Architecture:** A pure in-memory state machine (`agent/internal/state`) maps verified hook events to states keyed per `(target, pane)`. A loopback-only, token-authenticated `POST /hook` (`agent/internal/hooks`) correlates each hook to a tmux pane using `$TMUX_PANE`/`$TMUX` (passed as headers by the installed command) and feeds the machine. `GET /sessions` rolls pane states up per session (§9.2). The agent is pull-only — the hub (M7) reads this state via the existing pull; no agent→hub push.

**Tech Stack:** Go (std `net/http` ServeMux, `encoding/json`, `crypto/subtle`); `github.com/BurntSushi/toml`; existing `agentmon/shared`, `agentmon/agent/internal/{config,tmux,api}`.

**Spec:** `docs/superpowers/specs/2026-06-29-agentmon-m6-agent-state-design.md`. **Spike evidence:** `scratchpad/hook-spike/FINDINGS.md`.

## Global Constraints

- **Hooks are opt-in:** `POST /hook` is mounted only when `cfg.HookToken != ""`.
- **Agent emits only the 5 global states** (`blocked/done/working/idle/unknown`); the per-principal `done→idle` "seen" projection is hub-side (M7), not here.
- **`/hook` never breaks Claude:** every soft failure (unknown socket, bad pane, missing `$TMUX`, malformed body) returns **`204`**; only auth → `401`, non-loopback → `403`. Body parsing tolerates extra/unknown fields and unknown event names (forward-compat, design §18-Q3).
- **Token-safe compares:** constant-time, length-safe SHA-256 compare (mirror `api.RequireBearer`).
- **Secrets:** `hook_token` resolves via `shared.ResolveSecretRef` (must be `env:`/`file:`, never bare literal) and is **optional** (empty ⇒ feature off). Never log token bytes.
- **`State` is additive JSON** on `shared.Session` — backward-compatible for the hub (M7) and web (M8), which are untouched in M6.
- **Build:** `go build ./...` and `go vet ./...` clean; agent builds with `CGO_ENABLED=0`.
- **Test:** TDD per task; run `go test ./... -race`; commit after each green task.
- **Safety (memory [[dev-host-runs-hub-and-claude]]):** no test or command may touch `~/.claude/settings.json`; installer tests use `t.TempDir()` only; the CLI has **no implicit `~/.claude` default**.

---

## File Structure

- `shared/session.go` (modify) — `State` type + consts + `RollUp`; add `State` field to `Session`.
- `shared/session_test.go` (create) — `RollUp` priority matrix.
- `agent/internal/state/state.go` (create) — `Machine`, `Event`, `Apply`, `Pane`, `Rollup`, mapping.
- `agent/internal/state/state_test.go` (create) — mapping + rollup + concurrency tests.
- `agent/internal/config/config.go` (modify) — `HookToken`, `HookTokenFile` + conditional resolve.
- `agent/internal/config/config_test.go` (modify) — hook-token resolution/optional/reject-literal.
- `agent/internal/hooks/hooks.go` (create) — `RequireHookAuth`, `HookHandler`, correlation helpers.
- `agent/internal/hooks/hooks_test.go` (create) — intake happy path + auth + soft drops + tolerance.
- `agent/internal/hooks/install.go` (create) — `Command`, `Snippet`, `Merge`, `Unmerge`, `LoadSettings`, `SaveSettings`, `WriteTokenFile`.
- `agent/internal/hooks/install_test.go` (create) — command/snippet/merge/unmerge/file round-trip.
- `agent/internal/api/sessions.go` (modify) — `SessionsHandler` takes `*state.Machine`, stamps `State`.
- `agent/internal/api/sessions_test.go` (modify) — fix call sites; add stamping tests.
- `agent/cmd/agentmon-agent/main.go` (modify) — create machine, pass to `/sessions`, mount `/hook`, subcommand dispatch.
- `agent/cmd/agentmon-agent/hooks_cli.go` (create) — `hooksMain`, `hookTestMain` orchestration.
- `agent/cmd/agentmon-agent/hooks_cli_test.go` (create) — `hooksMain` print/install/uninstall over temp files.

---

## Task 1: Shared state contract + rollup

**Files:**
- Modify: `shared/session.go`
- Test: `shared/session_test.go` (create)

**Interfaces:**
- Produces: `shared.State` (string), consts `StateBlocked/StateDone/StateWorking/StateIdle/StateUnknown`, `func RollUp(...State) State`, and `Session.State State` (`json:"state"`).

- [ ] **Step 1: Write the failing test** — create `shared/session_test.go`:

```go
package shared

import "testing"

func TestRollUpPriority(t *testing.T) {
	cases := []struct {
		name string
		in   []State
		want State
	}{
		{"empty", nil, StateUnknown},
		{"single idle", []State{StateIdle}, StateIdle},
		{"blocked beats all", []State{StateIdle, StateWorking, StateDone, StateBlocked}, StateBlocked},
		{"done beats working", []State{StateWorking, StateDone, StateIdle}, StateDone},
		{"working beats idle", []State{StateIdle, StateWorking}, StateWorking},
		{"idle beats unknown", []State{StateUnknown, StateIdle}, StateIdle},
		{"all unknown", []State{StateUnknown, StateUnknown}, StateUnknown},
		{"unrecognized is unknown", []State{"weird"}, StateUnknown},
		{"unrecognized with idle", []State{"weird", StateIdle}, StateIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RollUp(c.in...); got != c.want {
				t.Fatalf("RollUp(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./shared/ -run TestRollUpPriority`
Expected: FAIL (undefined: State / RollUp).

- [ ] **Step 3: Implement** — replace the top of `shared/session.go` (the comment + `Session` struct). New `shared/session.go`:

```go
package shared

// State is an agent session/pane state. The agent emits only these five global
// states; the per-principal done→idle "seen" projection (§9.3) is hub-side.
type State string

const (
	StateBlocked State = "blocked" // needs human input/approval — highest priority
	StateDone    State = "done"    // finished a turn (globally unseen)
	StateWorking State = "working" // actively processing / running tools
	StateIdle    State = "idle"    // calm: agent present at its prompt, not working
	StateUnknown State = "unknown" // plain shell, or no hook signal yet
)

// statePriority orders states for rollup (§9.2): blocked > done > working > idle > unknown.
var statePriority = map[State]int{
	StateBlocked: 5, StateDone: 4, StateWorking: 3, StateIdle: 2, StateUnknown: 1,
}

// RollUp reduces pane states to one session/server state using §9.2 priority.
// Empty input or any unrecognized state contributes as StateUnknown.
func RollUp(states ...State) State {
	best, bestP := StateUnknown, statePriority[StateUnknown]
	for _, s := range states {
		p, ok := statePriority[s]
		if !ok {
			continue // unrecognized → contributes as unknown (no-op)
		}
		if p > bestP {
			best, bestP = s, p
		}
	}
	return best
}

// Session is the project-identifiable unit shown in every client surface.
type Session struct {
	Name    string   `json:"name"`
	Server  string   `json:"server"`
	Target  string   `json:"target"`
	Cwd     string   `json:"cwd"`
	Command string   `json:"command"`
	State   State    `json:"state"` // rolled up from this session's panes; "unknown" if no hook seen
	Windows []Window `json:"windows"`
}
```

Leave the rest of `shared/session.go` (`Window`, `Pane`, `SessionList`) unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./shared/ -run TestRollUpPriority`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/session.go shared/session_test.go
git commit -m "feat(shared): State type + RollUp + Session.State (M6 contract)"
```

---

## Task 2: Agent state machine

**Files:**
- Create: `agent/internal/state/state.go`
- Test: `agent/internal/state/state_test.go`

**Interfaces:**
- Consumes: `shared.State`, `shared.RollUp` (Task 1).
- Produces:
  - `type Event struct { Target, Pane, Name, NotificationKind, ClaudeSessionID string; At time.Time }`
  - `func New(now func() time.Time) *Machine`
  - `func (m *Machine) Apply(ev Event) (shared.State, bool)` — returns (newState, changed)
  - `func (m *Machine) Pane(target, pane string) (shared.State, bool)`
  - `func (m *Machine) Rollup(target string, panes []string) shared.State`

- [ ] **Step 1: Write the failing test** — create `agent/internal/state/state_test.go`:

```go
package state

import (
	"testing"
	"time"

	"agentmon/shared"
)

func fixedNow() func() time.Time {
	t := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestApplyMapping(t *testing.T) {
	cases := []struct {
		name, event, kind string
		want              shared.State
	}{
		{"session start", "SessionStart", "", shared.StateIdle},
		{"prompt", "UserPromptSubmit", "", shared.StateWorking},
		{"pretool", "PreToolUse", "", shared.StateWorking},
		{"posttool", "PostToolUse", "", shared.StateWorking},
		{"permission request", "PermissionRequest", "", shared.StateBlocked},
		{"notif permission", "Notification", "permission_prompt", shared.StateBlocked},
		{"notif idle", "Notification", "idle", shared.StateDone},
		{"stop", "Stop", "", shared.StateDone},
		{"session end", "SessionEnd", "", shared.StateUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(fixedNow())
			got, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: c.event, NotificationKind: c.kind})
			if got != c.want {
				t.Fatalf("Apply(%s/%s) = %q, want %q", c.event, c.kind, got, c.want)
			}
			if c.want != shared.StateUnknown && !changed {
				t.Fatalf("first %s should report changed", c.event)
			}
		})
	}
}

func TestApplyPreservesOnSubagentStopAndUnknownEvent(t *testing.T) {
	for _, name := range []string{"SubagentStop", "TotallyNewEventV9"} {
		m := New(fixedNow())
		m.Apply(Event{Target: "default", Pane: "%0", Name: "PreToolUse"}) // working
		got, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: name})
		if got != shared.StateWorking || changed {
			t.Fatalf("%s: got %q changed=%v, want working/false", name, got, changed)
		}
	}
}

func TestApplyChangedFlag(t *testing.T) {
	m := New(fixedNow())
	if _, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"}); !changed {
		t.Fatal("first Stop should be changed")
	}
	if _, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"}); changed {
		t.Fatal("repeat Stop should not be changed")
	}
}

func TestPaneAndRollup(t *testing.T) {
	m := New(fixedNow())
	if s, ok := m.Pane("default", "%9"); ok || s != shared.StateUnknown {
		t.Fatalf("unknown pane → %q ok=%v", s, ok)
	}
	m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"})              // done
	m.Apply(Event{Target: "default", Pane: "%1", Name: "PermissionRequest"}) // blocked
	if got := m.Rollup("default", []string{"%0", "%1"}); got != shared.StateBlocked {
		t.Fatalf("rollup = %q, want blocked", got)
	}
	if got := m.Rollup("default", []string{"%0"}); got != shared.StateDone {
		t.Fatalf("rollup = %q, want done", got)
	}
	if got := m.Rollup("default", []string{"%none"}); got != shared.StateUnknown {
		t.Fatalf("rollup unknown panes = %q, want unknown", got)
	}
	if got := m.Rollup("other", []string{"%0"}); got != shared.StateUnknown {
		t.Fatalf("rollup wrong target = %q, want unknown", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/internal/state/`
Expected: FAIL (no Go files / undefined).

- [ ] **Step 3: Implement** — create `agent/internal/state/state.go`:

```go
// Package state derives a Claude Code session/pane state from hook events. It is
// pure (no tmux, no HTTP), in-memory, and safe for concurrent use.
package state

import (
	"strings"
	"sync"
	"time"

	"agentmon/shared"
)

// Event is a parsed, correlated hook signal handed to the machine.
type Event struct {
	Target           string    // resolved config.Target.Label
	Pane             string    // tmux pane id, e.g. "%3"
	Name             string    // hook_event_name
	NotificationKind string    // notification_type (Notification only; else "")
	ClaudeSessionID  string    // session_id (UUID) — informational
	At               time.Time // event time; defaults to now() when zero
}

type paneState struct {
	State           shared.State
	LastEvent       string
	ClaudeSessionID string
	UpdatedAt       time.Time
}

type key struct{ target, pane string }

// Machine holds current state per (target, pane).
type Machine struct {
	mu    sync.Mutex
	panes map[key]paneState
	now   func() time.Time
}

// New builds a Machine. now defaults to time.Now when nil.
func New(now func() time.Time) *Machine {
	if now == nil {
		now = time.Now
	}
	return &Machine{panes: map[key]paneState{}, now: now}
}

// derive maps a hook event to a state, or (_, false) to preserve the prior state.
func derive(name, notificationKind string) (shared.State, bool) {
	switch name {
	case "SessionStart":
		return shared.StateIdle, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return shared.StateWorking, true
	case "PermissionRequest":
		return shared.StateBlocked, true
	case "Notification":
		if strings.Contains(strings.ToLower(notificationKind), "permission") {
			return shared.StateBlocked, true
		}
		return shared.StateDone, true
	case "Stop":
		return shared.StateDone, true
	case "SessionEnd":
		return shared.StateUnknown, true
	default: // SubagentStop and any unknown event preserve the prior state
		return "", false
	}
}

// Apply records the event and returns the new pane state plus whether it changed.
func (m *Machine) Apply(ev Event) (shared.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key{ev.Target, ev.Pane}
	prior := m.panes[k].State
	if prior == "" {
		prior = shared.StateUnknown
	}
	next := prior
	if d, ok := derive(ev.Name, ev.NotificationKind); ok {
		next = d
	}
	at := ev.At
	if at.IsZero() {
		at = m.now()
	}
	m.panes[k] = paneState{State: next, LastEvent: ev.Name, ClaudeSessionID: ev.ClaudeSessionID, UpdatedAt: at}
	return next, next != prior
}

// Pane returns the current state for one pane (ok=false if never seen).
func (m *Machine) Pane(target, pane string) (shared.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps, ok := m.panes[key{target, pane}]
	if !ok {
		return shared.StateUnknown, false
	}
	return ps.State, true
}

// Rollup reduces the known states of the given panes to one (§9.2). Panes with no
// recorded state are excluded; no known panes → StateUnknown.
func (m *Machine) Rollup(target string, panes []string) shared.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	var states []shared.State
	for _, p := range panes {
		if ps, ok := m.panes[key{target, p}]; ok {
			states = append(states, ps.State)
		}
	}
	return shared.RollUp(states...)
}
```

- [ ] **Step 4: Run tests (with race detector)**

Run: `go test ./agent/internal/state/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/state/
git commit -m "feat(agent/state): hook-event state machine + rollup (M6)"
```

---

## Task 3: Config — hook token fields

**Files:**
- Modify: `agent/internal/config/config.go`
- Test: `agent/internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.HookToken string` (`toml:"hook_token"`), `Config.HookTokenFile string` (`toml:"hook_token_file"`). `HookToken` resolved via `shared.ResolveSecretRef` only when non-empty.

- [ ] **Step 1: Write the failing tests** — append to `agent/internal/config/config_test.go`:

```go
func TestLoadResolvesHookToken(t *testing.T) {
	t.Setenv("HK_HUB", "h")
	t.Setenv("HK_DK", "d")
	t.Setenv("AGENTMON_HOOK_TOKEN", "hooksecret")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "s"
hub_token = "env:HK_HUB"
directive_key = "env:HK_DK"
hook_token = "env:AGENTMON_HOOK_TOKEN"
hook_token_file = "/run/agentmon/hook-token"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HookToken != "hooksecret" {
		t.Fatalf("hook token = %q", cfg.HookToken)
	}
	if cfg.HookTokenFile != "/run/agentmon/hook-token" {
		t.Fatalf("hook token file = %q", cfg.HookTokenFile)
	}
}

func TestLoadHookTokenOptional(t *testing.T) {
	t.Setenv("HK_HUB2", "h")
	t.Setenv("HK_DK2", "d")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:HK_HUB2"
directive_key = "env:HK_DK2"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("optional hook_token should not error: %v", err)
	}
	if cfg.HookToken != "" {
		t.Fatalf("want empty hook token, got %q", cfg.HookToken)
	}
}

func TestLoadHookTokenBareLiteralRejected(t *testing.T) {
	t.Setenv("HK_HUB3", "h")
	t.Setenv("HK_DK3", "d")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:HK_HUB3"
directive_key = "env:HK_DK3"
hook_token = "plain-literal"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("bare-literal hook_token must be rejected")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./agent/internal/config/ -run TestLoadHookToken`
Expected: FAIL (unknown field / not resolved).

- [ ] **Step 3: Implement** — in `agent/internal/config/config.go`, add the two fields to `Config`:

```go
type Config struct {
	Listen          string   `toml:"listen"`
	ServerID        string   `toml:"server_id"`
	HubToken        string   `toml:"hub_token"`
	DirectiveKey    string   `toml:"directive_key"`
	HookToken       string   `toml:"hook_token"`      // optional; enables /hook when set
	HookTokenFile   string   `toml:"hook_token_file"` // optional path the agent writes the token to
	ScrollbackLines int      `toml:"scrollback_lines"`
	Targets         []Target `toml:"targets"`
}
```

Then, in `Load`, after the existing required-secret loop (`for _, p := range []*string{&c.HubToken, &c.DirectiveKey} { ... }`) and before `return c, nil`, add:

```go
	if c.HookToken != "" {
		v, err := shared.ResolveSecretRef(c.HookToken)
		if err != nil {
			return Config{}, err
		}
		c.HookToken = v
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./agent/internal/config/`
Expected: PASS (all, including pre-existing tests).

- [ ] **Step 5: Commit**

```bash
git add agent/internal/config/
git commit -m "feat(agent/config): optional hook_token + hook_token_file (M6)"
```

---

## Task 4: `POST /hook` intake

**Files:**
- Create: `agent/internal/hooks/hooks.go`
- Test: `agent/internal/hooks/hooks_test.go`

**Interfaces:**
- Consumes: `config.Config` (Task 3), `state.Machine`/`state.Event` (Task 2), `tmux.ValidatePaneID`.
- Produces:
  - `func RequireHookAuth(token string, next http.Handler) http.Handler`
  - `func HookHandler(cfg config.Config, m *state.Machine, now func() time.Time) http.HandlerFunc`

- [ ] **Step 1: Write the failing tests** — create `agent/internal/hooks/hooks_test.go`:

```go
package hooks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/shared"
)

func testCfg() config.Config {
	return config.Config{
		HookToken: "hooktok",
		Targets:   []config.Target{{Label: "default", SocketName: ""}, {Label: "build", SocketName: "buildsock"}},
	}
}

func post(t *testing.T, h http.Handler, remote, auth, pane, tmuxEnv, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.RemoteAddr = remote
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if pane != "" {
		req.Header.Set("X-AgentMon-Pane", pane)
	}
	if tmuxEnv != "" {
		req.Header.Set("X-AgentMon-Tmux", tmuxEnv)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func handler(m *state.Machine) http.Handler {
	return RequireHookAuth("hooktok", HookHandler(testCfg(), m, nil))
}

func TestHookValidPermissionRequest(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%3",
		"/tmp/tmux-0/default,123,0",
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash","session_id":"abc"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("code %d, want 204", rr.Code)
	}
	if s, ok := m.Pane("default", "%3"); !ok || s != shared.StateBlocked {
		t.Fatalf("state = %q ok=%v, want blocked", s, ok)
	}
}

func TestHookSocketMapsToNamedTarget(t *testing.T) {
	m := state.New(nil)
	post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%0",
		"/tmp/tmux-0/buildsock,1,0", `{"hook_event_name":"Stop"}`)
	if s, _ := m.Pane("build", "%0"); s != shared.StateDone {
		t.Fatalf("build state = %q, want done", s)
	}
}

func TestHookBadToken401(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer wrong", "%3",
		"/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code %d, want 401", rr.Code)
	}
}

func TestHookNonLoopback403(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "10.0.0.9:5000", "Bearer hooktok", "%3",
		"/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", rr.Code)
	}
}

func TestHookSoftDrops(t *testing.T) {
	m := state.New(nil)
	cases := []struct{ name, pane, tmuxEnv, body string }{
		{"unknown socket", "%3", "/tmp/tmux-0/ghost,1,0", `{"hook_event_name":"Stop"}`},
		{"missing tmux", "%3", "", `{"hook_event_name":"Stop"}`},
		{"bad pane", "bogus", "/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`},
		{"malformed json", "%3", "/tmp/tmux-0/default,1,0", `{not json`},
		{"no event name", "%3", "/tmp/tmux-0/default,1,0", `{"foo":"bar"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", c.pane, c.tmuxEnv, c.body)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("%s: code %d, want 204", c.name, rr.Code)
			}
		})
	}
	if s, ok := m.Pane("default", "%3"); ok {
		t.Fatalf("soft drops must not record state, got %q", s)
	}
}

func TestHookToleratesExtraFields(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%3", "/tmp/tmux-0/default,1,0",
		`{"hook_event_name":"UserPromptSubmit","prompt":"hi","permission_mode":"default","effort":{"level":"x"},"new_field":[1,2]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("code %d", rr.Code)
	}
	if s, _ := m.Pane("default", "%3"); s != shared.StateWorking {
		t.Fatalf("state %q, want working", s)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./agent/internal/hooks/`
Expected: FAIL (no Go files / undefined).

- [ ] **Step 3: Implement** — create `agent/internal/hooks/hooks.go`:

```go
// Package hooks implements the agent's local Claude Code hook intake.
package hooks

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
)

// RequireHookAuth gates POST /hook with the hook token, using a constant-time,
// length-safe SHA-256 compare (mirrors api.RequireBearer). token must be non-empty
// (the route is only mounted when cfg.HookToken != "").
func RequireHookAuth(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hookBody is the tolerant subset read from Claude's hook event JSON. Unknown and
// extra fields are ignored (forward-compat, design §18-Q3).
type hookBody struct {
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	SessionID        string `json:"session_id"`
}

// HookHandler applies a correlated hook to the state machine. It returns 204 on the
// happy path AND on every soft failure (unknown socket, bad pane, missing $TMUX,
// unparseable body) so a hook never breaks or stalls Claude. now defaults to
// time.Now when nil. (Token auth and loopback are handled before/here respectively.)
func HookHandler(cfg config.Config, m *state.Machine, now func() time.Time) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		pane := r.Header.Get("X-AgentMon-Pane")
		socket := socketFromTmux(r.Header.Get("X-AgentMon-Tmux"))
		target, matched := matchTarget(cfg, socket)
		if !tmux.ValidatePaneID(pane) || !matched {
			log.Printf("hook: soft drop (pane=%q socket=%q matched=%v)", pane, socket, matched)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body hookBody
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := dec.Decode(&body); err != nil || body.HookEventName == "" {
			log.Printf("hook: soft drop (bad body / no event name): %v", err)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.Apply(state.Event{
			Target:           target,
			Pane:             pane,
			Name:             body.HookEventName,
			NotificationKind: body.NotificationType,
			ClaudeSessionID:  body.SessionID,
			At:               now(),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// socketFromTmux extracts the socket name from $TMUX ("<path>,<pid>,<idx>"): the
// basename of the path before the first comma. "" when $TMUX is empty/malformed.
func socketFromTmux(tmuxEnv string) string {
	if tmuxEnv == "" {
		return ""
	}
	path := tmuxEnv
	if i := strings.IndexByte(tmuxEnv, ','); i >= 0 {
		path = tmuxEnv[:i]
	}
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// matchTarget maps a tmux socket name to a configured target label. The default
// socket is named "default" on disk but configured as SocketName "".
func matchTarget(cfg config.Config, socket string) (string, bool) {
	if socket == "" {
		return "", false
	}
	for _, t := range cfg.Targets {
		if t.SocketName == socket || (socket == "default" && t.SocketName == "") {
			return t.Label, true
		}
	}
	return "", false
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run tests (with race detector)**

Run: `go test ./agent/internal/hooks/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/hooks/hooks.go agent/internal/hooks/hooks_test.go
git commit -m "feat(agent/hooks): token+loopback-gated POST /hook intake (M6)"
```

---

## Task 5: Hook installer — snippet, merge, file I/O

**Files:**
- Create: `agent/internal/hooks/install.go`
- Test: `agent/internal/hooks/install_test.go`

**Interfaces:**
- Consumes: `config.Config` (Task 3).
- Produces:
  - `const Marker = "agentmon-hook"`
  - `func Command(cfg config.Config) (string, error)`
  - `func Snippet(cfg config.Config) (map[string]any, error)`
  - `func Merge(existing map[string]any, cfg config.Config) (map[string]any, error)`
  - `func Unmerge(existing map[string]any) map[string]any`
  - `func LoadSettings(path string) (map[string]any, error)`
  - `func SaveSettings(path string, m map[string]any) error`
  - `func WriteTokenFile(path, token string) error`

- [ ] **Step 1: Write the failing tests** — create `agent/internal/hooks/install_test.go`:

```go
package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/agent/internal/config"
)

func installCfg() config.Config {
	return config.Config{Listen: "10.0.0.5:8377", HookToken: "tok"}
}

func TestCommandPortTokenFileAndEnv(t *testing.T) {
	c := installCfg()
	c.HookTokenFile = "/run/agentmon/hook-token"
	cmd, err := Command(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"127.0.0.1:8377/hook", "$(cat /run/agentmon/hook-token)", "$TMUX_PANE", "$TMUX", Marker} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q: %s", want, cmd)
		}
	}
}

func TestCommandLiteralTokenWhenNoFile(t *testing.T) {
	cmd, _ := Command(installCfg())
	if !strings.Contains(cmd, "Bearer tok") {
		t.Fatalf("want literal token: %s", cmd)
	}
}

func TestSnippetCoversAllEvents(t *testing.T) {
	s, err := Snippet(installCfg())
	if err != nil {
		t.Fatal(err)
	}
	h := s["hooks"].(map[string]any)
	for _, e := range events {
		if _, ok := h[e]; !ok {
			t.Fatalf("snippet missing event %s", e)
		}
	}
}

func TestMergeIdempotentPreservesUserHooks(t *testing.T) {
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
		},
		"otherSetting": true,
	}
	m1, err := Merge(existing, installCfg())
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := Merge(m1, installCfg()) // second run must not duplicate
	stop := m2["hooks"].(map[string]any)["Stop"].([]any)
	user, agent := 0, 0
	for _, g := range stop {
		if isAgentmonGroup(g) {
			agent++
		} else {
			user++
		}
	}
	if user != 1 || agent != 1 {
		t.Fatalf("Stop groups user=%d agent=%d, want 1/1", user, agent)
	}
	if m2["otherSetting"] != true {
		t.Fatal("unrelated setting lost")
	}
}

func TestUnmergeRemovesOnlyOurs(t *testing.T) {
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
		},
	}
	merged, _ := Merge(existing, installCfg())
	cleaned := Unmerge(merged)
	hooks := cleaned["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 || isAgentmonGroup(stop[0]) {
		t.Fatalf("user hook should remain alone: %+v", stop)
	}
	if _, ok := hooks["PreToolUse"]; ok {
		t.Fatal("PreToolUse (ours only) should be pruned")
	}
}

func TestUnmergeEmptyDropsHooksKey(t *testing.T) {
	merged, _ := Merge(map[string]any{}, installCfg())
	cleaned := Unmerge(merged)
	if _, ok := cleaned["hooks"]; ok {
		t.Fatal("hooks key should be gone when empty")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if m, err := LoadSettings(p); err != nil || len(m) != 0 {
		t.Fatalf("missing file should load empty: %v %+v", err, m)
	}
	merged, _ := Merge(map[string]any{}, installCfg())
	if err := SaveSettings(p, merged); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back["hooks"].(map[string]any); !ok {
		t.Fatalf("round-trip lost hooks: %+v", back)
	}
}

func TestWriteTokenFilePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "hook-token")
	if err := WriteTokenFile(p, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "s3cr3t" {
		t.Fatalf("token contents = %q", b)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./agent/internal/hooks/ -run 'TestCommand|TestSnippet|TestMerge|TestUnmerge|TestSettings|TestWriteToken'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** — create `agent/internal/hooks/install.go`:

```go
package hooks

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"agentmon/agent/internal/config"
)

// Marker tags AgentMon-installed hook commands so install is idempotent and
// uninstall removes exactly our entries (and nothing the user added).
const Marker = "agentmon-hook"

// events are the Claude Code hook events AgentMon installs (verified v2.1.195).
var events = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"Notification", "PermissionRequest", "Stop", "SubagentStop", "SessionEnd",
}

// Command builds the shell command Claude runs for each hook event. It pipes the
// event JSON (stdin) to the agent and carries pane/socket correlation in headers.
// curl failures are swallowed (|| true) so a hook never fails Claude's turn.
func Command(cfg config.Config) (string, error) {
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("parse listen %q: %w", cfg.Listen, err)
	}
	tokenExpr := cfg.HookToken
	if cfg.HookTokenFile != "" {
		tokenExpr = "$(cat " + cfg.HookTokenFile + ")"
	}
	return fmt.Sprintf(
		`curl -sS -m 2 -H "Authorization: Bearer %s" `+
			`-H "X-AgentMon-Pane: $TMUX_PANE" -H "X-AgentMon-Tmux: $TMUX" `+
			`--data-binary @- http://127.0.0.1:%s/hook >/dev/null 2>&1 || true  # %s`,
		tokenExpr, port, Marker), nil
}

func group(cmd string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
}

// Snippet returns the {"hooks":{...}} settings block AgentMon installs.
func Snippet(cfg config.Config) (map[string]any, error) {
	cmd, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	h := map[string]any{}
	for _, e := range events {
		h[e] = []any{group(cmd)}
	}
	return map[string]any{"hooks": h}, nil
}

// Merge splices AgentMon's hooks into an existing settings map idempotently:
// existing AgentMon groups are removed first, so re-running never duplicates and the
// user's own hooks are untouched. Returns the same (or a fresh) map.
func Merge(existing map[string]any, cfg config.Config) (map[string]any, error) {
	cmd, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		existing = map[string]any{}
	}
	hooks, _ := existing["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, e := range events {
		arr, _ := hooks[e].([]any)
		arr = append(dropAgentmon(arr), group(cmd))
		hooks[e] = arr
	}
	existing["hooks"] = hooks
	return existing, nil
}

// Unmerge removes only AgentMon groups, pruning empty arrays and an empty hooks map.
func Unmerge(existing map[string]any) map[string]any {
	if existing == nil {
		return map[string]any{}
	}
	hooks, _ := existing["hooks"].(map[string]any)
	if hooks == nil {
		return existing
	}
	for e, v := range hooks {
		arr, _ := v.([]any)
		arr = dropAgentmon(arr)
		if len(arr) == 0 {
			delete(hooks, e)
		} else {
			hooks[e] = arr
		}
	}
	if len(hooks) == 0 {
		delete(existing, "hooks")
	} else {
		existing["hooks"] = hooks
	}
	return existing
}

func dropAgentmon(arr []any) []any {
	out := arr[:0:0] // fresh backing array (nil when arr is nil/empty)
	for _, g := range arr {
		if !isAgentmonGroup(g) {
			out = append(out, g)
		}
	}
	return out
}

func isAgentmonGroup(g any) bool {
	gm, ok := g.(map[string]any)
	if !ok {
		return false
	}
	hs, ok := gm["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, Marker) {
			return true
		}
	}
	return false
}

// LoadSettings reads a Claude Code settings JSON file. A missing or empty file
// loads as an empty map (so install can create it).
func LoadSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// SaveSettings writes a settings map as pretty JSON (0600).
func SaveSettings(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// WriteTokenFile writes token to path (0600), creating parent dirs (0700).
func WriteTokenFile(path, token string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(token), 0o600)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./agent/internal/hooks/`
Expected: PASS (Task 4 + Task 5 tests).

- [ ] **Step 5: Commit**

```bash
git add agent/internal/hooks/install.go agent/internal/hooks/install_test.go
git commit -m "feat(agent/hooks): installer snippet/merge/unmerge + settings I/O (M6)"
```

---

## Task 6: `GET /sessions` stamps rolled-up state

**Files:**
- Modify: `agent/internal/api/sessions.go`, `agent/cmd/agentmon-agent/main.go`
- Test: `agent/internal/api/sessions_test.go`

**Interfaces:**
- Consumes: `state.Machine` (Task 2).
- Produces: `func SessionsHandler(cfg config.Config, discover Discoverer, m *state.Machine) http.HandlerFunc` (signature changes — adds `m`; nil ⇒ all sessions `unknown`).

- [ ] **Step 1: Update existing tests + add new ones** — in `agent/internal/api/sessions_test.go`:

Add `"agentmon/agent/internal/state"` to imports. Change all **five** existing `SessionsHandler(testCfg(), disc)` call sites to `SessionsHandler(testCfg(), disc, nil)`. Then append:

```go
func TestSessionsHandlerStampsState(t *testing.T) {
	m := state.New(nil)
	m.Apply(state.Event{Target: "default", Pane: "%0", Name: "PermissionRequest"})
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return []shared.Session{{Name: "proj", Target: "default",
			Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc, m)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	var body shared.SessionList
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Sessions[0].State != shared.StateBlocked {
		t.Fatalf("state = %q, want blocked", body.Sessions[0].State)
	}
}

func TestSessionsHandlerNilMachineUnknown(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return []shared.Session{{Name: "p", Target: "default",
			Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	var body shared.SessionList
	json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Sessions[0].State != shared.StateUnknown {
		t.Fatalf("state = %q, want unknown", body.Sessions[0].State)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail / don't compile**

Run: `go test ./agent/internal/api/ -run TestSessionsHandler`
Expected: FAIL (signature mismatch / undefined `state`).

- [ ] **Step 3: Implement** — in `agent/internal/api/sessions.go`:

Add `"agentmon/agent/internal/state"` to the import block. Change the signature and stamp state before encoding:

```go
func SessionsHandler(cfg config.Config, discover Discoverer, m *state.Machine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		sessions, err := discover(r.Context(), tmux.DiscoverOpts{
			ServerID:    cfg.ServerID,
			TargetLabel: t.Label,
			SocketName:  t.SocketName,
		})
		if err != nil {
			log.Printf("sessions: discovery failed (target=%q): %v", t.Label, err)
			writeJSONError(w, http.StatusInternalServerError, "discovery failed")
			return
		}
		if sessions == nil {
			sessions = []shared.Session{}
		}
		stampState(m, t.Label, sessions)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.SessionList{Sessions: sessions})
	}
}

// stampState fills Session.State from the machine's per-pane states (rolled up).
// A nil machine (hooks disabled) leaves every session StateUnknown.
func stampState(m *state.Machine, target string, sessions []shared.Session) {
	for i := range sessions {
		if m == nil {
			sessions[i].State = shared.StateUnknown
			continue
		}
		var panes []string
		for _, win := range sessions[i].Windows {
			for _, p := range win.Panes {
				panes = append(panes, p.ID)
			}
		}
		sessions[i].State = m.Rollup(target, panes)
	}
}
```

- [ ] **Step 4: Fix the build — update main.go call site.** In `agent/cmd/agentmon-agent/main.go`, add `"agentmon/agent/internal/state"` to imports, create the machine, and pass it:

```go
	machine := state.New(nil)
```
(place just after `discover := func(...) {...}` block), and change the `/sessions` registration line to:
```go
	mux.Handle("GET /sessions", api.RequireBearer(cfg.HubToken, api.SessionsHandler(cfg, discover, machine)))
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./agent/internal/api/ -race && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add agent/internal/api/sessions.go agent/internal/api/sessions_test.go agent/cmd/agentmon-agent/main.go
git commit -m "feat(agent/api): GET /sessions stamps rolled-up State (M6)"
```

---

## Task 7: Wire `/hook` + the hooks CLI

**Files:**
- Modify: `agent/cmd/agentmon-agent/main.go`
- Create: `agent/cmd/agentmon-agent/hooks_cli.go`, `agent/cmd/agentmon-agent/hooks_cli_test.go`

**Interfaces:**
- Consumes: `hooks.{Snippet,Merge,Unmerge,LoadSettings,SaveSettings,WriteTokenFile,RequireHookAuth,HookHandler}` (Tasks 4–5), `config.Load`, `state.New` (Task 2).
- Produces: `func hooksMain(args []string, stdout io.Writer) error`, `func hookTestMain(args []string, stdout io.Writer) error`.

- [ ] **Step 1: Write the failing test** — create `agent/cmd/agentmon-agent/hooks_cli_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAgentConfig writes a minimal valid agent.toml (env: secret refs) and returns its path.
func writeAgentConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("M6_HUB", "h")
	t.Setenv("M6_DK", "d")
	t.Setenv("M6_HOOK", "hooktok")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "s"
hub_token = "env:M6_HUB"
directive_key = "env:M6_DK"
hook_token = "env:M6_HOOK"
[[targets]]
  socket_name = ""
  label = "default"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHooksMainPrint(t *testing.T) {
	cfg := writeAgentConfig(t)
	var out bytes.Buffer
	if err := hooksMain([]string{"print", "--config", cfg}, &out); err != nil {
		t.Fatal(err)
	}
	var snip map[string]any
	if err := json.Unmarshal(out.Bytes(), &snip); err != nil {
		t.Fatalf("print is not valid JSON: %v\n%s", err, out.String())
	}
	if _, ok := snip["hooks"].(map[string]any)["Stop"]; !ok {
		t.Fatal("print missing Stop hook")
	}
	if !strings.Contains(out.String(), "127.0.0.1:8377/hook") {
		t.Fatal("print missing endpoint")
	}
}

func TestHooksMainInstallUninstallRoundTrip(t *testing.T) {
	cfg := writeAgentConfig(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	var out bytes.Buffer
	if err := hooksMain([]string{"install", "--config", cfg, "--settings", settings}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), "agentmon-hook") {
		t.Fatalf("install did not write our marker:\n%s", b)
	}
	if err := hooksMain([]string{"uninstall", "--config", cfg, "--settings", settings}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settings)
	if strings.Contains(string(b), "agentmon-hook") {
		t.Fatalf("uninstall left our marker:\n%s", b)
	}
}

func TestHooksMainInstallRequiresSettings(t *testing.T) {
	cfg := writeAgentConfig(t)
	if err := hooksMain([]string{"install", "--config", cfg}, new(bytes.Buffer)); err == nil {
		t.Fatal("install without --settings must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/cmd/agentmon-agent/ -run TestHooksMain`
Expected: FAIL (undefined: hooksMain).

- [ ] **Step 3: Implement the CLI** — create `agent/cmd/agentmon-agent/hooks_cli.go`:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/hooks"
)

// hooksMain runs `agentmon-agent hooks <print|install|uninstall>`.
func hooksMain(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentmon-agent hooks <print|install|uninstall> [--config p] [--settings p]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("hooks "+sub, flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	settings := fs.String("settings", "", "path to the Claude Code settings.json (required for install/uninstall)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch sub {
	case "print":
		snip, err := hooks.Snippet(cfg)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snip)
	case "install":
		if *settings == "" {
			return fmt.Errorf("hooks install requires --settings <PATH>")
		}
		existing, err := hooks.LoadSettings(*settings)
		if err != nil {
			return err
		}
		merged, err := hooks.Merge(existing, cfg)
		if err != nil {
			return err
		}
		if err := hooks.SaveSettings(*settings, merged); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "installed AgentMon hooks into %s\n", *settings)
		return nil
	case "uninstall":
		if *settings == "" {
			return fmt.Errorf("hooks uninstall requires --settings <PATH>")
		}
		existing, err := hooks.LoadSettings(*settings)
		if err != nil {
			return err
		}
		if err := hooks.SaveSettings(*settings, hooks.Unmerge(existing)); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed AgentMon hooks from %s\n", *settings)
		return nil
	default:
		return fmt.Errorf("unknown hooks subcommand %q", sub)
	}
}

// hookTestMain runs `agentmon-agent hook-test` — synthesizes a hook POST to the
// local agent to verify the wiring end-to-end (design §10.3).
func hookTestMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("hook-test", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	pane := fs.String("pane", os.Getenv("TMUX_PANE"), "tmux pane id (defaults to $TMUX_PANE)")
	event := fs.String("event", "Stop", "hook event name")
	kind := fs.String("notification-kind", "", "notification_type (for Notification)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.HookToken == "" {
		return fmt.Errorf("hook_token not configured; /hook is disabled")
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	body := fmt.Sprintf(`{"hook_event_name":%q,"notification_type":%q,"session_id":"hook-test"}`, *event, *kind)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/hook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
	req.Header.Set("X-AgentMon-Pane", *pane)
	req.Header.Set("X-AgentMon-Tmux", os.Getenv("TMUX"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	fmt.Fprintf(stdout, "hook-test → HTTP %d\n", resp.StatusCode)
	return nil
}
```

- [ ] **Step 4: Wire the server + dispatch in main.go.** In `agent/cmd/agentmon-agent/main.go`:

Add imports: `"os"`, `"agentmon/agent/internal/hooks"` (keep `state`/others from Task 6). At the very top of `main()`, add subcommand dispatch before flag parsing:

```go
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hooks":
			if err := hooksMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
		case "hook-test":
			if err := hookTestMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	// ... existing daemon body (config load, handlers, ListenAndServe) ...
}
```

Then, after the `/panes/{paneId}/io` registration and before the `log.Printf("agentmon-agent ...")` line, mount the hook intake when enabled:

```go
	if cfg.HookToken != "" {
		if cfg.HookTokenFile != "" {
			if err := hooks.WriteTokenFile(cfg.HookTokenFile, cfg.HookToken); err != nil {
				log.Fatalf("hook token file: %v", err)
			}
		}
		mux.Handle("POST /hook", hooks.RequireHookAuth(cfg.HookToken, hooks.HookHandler(cfg, machine, nil)))
		log.Printf("hook intake enabled at POST /hook")
	}
```

- [ ] **Step 5: Run tests + build + vet**

Run: `go test ./agent/cmd/agentmon-agent/ ./... -race && go build ./... && go vet ./...`
Expected: PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add agent/cmd/agentmon-agent/
git commit -m "feat(agent): mount POST /hook + hooks/hook-test CLI (M6)"
```

---

## Task 8: Full-suite verification + safe manual smoke

**Files:** none (verification only).

- [ ] **Step 1: Whole-repo test under race**

Run: `go test ./... -race`
Expected: PASS (agent + shared green; hub/web untouched still green).

- [ ] **Step 2: Vet + build the agent for the deploy arch (CGO off)**

Run: `go vet ./... && CGO_ENABLED=0 go build ./agent/...`
Expected: clean.

- [ ] **Step 3: Print the snippet from a scratch config (no writes, sanity)**

Create a scratch `agent.toml` in `t`-style scratch (NOT `/etc`), e.g. under the session scratchpad, with `listen`, `server_id`, `hub_token=env:...`, `directive_key=env:...`, `hook_token=env:...`, one `[[targets]]`. Run:
`go run ./agent/cmd/agentmon-agent hooks print --config <scratch>/agent.toml`
Expected: a JSON `{"hooks":{...}}` block with all nine events and `127.0.0.1:<port>/hook`.

- [ ] **Step 4 (optional, safe live smoke):** Following the §1 spike's safety pattern — a **throwaway tmux socket**, a scratch agent on a loopback port, and a temp `--settings` file — drive a real Claude through `hooks install --settings <tmp>` and confirm `GET /sessions` reports `working`/`blocked`/`done`. **Never** touch `~/.claude/settings.json`, session `0`, or the `agentmon` demo panes. Tear the socket down afterward.

- [ ] **Step 5:** No commit (verification only). Proceed to `/multi-review --codex` and fix everything but nitpicks (per the kickoff).

---

## Self-Review (completed by plan author)

**Spec coverage:** shared.State + RollUp (Task 1 ↔ spec §2); state machine + mapping (Task 2 ↔ §3); hook_token config (Task 3 ↔ §5); POST /hook intake + correlation + soft-drop + tolerance (Task 4 ↔ §4, §1.3); installer print/merge/unmerge + token file (Task 5 ↔ §5, §7); GET /sessions stamping (Task 6 ↔ §6); /hook wiring + CLI dispatch + hook-test (Task 7 ↔ §7, §8); acceptance + safe smoke (Task 8 ↔ §10, §9). All spec sections map to a task.

**Placeholder scan:** none — every code/step is concrete.

**Type consistency:** `SessionsHandler(cfg, disc, m)` defined in Task 6 and called in main.go (Task 6 step 4); `state.Event{Target,Pane,Name,NotificationKind,ClaudeSessionID,At}` consistent across Tasks 2/4; `hooks.{Command,Snippet,Merge,Unmerge,LoadSettings,SaveSettings,WriteTokenFile,RequireHookAuth,HookHandler}` defined in Tasks 4/5 and consumed in Task 7; `Marker = "agentmon-hook"` consistent in install.go and the CLI test. `events` slice referenced by tests in the same package (Task 5).
