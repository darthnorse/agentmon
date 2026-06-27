# AgentMon Phase 1 · M1 — Agent: Discovery + REST — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `agentmon-agent` discover live tmux sessions on a real server and serve them over an authenticated `GET /sessions` REST endpoint, and land the ported tmux control-mode client that M2 will build the terminal WS on.

**Architecture:** A new `agent/internal/tmux` package holds (a) the verbatim port of the spike's control-mode client (`control.go`), unwired in M1, and (b) a discovery layer that shells out to `tmux list-sessions` / `list-panes -s` through a `Runner` seam so the tree-assembly logic is fully unit-testable in CI without a tmux binary; a real `ExecRunner` plus a dev-box integration test exercise the live path. `agent/internal/api` gains a constant-time bearer-token middleware and a `GET /sessions` handler (target resolved from config, discovery injected for testability); `main.go` wires them, requiring a non-empty `hub_token` at startup. Healthz stays unauthenticated (liveness probe).

**Tech Stack:** Go 1.23, stdlib only (`net/http` method-pattern mux, `os/exec`, `crypto/subtle`); `agentmon/shared` DTOs (`Session`/`Window`/`Pane`). No new third-party dependencies in M1 (WebSocket + directive verification are M2).

## Global Constraints

*(Every task's requirements implicitly include this section — values copied verbatim from the spec / repo state.)*

- **Go 1.23**, all Go binaries build with **`CGO_ENABLED=0`** (static). No new third-party deps in M1.
- **Module paths:** `agentmon/agent` imports `agentmon/shared` via the existing `replace agentmon/shared => ../shared`. The new package is `agentmon/agent/internal/tmux`.
- **Session tree is the shared DTO** (`shared/session.go`, already built in M0): `Session{Name, Server, Target, Cwd, Command, Windows}`, `Window{ID, Index, Name, Panes}`, `Pane{ID, Command, Cwd}`. **No `state` field in Phase 1** (hooks are Phase 3). Do not add fields to these structs.
- **Resource ID forms** (from `shared/ids.go`): `pane:<serverId>/<targetId>/<paneId>`, `session:<serverId>/<targetId>/<name>`. Not minted in M1, but discovery output must be consistent with them.
- **M1 scope only.** Build: control-client port, discovery, `GET /healthz` (already exists), `GET /sessions`, **bearer-token verify**. Do **not** build: the terminal WebSocket, HMAC directive verify/nonce/expiry, `mode=ro|rw` enforcement, `POST /sessions`, `POST /hook` — those are M2+.
- **Bearer auth applies to `/sessions`; `/healthz` stays open** (liveness probe — exposes only version/serverId/tmuxAvailable, no secrets; the hub can poll it without minting a directive). This is the deliberate M1 reading of design §12's "all endpoints require bearer."
- **tmux-dependent tests do not run in CI** (no tmux there). Pure assembly logic is tested with a fake `Runner` (runs everywhere); the live `ExecRunner` path is an integration test that `t.Skip`s when `tmux` is absent.
- **Done when (M1 acceptance):** `curl -H 'Authorization: Bearer <token>'` an agent on a real server → live session tree JSON; bad/no token → `401`; control-mode unit tests green.

---

## File structure (created / modified in M1)

```text
agent/
├── cmd/agentmon-agent/main.go              # MODIFY: wire GET /sessions + bearer; require hub_token
└── internal/
    ├── tmux/                               # NEW package
    │   ├── control.go                      # NEW: verbatim port of spike-0.5/control.go (package main → tmux)
    │   ├── control_test.go                 # NEW: verbatim port of spike-0.5/control_test.go
    │   ├── discovery.go                    # NEW: Discover() + Runner seam + pure assembly
    │   ├── discovery_test.go               # NEW: fake-Runner unit tests (CI-green)
    │   ├── runner.go                       # NEW: ExecRunner (real tmux exec)
    │   └── discovery_integration_test.go   # NEW: real-tmux test, t.Skip when tmux absent
    ├── api/
    │   ├── bearer.go                       # NEW: RequireBearer middleware
    │   ├── bearer_test.go                  # NEW
    │   ├── sessions.go                     # NEW: SessionsHandler + Discoverer seam
    │   ├── sessions_test.go                # NEW
    │   ├── health.go                       # MODIFY (Task 6): compute tmuxAvailable at construction
    │   └── health_test.go                  # MODIFY (Task 6): pass tmuxAvailable in
    └── config/
        ├── config.go                       # MODIFY: add ResolveTarget method
        ├── config_test.go                  # MODIFY: add ResolveTarget tests + fix unchecked WriteFile errs
shared/
└── session.go                              # MODIFY: add SessionList response envelope
```

**Carry-over triage (from `docs/superpowers/m0-carryover.md`).** Folded into M1 because they touch files this milestone already owns: agent `config_test.go` unchecked `os.WriteFile` errors (Task 6), agent `health.go` per-request `LookPath` → construction time (Task 6). **Explicitly deferred** (not agent-code, out of M1's theme): `.dockerignore`/`Dockerfile` build-infra → fold into the hub/build work (M3/M4); hubd `repo_test.go`/`directive_test.go`/`health_test.go` hygiene → with the hub milestones. **Not M1:** `resolveRef` secret hardening is explicitly **M3** (before auth) per the carry-over — do not do it here (but Task 5 *does* add a minimal non-empty `hub_token` startup check, since M1 is the first real consumer of that token).

---

## Task 1: Port the tmux control-mode client into `agent/internal/tmux`

Verbatim port of the spike's validated control client so M2 can build the terminal WS on it. It is **not wired to anything in M1** — this task only lands the code + its pure-helper unit tests, green in the agent module.

**Files:**
- Create: `agent/internal/tmux/control.go` (from `spike-0.5/control.go`)
- Create: `agent/internal/tmux/control_test.go` (from `spike-0.5/control_test.go`)

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces: `tmux.ControlClient` with `NewControlClient(ctx, session, pane string) (*ControlClient, error)`, methods `SendInput([]byte) error`, `Resize(cols, rows int) error`, `Close()`, exported fields `Output chan []byte`, `Done chan struct{}`. Unexported pure helpers `parseOutput`, `unescapeOutput`, `encodeSendKeys` (used by M2, tested here).

- [ ] **Step 1: Copy the two spike files into the new package**

```bash
mkdir -p agent/internal/tmux
cp spike-0.5/control.go      agent/internal/tmux/control.go
cp spike-0.5/control_test.go agent/internal/tmux/control_test.go
```

- [ ] **Step 2: Change the package declaration in both files**

Edit `agent/internal/tmux/control.go` line 1: `package main` → `package tmux`.
Edit `agent/internal/tmux/control_test.go` line 1: `package main` → `package tmux`.

No other edits. The files are stdlib-only (`bufio`, `context`, `fmt`, `io`, `log`, `os/exec`, `strings`, `sync` / `reflect`, `testing`) and self-contained, so they compile unchanged in the agent module.

- [ ] **Step 3: Run the ported tests to verify they pass**

Run: `go test ./agent/internal/tmux/ -run 'TestUnescapeOutput|TestParseOutput|TestEncodeSendKeys' -v`
Expected: PASS — `TestUnescapeOutput` (8 sub-cases), `TestParseOutput`, `TestEncodeSendKeys`, `TestEncodeSendKeysChunks`, `TestEncodeSendKeysEmpty`.

- [ ] **Step 4: Verify the whole agent module still builds**

Run: `go build ./agent/...`
Expected: no output (success). `go vet ./agent/internal/tmux/` clean.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/tmux/control.go agent/internal/tmux/control_test.go
git commit -m "feat(m1): port spike control-mode client into agent/internal/tmux"
```

---

## Task 2: Session discovery — `Runner` seam + pure tree assembly

The core of M1. `Discover` shells out via an injected `Runner`, so the line-parsing and window-grouping logic is fully unit-tested in CI **without tmux**. Uses two tmux calls: one `list-sessions`, then one `list-panes -s` per session (panes carry their window's id/index/name/active flags, so one call rebuilds each session's window→pane tree).

**Files:**
- Create: `agent/internal/tmux/discovery.go`
- Test: `agent/internal/tmux/discovery_test.go`

**Interfaces:**
- Consumes: `agentmon/shared` (`Session`, `Window`, `Pane`).
- Produces:
  - `type Runner func(ctx context.Context, args ...string) ([]byte, error)`
  - `type DiscoverOpts struct { ServerID, TargetLabel, SocketName string }`
  - `func Discover(ctx context.Context, run Runner, opts DiscoverOpts) ([]shared.Session, error)` — returns `[]shared.Session{}` (never nil) on success; empty (not error) when no tmux server is running.

- [ ] **Step 1: Write the failing tests**

Create `agent/internal/tmux/discovery_test.go`:

```go
package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner returns canned output keyed by tmux subcommand. For list-panes it
// keys on the "-t <sessionId>" argument so multi-session cases are exercised.
func fakeRunner(t *testing.T, sessions string, panes map[string]string, sessErr error) Runner {
	t.Helper()
	return func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case contains(args, "list-sessions"):
			if sessErr != nil {
				return nil, sessErr
			}
			return []byte(sessions), nil
		case contains(args, "list-panes"):
			sid := argAfter(args, "-t")
			out, ok := panes[sid]
			if !ok {
				t.Fatalf("no canned list-panes for %q (args=%v)", sid, args)
			}
			return []byte(out), nil
		default:
			t.Fatalf("unexpected tmux args: %v", args)
			return nil, nil
		}
	}
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// p builds a US-delimited record from fields (tests must not hardcode \x1f).
func p(fields ...string) string { return strings.Join(fields, fieldSep) }

func TestDiscoverNoServerIsEmpty(t *testing.T) {
	run := fakeRunner(t, "", nil, errors.New("tmux list-sessions: exit status 1: no server running on /tmp/tmux-0/default"))
	got, err := Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
}

func TestDiscoverGroupsPanesIntoWindowsInOrder(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": strings.Join([]string{
			// window_id windex wname wactive  pane_id pcmd pcwd pactive
			p("@1", "0", "main", "1", "%0", "zsh", "/home/dev/proj", "1"),
			p("@1", "0", "main", "1", "%1", "vim", "/home/dev/proj", "0"),
			p("@2", "1", "logs", "0", "%2", "tail", "/var/log", "1"),
		}, "\n") + "\n",
	}
	got, err := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if len(s.Windows) != 2 {
		t.Fatalf("want 2 windows, got %d (%+v)", len(s.Windows), s.Windows)
	}
	if s.Windows[0].ID != "@1" || s.Windows[0].Index != "0" || s.Windows[0].Name != "main" {
		t.Fatalf("window[0] = %+v", s.Windows[0])
	}
	if len(s.Windows[0].Panes) != 2 || s.Windows[0].Panes[0].ID != "%0" || s.Windows[0].Panes[1].ID != "%1" {
		t.Fatalf("window[0] panes = %+v", s.Windows[0].Panes)
	}
	if len(s.Windows[1].Panes) != 1 || s.Windows[1].Panes[0].Command != "tail" {
		t.Fatalf("window[1] panes = %+v", s.Windows[1].Panes)
	}
}

func TestDiscoverSessionCwdCommandFromActivePane(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": strings.Join([]string{
			p("@1", "0", "main", "0", "%0", "zsh", "/home/dev/inactive", "1"),
			p("@2", "1", "logs", "1", "%2", "claude", "/home/dev/active", "1"),
		}, "\n") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if got[0].Cwd != "/home/dev/active" || got[0].Command != "claude" {
		t.Fatalf("session cwd/command = %q/%q, want active pane", got[0].Cwd, got[0].Command)
	}
}

func TestDiscoverSessionFallsBackToFirstPaneWhenNoActiveFlag(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": p("@1", "0", "main", "0", "%0", "bash", "/srv/app", "0") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if got[0].Cwd != "/srv/app" || got[0].Command != "bash" {
		t.Fatalf("fallback cwd/command = %q/%q", got[0].Cwd, got[0].Command)
	}
}

func TestDiscoverStampsServerTargetAndHandlesMultipleSessions(t *testing.T) {
	sessions := p("$1", "alpha") + "\n" + p("$2", "beta") + "\n"
	panes := map[string]string{
		"$1": p("@1", "0", "w", "1", "%0", "zsh", "/a", "1") + "\n",
		"$2": p("@2", "0", "w", "1", "%1", "zsh", "/b", "1") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "server-a", TargetLabel: "default"})
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	for _, s := range got {
		if s.Server != "server-a" || s.Target != "default" {
			t.Fatalf("server/target not stamped: %+v", s)
		}
	}
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("names/order: %q %q", got[0].Name, got[1].Name)
	}
}

func TestDiscoverPassesSocketFlag(t *testing.T) {
	var sawSocket bool
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if contains(args, "-L") && argAfter(args, "-L") == "mysock" {
			sawSocket = true
		}
		if contains(args, "list-sessions") {
			return []byte(""), errors.New("no server running")
		}
		return nil, nil
	}
	_, _ = Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", SocketName: "mysock"})
	if !sawSocket {
		t.Fatal("expected -L mysock in tmux args")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail (compile error — package has no `Discover`/`fieldSep`)**

Run: `go test ./agent/internal/tmux/ -run TestDiscover -v`
Expected: FAIL — `undefined: Discover`, `undefined: fieldSep`, `undefined: DiscoverOpts`, `undefined: Runner`.

- [ ] **Step 3: Write the implementation**

Create `agent/internal/tmux/discovery.go`:

```go
package tmux

import (
	"context"
	"strings"

	"agentmon/shared"
)

// fieldSep is ASCII Unit Separator (0x1f). We use it as the tmux -F delimiter
// because it cannot appear in a session/window name or a filesystem path, so it
// safely separates fields that may contain spaces.
const fieldSep = "\x1f"

// Runner executes `tmux <args...>` and returns stdout. Production uses
// ExecRunner; tests inject a fake. On a tmux command failure the returned error
// SHOULD carry tmux's stderr text (ExecRunner does) so Discover can recognise the
// benign "no server running" case.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// DiscoverOpts carries the primitive inputs of one discovery pass (no config
// coupling — the api layer maps a config.Target into this).
type DiscoverOpts struct {
	ServerID    string
	TargetLabel string
	SocketName  string // "" → tmux default socket
}

// paneFmt lists, per pane, the owning window's id/index/name/active flag and the
// pane's id/command/cwd/active flag — enough to rebuild the window→pane tree and
// pick the session's active pane from a single `list-panes -s` call.
const paneFmt = "#{window_id}" + fieldSep + "#{window_index}" + fieldSep +
	"#{window_name}" + fieldSep + "#{window_active}" + fieldSep +
	"#{pane_id}" + fieldSep + "#{pane_current_command}" + fieldSep +
	"#{pane_current_path}" + fieldSep + "#{pane_active}"

// Discover returns the live session tree for one target. A target whose tmux
// server is not running yields an empty (non-nil) slice, not an error.
func Discover(ctx context.Context, run Runner, opts DiscoverOpts) ([]shared.Session, error) {
	base := socketArgs(opts.SocketName)

	sessOut, err := run(ctx, with(base, "list-sessions", "-F",
		"#{session_id}"+fieldSep+"#{session_name}")...)
	if err != nil {
		if isNoServer(err) {
			return []shared.Session{}, nil
		}
		return nil, err
	}

	sessions := []shared.Session{}
	for _, line := range nonEmptyLines(sessOut) {
		sid, name, ok := cut2(line)
		if !ok {
			continue
		}
		windows, cwd, command, err := discoverPanes(ctx, run, base, sid)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, shared.Session{
			Name:    name,
			Server:  opts.ServerID,
			Target:  opts.TargetLabel,
			Cwd:     cwd,
			Command: command,
			Windows: windows,
		})
	}
	return sessions, nil
}

// discoverPanes runs one `list-panes -s` for the session and assembles its
// windows (first-seen order, which follows tmux's window-index order) along with
// the session-level cwd/command taken from the active window's active pane.
func discoverPanes(ctx context.Context, run Runner, base []string, sessionID string) (windows []shared.Window, sessCwd, sessCommand string, err error) {
	out, err := run(ctx, with(base, "list-panes", "-s", "-t", sessionID, "-F", paneFmt)...)
	if err != nil {
		return nil, "", "", err
	}
	pos := map[string]int{} // window_id → index in windows
	for _, line := range nonEmptyLines(out) {
		f := strings.Split(line, fieldSep)
		if len(f) != 8 {
			continue
		}
		wid, windex, wname, wactive := f[0], f[1], f[2], f[3]
		pid, pcmd, pcwd, pactive := f[4], f[5], f[6], f[7]
		i, seen := pos[wid]
		if !seen {
			i = len(windows)
			pos[wid] = i
			windows = append(windows, shared.Window{ID: wid, Index: windex, Name: wname})
		}
		windows[i].Panes = append(windows[i].Panes, shared.Pane{ID: pid, Command: pcmd, Cwd: pcwd})
		if wactive == "1" && pactive == "1" {
			sessCwd, sessCommand = pcwd, pcmd
		}
	}
	if sessCwd == "" && sessCommand == "" && len(windows) > 0 && len(windows[0].Panes) > 0 {
		sessCwd = windows[0].Panes[0].Cwd
		sessCommand = windows[0].Panes[0].Command
	}
	return windows, sessCwd, sessCommand, nil
}

// with returns base followed by extra, never aliasing base's backing array.
func with(base []string, extra ...string) []string {
	args := make([]string, 0, len(base)+len(extra))
	args = append(args, base...)
	return append(args, extra...)
}

func socketArgs(socket string) []string {
	if socket == "" {
		return nil
	}
	return []string{"-L", socket}
}

func isNoServer(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no server running")
}

func nonEmptyLines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func cut2(line string) (a, b string, ok bool) {
	before, after, found := strings.Cut(line, fieldSep)
	return before, after, found
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./agent/internal/tmux/ -run TestDiscover -v`
Expected: PASS — all six `TestDiscover*` cases.

- [ ] **Step 5: Run the whole tmux package + vet**

Run: `go test ./agent/internal/tmux/ && go vet ./agent/internal/tmux/`
Expected: `ok agentmon/agent/internal/tmux`, vet clean.

- [ ] **Step 6: Commit**

```bash
git add agent/internal/tmux/discovery.go agent/internal/tmux/discovery_test.go
git commit -m "feat(m1): tmux session discovery with Runner seam + pure tree assembly"
```

---

## Task 3: Real `ExecRunner` + dev-box integration test

The production `Runner`, plus an integration test that drives a real `tmux -L` socket. The test `t.Skip`s when tmux is absent, so `go test ./...` stays green in CI; on the dev box (tmux 3.5a) it exercises the live exec → parse path end-to-end.

**Files:**
- Create: `agent/internal/tmux/runner.go`
- Test: `agent/internal/tmux/discovery_integration_test.go`

**Interfaces:**
- Consumes: `Discover`, `DiscoverOpts` (Task 2).
- Produces: `func ExecRunner(ctx context.Context, args ...string) ([]byte, error)` — a `Runner` that runs the real `tmux` binary and wraps stderr into its error (so `isNoServer` works on the live path).

- [ ] **Step 1: Write the failing integration test**

Create `agent/internal/tmux/discovery_integration_test.go`:

```go
package tmux

import (
	"context"
	"os/exec"
	"testing"
)

// requireTmux skips on hosts without tmux (e.g. CI). Integration tests run on the
// dev box / real servers only, per the Phase 1 testing strategy.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping integration test")
	}
}

const testSocket = "agentmon-m1-test"

func killTestServer() { _ = exec.Command("tmux", "-L", testSocket, "kill-server").Run() }

func TestExecRunnerDiscoversRealSession(t *testing.T) {
	requireTmux(t)
	killTestServer()
	t.Cleanup(killTestServer)

	mk := exec.Command("tmux", "-L", testSocket, "new-session", "-d", "-s", "proj", "-c", "/tmp")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}

	got, err := Discover(context.Background(), ExecRunner,
		DiscoverOpts{ServerID: "srv", TargetLabel: "default", SocketName: testSocket})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].Name != "proj" {
		t.Fatalf("sessions = %+v", got)
	}
	s := got[0]
	if s.Server != "srv" || s.Target != "default" {
		t.Fatalf("server/target = %q/%q", s.Server, s.Target)
	}
	if len(s.Windows) == 0 || len(s.Windows[0].Panes) == 0 {
		t.Fatalf("expected at least one window/pane: %+v", s.Windows)
	}
	if s.Cwd == "" || s.Command == "" {
		t.Fatalf("session cwd/command empty: %q/%q", s.Cwd, s.Command)
	}
}

func TestExecRunnerEmptyWhenNoServer(t *testing.T) {
	requireTmux(t)
	killTestServer() // ensure no server on this socket

	got, err := Discover(context.Background(), ExecRunner,
		DiscoverOpts{ServerID: "srv", TargetLabel: "default", SocketName: testSocket})
	if err != nil {
		t.Fatalf("want nil error for no-server, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails (no `ExecRunner`)**

Run: `go test ./agent/internal/tmux/ -run TestExecRunner -v`
Expected: FAIL — `undefined: ExecRunner`.

- [ ] **Step 3: Write `ExecRunner`**

Create `agent/internal/tmux/runner.go`:

```go
package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner runs the real tmux binary. On failure it folds tmux's stderr into
// the error so Discover can detect the benign "no server running" message.
func ExecRunner(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tmux %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
```

- [ ] **Step 4: Run the integration tests (dev box has tmux 3.5a)**

Run: `go test ./agent/internal/tmux/ -run TestExecRunner -v`
Expected: PASS — `TestExecRunnerDiscoversRealSession`, `TestExecRunnerEmptyWhenNoServer`. (On a tmux-less host both would log `SKIP`; that is the CI behaviour.)

- [ ] **Step 5: Run the full package + confirm CI-shape (no tmux) still green**

Run: `go test ./agent/internal/tmux/`
Expected: `ok`. (Simulate CI with `PATH= go test ./agent/internal/tmux/ -run TestExecRunner -v` → both tests `SKIP`, exit 0.)

- [ ] **Step 6: Commit**

```bash
git add agent/internal/tmux/runner.go agent/internal/tmux/discovery_integration_test.go
git commit -m "feat(m1): real tmux ExecRunner + dev-box discovery integration test"
```

---

## Task 4: Bearer-token middleware

A constant-time bearer check, isolated as its own task because it is the M1 security gate (`bad/no token → 401`). Generic `http.Handler` wrapper, fully unit-tested with no tmux/config dependency.

**Files:**
- Create: `agent/internal/api/bearer.go`
- Test: `agent/internal/api/bearer_test.go`

**Interfaces:**
- Consumes: nothing (stdlib).
- Produces: `func RequireBearer(token string, next http.Handler) http.Handler` — passes through only when `Authorization` is exactly `Bearer <token>` (constant-time compare); otherwise writes `401` with `{"error":"unauthorized"}` and does not call `next`.

- [ ] **Step 1: Write the failing tests**

Create `agent/internal/api/bearer_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func passThrough() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func doReq(h http.Handler, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRequireBearerAllowsCorrectToken(t *testing.T) {
	rr := doReq(RequireBearer("s3cret", passThrough()), "Bearer s3cret")
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestRequireBearerRejects(t *testing.T) {
	cases := map[string]string{
		"missing header":   "",
		"wrong scheme":     "Token s3cret",
		"wrong token":      "Bearer nope",
		"empty bearer":     "Bearer ",
		"no space":         "Bearers3cret",
		"prefix-only match": "Bearer s3cretXX",
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doReq(RequireBearer("s3cret", passThrough()), auth)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("auth=%q → code %d, want 401", auth, rr.Code)
			}
			if rr.Body.String() == "ok" {
				t.Fatal("next handler should not have run")
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails (no `RequireBearer`)**

Run: `go test ./agent/internal/api/ -run TestRequireBearer -v`
Expected: FAIL — `undefined: RequireBearer`.

- [ ] **Step 3: Write the middleware**

Create `agent/internal/api/bearer.go`:

```go
package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// RequireBearer rejects any request whose Authorization header is not exactly
// "Bearer <token>". The comparison is constant-time. token must be non-empty
// (enforced at startup in main).
func RequireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		presented, ok := strings.CutPrefix(h, prefix)
		if !ok || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./agent/internal/api/ -run TestRequireBearer -v`
Expected: PASS — `TestRequireBearerAllowsCorrectToken` and all `TestRequireBearerRejects` sub-cases.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/api/bearer.go agent/internal/api/bearer_test.go
git commit -m "feat(m1): constant-time bearer-token middleware for agent REST"
```

---

## Task 5: `GET /sessions` handler + target resolution + wiring (M1 done-when)

Wire it all together: a `config.ResolveTarget` helper, a shared `SessionList` response envelope, the `SessionsHandler` (discovery injected so it unit-tests without tmux), and `main.go` registering `GET /sessions` behind `RequireBearer` while requiring a non-empty `hub_token` at startup. This task ends at the M1 acceptance criterion.

**Files:**
- Modify: `shared/session.go` (add `SessionList`)
- Modify: `agent/internal/config/config.go` (add `ResolveTarget`)
- Modify: `agent/internal/config/config_test.go` (add `ResolveTarget` tests)
- Create: `agent/internal/api/sessions.go`, `agent/internal/api/sessions_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go`

**Interfaces:**
- Consumes: `tmux.Discover`/`DiscoverOpts`/`ExecRunner` (Tasks 2–3), `RequireBearer` (Task 4), `config.Config`/`Target`, `shared.Session`.
- Produces:
  - `shared.SessionList struct { Sessions []Session `json:"sessions"` }` — the agent `GET /sessions` envelope (the hub re-shapes these into its public array in M3).
  - `func (c Config) ResolveTarget(label string) (Target, bool)` — empty label → first target (Phase 1 default); unknown label → `false`.
  - `type api.Discoverer func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error)`
  - `func api.SessionsHandler(cfg config.Config, discover Discoverer) http.HandlerFunc`

- [ ] **Step 1: Add the response envelope to shared (failing consumer first is implicit; this struct has no behaviour to test)**

Edit `shared/session.go` — append after the `Pane` struct:

```go
// SessionList is the agent's GET /sessions response envelope. The hub re-shapes
// these into its public /servers/{id}/sessions array; this is the agent↔hub form.
type SessionList struct {
	Sessions []Session `json:"sessions"`
}
```

Run: `go build ./shared/`
Expected: success.

- [ ] **Step 2: Write the failing `ResolveTarget` tests**

Edit `agent/internal/config/config_test.go` — add at the end of the file:

```go
func TestResolveTarget(t *testing.T) {
	cfg := Config{Targets: []Target{
		{Label: "default", SocketName: ""},
		{Label: "build", SocketName: "buildsock"},
	}}

	if tg, ok := cfg.ResolveTarget(""); !ok || tg.Label != "default" {
		t.Fatalf("empty → %+v ok=%v, want default", tg, ok)
	}
	if tg, ok := cfg.ResolveTarget("build"); !ok || tg.SocketName != "buildsock" {
		t.Fatalf("build → %+v ok=%v", tg, ok)
	}
	if _, ok := cfg.ResolveTarget("nope"); ok {
		t.Fatal("unknown label should not resolve")
	}
}

func TestResolveTargetNoTargets(t *testing.T) {
	if _, ok := (Config{}).ResolveTarget(""); ok {
		t.Fatal("no targets configured should not resolve")
	}
}
```

- [ ] **Step 3: Run to verify it fails (no `ResolveTarget`)**

Run: `go test ./agent/internal/config/ -run TestResolveTarget -v`
Expected: FAIL — `cfg.ResolveTarget undefined`.

- [ ] **Step 4: Implement `ResolveTarget`**

Edit `agent/internal/config/config.go` — add after the `Load` function:

```go
// ResolveTarget selects a target by label. An empty label resolves to the first
// configured target (the Phase 1 default). Returns false when no target matches
// or none are configured.
func (c Config) ResolveTarget(label string) (Target, bool) {
	if len(c.Targets) == 0 {
		return Target{}, false
	}
	if label == "" {
		return c.Targets[0], true
	}
	for _, t := range c.Targets {
		if t.Label == label {
			return t, true
		}
	}
	return Target{}, false
}
```

- [ ] **Step 5: Run to verify ResolveTarget passes**

Run: `go test ./agent/internal/config/ -run TestResolveTarget -v`
Expected: PASS.

- [ ] **Step 6: Write the failing `SessionsHandler` tests**

Create `agent/internal/api/sessions_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

func testCfg() config.Config {
	return config.Config{
		ServerID: "server-a",
		Targets:  []config.Target{{Label: "default", SocketName: ""}},
	}
}

func TestSessionsHandlerReturnsTree(t *testing.T) {
	var gotOpts tmux.DiscoverOpts
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		gotOpts = opts
		return []shared.Session{{Name: "proj", Server: "server-a", Target: "default",
			Cwd: "/home/dev/proj", Command: "zsh",
			Windows: []shared.Window{{ID: "@1", Index: "0", Name: "main",
				Panes: []shared.Pane{{ID: "%0", Command: "zsh", Cwd: "/home/dev/proj"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc)
	req := httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code %d", rr.Code)
	}
	var body shared.SessionList
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(body.Sessions) != 1 || body.Sessions[0].Name != "proj" {
		t.Fatalf("sessions = %+v", body.Sessions)
	}
	if gotOpts.ServerID != "server-a" || gotOpts.TargetLabel != "default" {
		t.Fatalf("discover opts = %+v", gotOpts)
	}
}

func TestSessionsHandlerEmptyTargetUsesDefault(t *testing.T) {
	called := false
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		called = true
		if opts.TargetLabel != "default" {
			t.Fatalf("want default target, got %q", opts.TargetLabel)
		}
		return []shared.Session{}, nil
	}
	h := SessionsHandler(testCfg(), disc)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("code=%d called=%v", rr.Code, called)
	}
}

func TestSessionsHandlerUnknownTarget404(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		t.Fatal("discover must not be called for unknown target")
		return nil, nil
	}
	h := SessionsHandler(testCfg(), disc)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=ghost", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code %d, want 404", rr.Code)
	}
}

func TestSessionsHandlerDiscoveryError500(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return nil, errors.New("tmux boom")
	}
	h := SessionsHandler(testCfg(), disc)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code %d, want 500", rr.Code)
	}
}
```

- [ ] **Step 7: Run to verify it fails (no `SessionsHandler`)**

Run: `go test ./agent/internal/api/ -run TestSessionsHandler -v`
Expected: FAIL — `undefined: SessionsHandler`.

- [ ] **Step 8: Write the handler**

Create `agent/internal/api/sessions.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// Discoverer resolves a target's live session tree. Injected so the handler is
// testable without a real tmux (production binds tmux.Discover + tmux.ExecRunner).
type Discoverer func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error)

// SessionsHandler serves GET /sessions?target=<label>. Target resolves via config
// (empty → default); discovery runs through the injected Discoverer.
func SessionsHandler(cfg config.Config, discover Discoverer) http.HandlerFunc {
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
			writeJSONError(w, http.StatusInternalServerError, "discovery failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.SessionList{Sessions: sessions})
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

- [ ] **Step 9: Run handler tests + full api package**

Run: `go test ./agent/internal/api/ -v`
Expected: PASS — `TestSessionsHandler*`, plus the existing `TestHealthHandler` and `TestRequireBearer*`.

- [ ] **Step 10: Wire into `main.go`**

Replace `agent/cmd/agentmon-agent/main.go` with:

```go
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.HubToken == "" {
		log.Fatal("config: hub_token is required")
	}

	discover := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return tmux.Discover(ctx, tmux.ExecRunner, opts)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version))
	mux.Handle("GET /sessions", api.RequireBearer(cfg.HubToken, api.SessionsHandler(cfg, discover)))

	log.Printf("agentmon-agent %s listening on %s (server %s)", version, cfg.Listen, cfg.ServerID)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
```

- [ ] **Step 11: Build the agent + run the whole agent module's tests**

Run: `go build ./agent/... && go test ./agent/... && go vet ./agent/...`
Expected: build succeeds; tests `ok`; vet clean.

- [ ] **Step 12: Manual end-to-end smoke against real tmux (the M1 done-when)**

```bash
# start a tmux session to discover
tmux -L agentmon-m1-smoke new-session -d -s demo -c /tmp
# minimal agent.toml pointing at that socket
cat > /tmp/agent-m1.toml <<'EOF'
listen        = "127.0.0.1:8377"
server_id     = "server-a"
hub_token     = "env:AGENTMON_AGENT_TOKEN"
directive_key = "unused-in-m1"
[[targets]]
  os_user = "dev"
  socket_name = "agentmon-m1-smoke"
  label = "default"
EOF
AGENTMON_AGENT_TOKEN=test-token go run ./agent/cmd/agentmon-agent -config /tmp/agent-m1.toml &
sleep 1
# 1) good token → live session tree
curl -fsS -H 'Authorization: Bearer test-token' 'http://127.0.0.1:8377/sessions?target=default' | tee /tmp/sess.json
# 2) bad token → 401
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer WRONG' http://127.0.0.1:8377/sessions
# 3) no token → 401
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8377/sessions
# 4) healthz stays open
curl -fsS http://127.0.0.1:8377/healthz
# cleanup
kill %1; tmux -L agentmon-m1-smoke kill-server
```

Expected: (1) JSON `{"sessions":[{"name":"demo",...,"windows":[...]}]}` with the `demo` session; (2) `401`; (3) `401`; (4) `{"ok":true,...}`.

- [ ] **Step 13: Commit**

```bash
git add shared/session.go agent/internal/config/config.go agent/internal/config/config_test.go \
        agent/internal/api/sessions.go agent/internal/api/sessions_test.go \
        agent/cmd/agentmon-agent/main.go
git commit -m "feat(m1): authenticated GET /sessions — target resolution, discovery wiring, hub_token required"
```

---

## Task 6: M0 carry-over cleanups (agent-local)

The two carry-over items that touch files M1 already owns. Kept as a separate, independently-reviewable commit. (Build-infra and hubd carry-overs are deliberately out of scope — see the triage note above.)

**Files:**
- Modify: `agent/internal/config/config_test.go` (check `os.WriteFile` errors)
- Modify: `agent/internal/api/health.go` + `agent/internal/api/health_test.go` + `agent/cmd/agentmon-agent/main.go` (compute `tmuxAvailable` once at construction)

**Interfaces:**
- Produces: `func HealthHandler(serverID, version string, tmuxAvailable bool) http.HandlerFunc` (signature change: caller now supplies the precomputed bool).

- [ ] **Step 1: Fix unchecked `os.WriteFile` errors in `config_test.go`**

In `agent/internal/config/config_test.go`, both `TestLoadResolvesEnvRefs` and `TestLoadMissingEnvRefErrors` call `os.WriteFile(p, []byte(...), 0o600)` and drop the error. Wrap each:

```go
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "env:AGENTMON_AGENT_TOKEN"
directive_key = "literal-key"
scrollback_lines = 4000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
`), 0o600); err != nil {
		t.Fatal(err)
	}
```

and likewise for the second test's smaller TOML body.

- [ ] **Step 2: Move `tmuxAvailable` to handler-construction time**

Replace `agent/internal/api/health.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
)

// HealthHandler reports liveness. tmuxAvailable is resolved once at startup
// (passed in) rather than per request. Healthz is intentionally unauthenticated.
func HealthHandler(serverID, version string, tmuxAvailable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"version":       version,
			"serverId":      serverID,
			"tmuxAvailable": tmuxAvailable,
		})
	}
}
```

- [ ] **Step 3: Update `health_test.go` for the new signature**

In `agent/internal/api/health_test.go`, change the construction and assertion to supply/expect an explicit bool (no per-request `LookPath`):

```go
func TestHealthHandler(t *testing.T) {
	h := HealthHandler("server-a", "test", true)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body struct {
		OK            bool   `json:"ok"`
		ServerID      string `json:"serverId"`
		Version       string `json:"version"`
		TmuxAvailable bool   `json:"tmuxAvailable"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ServerID != "server-a" || body.Version != "test" || !body.TmuxAvailable {
		t.Fatalf("bad body: %+v", body)
	}
}
```

Remove the now-unused `os/exec` import from the test file.

- [ ] **Step 4: Compute `tmuxAvailable` in `main.go` and pass it in**

In `agent/cmd/agentmon-agent/main.go`, add `"os/exec"` to imports and update the healthz registration:

```go
	_, tmuxErr := exec.LookPath("tmux")
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version, tmuxErr == nil))
```

(Replaces the previous two-arg `HealthHandler` call.)

- [ ] **Step 5: Build + test the whole agent module**

Run: `go build ./agent/... && go test ./agent/... && go vet ./agent/...`
Expected: build succeeds; `TestHealthHandler`, config tests, api tests, tmux unit tests all `ok`; vet clean.

- [ ] **Step 6: Commit**

```bash
git add agent/internal/config/config_test.go agent/internal/api/health.go \
        agent/internal/api/health_test.go agent/cmd/agentmon-agent/main.go
git commit -m "refactor(m1): carry-over cleanups — checked WriteFile in tests; tmuxAvailable at construction"
```

---

## Whole-milestone verification (before the review gate)

Run from the repo root:

- [ ] `go build ./...` — all modules build.
- [ ] `go test ./...` — full suite green (tmux integration tests run on the dev box; they `SKIP` on tmux-less CI).
- [ ] `go vet ./...` — clean.
- [ ] CI-shape check: `CGO_ENABLED=0 go build ./agent/...` succeeds (static).
- [ ] Re-run the Task 5 Step 12 smoke (good token → tree, bad/no token → 401, healthz open).

**Review gate note:** Per the Phase 1 locked decisions (§2.6), the agent's multi-review gate falls **after M2** (agent = M1 + M2). M1 lands on a feature branch and is verified by the checklist above; the full multi-review (incl. Codex) runs once the terminal WS + directive verification (M2) are also in. Follow the project workflow (TDD throughout; fix all review findings but nitpicks). Use `superpowers:finishing-a-development-branch` when M1+M2 are review-clean.

---

## Self-review (against the Phase 1 design spec §4.1, §6.1, §6.3, §8 M1 row, §9)

- **§8 M1 "Port control.go/control_test.go"** → Task 1. ✓
- **§8 M1 "discovery → session tree (name/cwd/command/windows/panes)"** → Task 2 (assembly) + Task 3 (real tmux). Session-level cwd/command sourced from the active pane; window→pane tree from `list-panes -s`. ✓
- **§8 M1 "GET /healthz"** → already exists (M0); refined in Task 6. ✓
- **§8 M1 "GET /sessions"** → Task 5; response is `shared.SessionList` (`{sessions:[Session]}`) matching §12.2's envelope and §6.1's per-session fields. ✓
- **§8 M1 "bearer-token verify"** → Task 4 (middleware) + Task 5 (wired on `/sessions`). ✓
- **§8 M1 done-when "curl Bearer → tree; bad/no token → 401; control unit tests green"** → Task 5 Step 12 smoke + Task 1/Task 4 tests. ✓
- **§4.1 "Single default target in Phase 1; contracts carry targetId"** → `config.ResolveTarget` (empty→default), `?target=` plumbed into `DiscoverOpts.TargetLabel`/`SocketName`; `os_user` recorded but not used for switching in P1 (single target = agent's own user). ✓
- **§9 testing strategy "tmux tests not in CI; pure logic unit-tested"** → `Runner` seam (Task 2, CI) + `t.Skip` integration (Task 3). ✓
- **Out-of-scope confirmed absent:** no WS, no directive verify/nonce/expiry, no `mode=ro|rw`, no `POST /sessions`, no `POST /hook`. ✓
- **Type consistency:** `Runner`, `DiscoverOpts{ServerID,TargetLabel,SocketName}`, `Discover`, `ExecRunner`, `Discoverer`, `SessionsHandler(cfg, discover)`, `ResolveTarget`, `SessionList`, `RequireBearer(token, next)`, `HealthHandler(serverID, version, tmuxAvailable)` are used identically across the tasks that define and consume them. ✓
- **Placeholder scan:** every code step shows complete code; no TBD/"add error handling"/"similar to". ✓

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-27-agentmon-phase-1-m1-agent-discovery.md`.**
