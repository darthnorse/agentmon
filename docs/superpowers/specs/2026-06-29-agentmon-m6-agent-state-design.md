# M6 — Agent-side Claude Code state detection (Phase 3a)

**Status:** approved design, pre-implementation.
**Date:** 2026-06-29.
**Phase:** 3 (Claude Code state via hooks), sub-milestone **a** of 3 (M6 agent → M7 hub → M8 web).
**Builds on:** M1–M5 (the multi-server terminal spine + web SPA), all merged & live-accepted.
**Authoritative sources:** `agentmon-design.md` §9 (state model), §10 (detecting state), §17 (Phase 3
deliverables), §18-Q3 (hook-payload open question); `docs/superpowers/m5-carryover.md`.
**Spike that de-risked this:** `scratchpad/hook-spike/FINDINGS.md` (this session) — the empirical
verification of the Claude Code v2.1.195 hook contract. Findings summarised in §1 below.

---

## 0. Scope

M6 delivers the **agent half** of Phase 3: an agent can be told, via Claude Code hooks, what each
local Claude session is doing, derive a state from that, and expose that state on its existing
inbound HTTP surface so the hub (M7) can aggregate it.

**In scope (M6):**
- `shared.Session.State` + the `State` type and rollup helper (the contract M6/M7/M8 share).
- `agent/internal/state` — a pure, in-memory state machine: hook event → state, keyed per pane,
  rolled up per session.
- `agent/internal/hooks` — a token-authenticated `POST /hook` intake that correlates an incoming
  hook to a tmux pane and feeds the state machine.
- A new `hook_token` config field + secure token delivery to the hook command.
- `GET /sessions` stamps `Session.State` (rollup), riding the existing hub→agent pull.
- `agentmon-agent hooks print|install|uninstall` (the installer/template) and
  `agentmon-agent hook-test` (the §10.3 test command).

**Out of scope (later sub-milestones):**
- M7 (hub): polling/ingesting agent state, the durable `session_state_events` write path, the
  per-principal `principal_seen` projection, `GET /api/v1/events` (SSE), `POST /api/v1/seen`, and the
  public session payload carrying state.
- M8 (web): state dots, server/session rollup dots, blocked-first sorting (sidebar + inbox).
- Attention alerts (toast/sound/push) — design §18-Q9, Phase 4.

**Non-goals / deliberate simplifications:**
- The agent holds state **in memory only**. Durable history (§9.4) is the hub's `session_state_events`
  table (M7). An agent restart resets state to `unknown` per pane until the next hook arrives.
- The agent emits only the five **global** states. The per-principal `done → idle` "seen" transition
  (§9.3) is a hub-side projection (M7); the agent has no concept of "seen".
- Fallback heuristics (§10.4, `capture-pane` scraping) are **not** built in M6. Hooks are the sole
  state source; a pane with no hook signal is `unknown`. Heuristics remain a later, secondary add.

---

## 1. Verified hook contract (the spike result — design §18-Q3 resolved)

Empirically confirmed against **Claude Code v2.1.195 / tmux 3.5a** on this host, using isolated
`claude --settings <file>` runs (never modifying `~/.claude/settings.json`) scoped to a throwaway tmux
socket. Full detail in `scratchpad/hook-spike/FINDINGS.md`.

### 1.1 settings `hooks` schema
```json
{ "hooks": { "<EventName>": [
  { "matcher": "<glob, optional>",
    "hooks": [ { "type": "command", "command": "<shell>" } ] } ] } }
```
The hook **command receives the event JSON on stdin**, runs in a shell, and **inherits the pane's
environment**. Exit 0 = allow/no-op.

### 1.2 Events that fire (this version) and their distinctive fields
Every payload carries: `session_id` (a Claude UUID — *not* the tmux session), `transcript_path`,
`cwd`, `hook_event_name`.

| Event              | Distinctive fields                                                        |
|--------------------|---------------------------------------------------------------------------|
| `SessionStart`     | `source`                                                                  |
| `UserPromptSubmit` | `prompt`, `permission_mode`                                               |
| `PreToolUse`       | `tool_name`, `tool_input{}`, `tool_use_id`, `permission_mode`, `effort{}` |
| `PostToolUse`      | + `tool_response{}`, `duration_ms`                                        |
| `Notification`     | `message`, **`notification_type`** (e.g. `"permission_prompt"`)           |
| `PermissionRequest`| `tool_name`, `tool_input{}`, `permission_mode`, `effort{}`                |
| `Stop`             | `last_assistant_message`, `stop_hook_active`, `background_tasks[]`        |
| `SubagentStop`     | (no distinctive correlation fields)                                       |
| `SessionEnd`       | `reason`                                                                  |

Design-doc guesses that do **not** exist in v2.1.195: `SubagentStart`, `StopFailure`, `PreCompact`.
`PermissionRequest` **does** exist (the design was unsure). A single permission prompt fires **both**
`PermissionRequest` and `Notification` — both map to `blocked`, which is idempotent under
store-events / derive-latest.

### 1.3 Correlation (resolves §9.5 / §18-Q3)
The payload has **no** tmux session name or pane id. But the hook command inherits:
- `TMUX_PANE` — the exact pane id (e.g. `%0`);
- `TMUX` — `"<socket-path>,<server-pid>,<session-index>"`; `basename` of field 1 = socket name
  (`agentmon`, or `default` for the default socket).

⇒ The installed hook **command** reads these two env vars and forwards them; the agent maps socket →
`config.Target` and uses the pane id directly. No `cwd` fuzzy-matching. If Claude runs outside tmux
(`$TMUX` empty), the hook is dropped (AgentMon's model is "agents in tmux").

---

## 2. Shared contract — `shared/session.go`

Add the state vocabulary and the rollup, shared verbatim by agent (M6), hub (M7), and web (M8 mirrors
it in `web/src/lib/contracts.ts`).

```go
// State is an agent session/pane state. The agent emits only these five global
// states; the per-principal done→idle "seen" projection (§9.3) is hub-side.
type State string

const (
    StateBlocked State = "blocked" // needs human input/approval — highest priority
    StateDone    State = "done"    // finished a turn (unseen by anyone yet, globally)
    StateWorking State = "working" // actively processing / running tools
    StateIdle    State = "idle"    // calm: agent present at its prompt, not working
    StateUnknown State = "unknown" // plain shell, or no hook signal yet
)

// RollUp reduces pane states to one session/server state using the §9.2 priority
// blocked > done > working > idle > unknown. Empty input → StateUnknown.
func RollUp(states ...State) State { ... }
```

And on `Session`:
```go
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
The "intentionally omitted in Phase 1" comment is removed. The agent guarantees a non-empty value by
having `SessionsHandler` stamp every session (§6) — `StateUnknown` when no hook has been seen — so the
field never serialises as `""`. (`tmux.Discover` itself leaves the zero value; the handler is the single
place that stamps it.)

**Rollup priority test matrix** (table-driven, must pass): any-blocked→blocked; else any-done→done;
else any-working→working; else any-idle→idle; else unknown; empty→unknown.

---

## 3. `agent/internal/state` — the state machine

Pure, dependency-free, mutex-guarded, in-memory. No tmux, no HTTP.

### 3.1 Types
```go
// Event is the parsed, correlated hook signal handed to the machine.
type Event struct {
    Target          string // resolved config.Target.Label (socket → target)
    Pane            string // tmux pane id, e.g. "%3"
    Name            string // hook_event_name, e.g. "PermissionRequest"
    NotificationKind string // notification_type, when Name=="Notification" (else "")
    ClaudeSessionID string // session_id (UUID) — informational
    At              time.Time
}

type paneState struct {
    State           State
    LastEvent       string
    ClaudeSessionID string
    UpdatedAt       time.Time
}

type Machine struct { /* mu sync.Mutex; panes map[key]paneState; now func() time.Time */ }

func New(now func() time.Time) *Machine        // now defaults to time.Now when nil
func (m *Machine) Apply(ev Event) (State, bool) // returns (newState, changed)
func (m *Machine) Pane(target, pane string) (State, bool)
func (m *Machine) Rollup(target string, panes []string) State // RollUp of known panes; unknown if none
```
Map key is `(target, pane)` — pane ids are unique only within a socket/server, and the agent may serve
multiple targets.

### 3.2 Mapping (event → state)
| `Name` (+ kind)                                  | New pane state              |
|--------------------------------------------------|-----------------------------|
| `SessionStart`                                   | `idle` (agent present, at prompt) |
| `UserPromptSubmit`, `PreToolUse`, `PostToolUse`  | `working`                   |
| `PermissionRequest`                              | `blocked`                   |
| `Notification`, kind contains `permission`       | `blocked`                   |
| `Notification`, any other kind (idle/waiting)    | `done`                      |
| `Stop`                                           | `done`                      |
| `SubagentStop`                                   | **preserve** (no change)    |
| `SessionEnd`                                     | `unknown` (Claude gone — pane entry is **evicted** from the map) |
| any **unknown** event name                       | **preserve** (no change)    |

On `SessionEnd` the machine **deletes** the pane's entry rather than storing an `unknown` tombstone
(bounds memory for a long-lived agent serving many short-lived panes); `Apply` still returns
`(StateUnknown, prior != StateUnknown)` and `Rollup`/`Pane` treat the pane as never-seen → `unknown`.

> **Known staleness limitation (multi-review, deferred to M7).** The map is keyed by `(target, pane)`
> only. Two residual cases are NOT handled in M6: (a) a pane whose Claude is *killed* without a clean
> `SessionEnd` leaves a lingering entry until the agent restarts; (b) after a tmux **server** restart,
> pane ids restart from `%0`, so a stale entry could roll up onto a new pane with the same id until its
> next hook (a `SessionStart`/`UserPromptSubmit` corrects a real Claude pane immediately). A
> prune-on-`/sessions`-stamp was considered but deliberately **not** added in M6: pruning against the
> live pane set introduces a TOCTOU race that could drop a *fresh* `blocked` whose hook lands between
> discovery and prune — unacceptable for the headline signal. This belongs in M7, which owns
> aggregation and already polls the live set: it can prune/epoch durably (e.g. key on a tmux
> server-pid epoch, available in `$TMUX`) without racing the intake.

`Apply` records every event (updates `LastEvent`/`UpdatedAt`) even when the state is preserved, so
"last activity" is current. `changed` is true only when the derived `State` differs from the prior
pane state. A first-ever event for a pane counts as changed (prior = implicit `unknown`).

> **M7 handoff note (from the whole-branch review).** `changed` is a *future-facing* per-pane
> transition signal — it is **not consumable over M6's pull transport**: the hub reads only the
> rolled-up `Session.State` via `GET /sessions`, and the production `/hook` handler discards `Apply`'s
> return. Two consequences M7 must design around: (a) rollup collapses rapid per-pane transitions
> between polls; (b) pull cannot distinguish a *new* `done` turn from a still-`done` session
> (`done→done`) — exactly the "unseen done" transition `principal_seen` must catch. So M7 must either
> add a per-pane transition surface to the agent (an events buffer or a `GET /state?since=<cursor>`
> endpoint that exposes `changed`/transitions), or accept lossy `done→done` detection. `changed` is
> kept in M6 (harmless, and ready for that future push/cursor path).

### 3.3 Tests (TDD, table-driven over the captured payloads)
- Each row of §3.2 from a clean machine and from each other prior state.
- `SubagentStop`/unknown-event preserve the prior state and return `changed=false`.
- `Rollup` across multiple panes uses §2 priority; unknown panes excluded; no-known-panes → unknown.
- Concurrency: parallel `Apply`/`Rollup` under `-race`.

---

## 4. `agent/internal/hooks` — `POST /hook` intake

### 4.1 The wire shape (chosen for robustness)
The installed hook command pipes Claude's stdin JSON **straight as the request body** and passes the
env-derived correlation bits as **headers** — avoiding fragile shell JSON construction:

```
POST /hook
Authorization: Bearer <hook_token>
X-AgentMon-Pane: %3                         # = $TMUX_PANE
X-AgentMon-Tmux: /tmp/tmux-0/agentmon,123,0 # = $TMUX
Content-Type: application/json
<body> = the unmodified Claude hook event JSON (from stdin)
```

### 4.2 Handler
`HookHandler(cfg, machine, now) http.HandlerFunc`, mounted as `POST /hook`, wrapped by a dedicated
`RequireHookAuth(cfg.HookToken, ...)` (constant-time compare, mirroring `RequireBearer`). Distinct
from `cfg.HubToken`.

Flow:
1. **Auth:** bad/empty token → `401`. If `cfg.HookToken == ""` the route is **not mounted** (hooks
   are opt-in; absent feature = no endpoint).
2. **Loopback guard:** reject non-loopback `RemoteAddr` (hooks only originate on `127.0.0.1`) → `403`.
   (Defence in depth; the token is the primary gate.)
3. **Correlate:** read `X-AgentMon-Pane` (must match `^%[0-9]+$`, reuse `tmux.ValidatePaneID`) and
   `X-AgentMon-Tmux`. Derive socket name = `basename(field-0 of $TMUX before the first comma)`. Map to
   a target: `t.SocketName == socket`, or `socket == "default" && t.SocketName == ""`. No match, or
   missing/empty `$TMUX` → **soft drop** (log + `204`).
4. **Parse body tolerantly:** decode only the known fields (`hook_event_name`, `notification_type`,
   `session_id`); ignore everything else; unknown/extra fields never error (§18-Q3 forward-compat). A
   malformed body → soft drop (`204`), never `5xx`.
5. **Apply:** build `state.Event`, call `machine.Apply`. Always respond **`204 No Content`** on the
   happy path (and on soft drops) so a hook never breaks or stalls Claude. Only auth/loopback failures
   return non-2xx.

### 4.3 Tests (httptest)
- Valid PermissionRequest → `204`, machine shows `blocked` for (target, pane).
- Wrong/empty token → `401`; non-loopback remote → `403`.
- Unknown socket / missing `$TMUX` / bad pane id / malformed JSON → `204`, machine unchanged.
- Extra/unknown payload fields and unknown event names tolerated.
- `hook_token` unset → route absent (`404`).

---

## 5. Config + token delivery — `agent/internal/config`

Add two fields:
```go
HookToken     string `toml:"hook_token"`      // secret; enables /hook when set; env:VAR-resolvable
HookTokenFile string `toml:"hook_token_file"` // optional path the agent writes the token to (0600)
```
`HookToken` joins the `ResolveSecretRef` loop. Both are optional — when `HookToken` is empty, the hook
feature is fully off (no `/hook` route, `hooks` subcommands warn).

**Token delivery to the hook command:**
- If `HookTokenFile` is set, the agent writes `HookToken` to it (`0600`, parent dir created) on
  startup, and `hooks print/install` emit a command that reads `$(cat <HookTokenFile>)` — keeping the
  secret **out of `settings.json`**. This is the recommended posture and what the docs show.
- If `HookTokenFile` is empty, `hooks print/install` bake the literal token into the command (a
  one-line setup). In that mode `hooks print/install` emit a **stderr warning** that the token lands
  in the settings file and that `hook_token_file` keeps it out. `hook-test` and the handler work
  either way.

**Security caveats (from the whole-branch review).**
- The hook token appears in curl's argv, so it is visible in the process table (`ps`/`/proc`) to other
  local users **regardless of file-vs-literal delivery** (`$(cat file)` expands before exec). File
  delivery's only benefit is keeping the secret out of `settings.json` — not hiding it from local
  users. Acceptable on the single-tenant LAN host (a leaked hook token only lets a *loopback* caller
  set in-memory state — no RCE/exfil), but documented so operators don't over-assume.
- The resolved token is interpolated into a double-quoted shell string unescaped; a token containing
  shell metacharacters (`"`, `$`, `` ` ``, `\`) would break the command. `hook_token_file` is the
  recommended posture and sidesteps this.
- The installed command targets `http://127.0.0.1:<port>/hook` (loopback only). If `listen` binds a
  concrete non-loopback, non-wildcard address, hooks silently no-op (`curl … || true`); `hooks
  print/install` emit a **stderr warning** for that case.

---

## 6. `GET /sessions` stamps `State`

`SessionsHandler` gains the `*state.Machine` (injected, like `Discoverer`). After discovery, for each
`Session` it collects that session's pane ids from the tree and sets
`Session.State = machine.Rollup(target, paneIDs)`. No `ResolvePaneSession` needed — the tree already
maps session → panes. When the machine is nil/empty every session is `unknown` (the discovery default).

This rides the **existing hub→agent pull**: once M7/M8 land, state flows hub→browser with no new
transport. Tests assert a discovered tree + a primed machine yields the right per-session rollup.

---

## 7. CLI — `agent/cmd/agentmon-agent`

A `hooks` subcommand group and `hook-test`, parsed before the existing daemon flow (if `os.Args[1]`
is `hooks` or `hook-test`, dispatch and exit; otherwise run the server as today).

- **`hooks print [--config <path>]`** — prints the `claude-hooks.json` snippet (the `{"hooks":{...}}`
  block) to stdout. No writes. The default, safe path. Uses config for the listen port and token
  mode. Registers a command for each event in §1.2 pointing at `POST http://127.0.0.1:<port>/hook`
  with the §4.1 headers; reads the token per §5.
- **`hooks install --settings <PATH> [--config <path>]`** — idempotent merge of that snippet into the
  JSON file at `<PATH>`. The path is **required** — there is no implicit `~/.claude` default, so no run
  can accidentally touch a live settings file. The merged block is tagged (an AgentMon marker) so
  re-running replaces only our block and never clobbers other hooks. Creates the file if absent.
- **`hooks uninstall --settings <PATH>`** — removes only the AgentMon-tagged block.
- **`hook-test --pane %N --event <Name> [--config <path>] [--notification-kind <k>]`** — synthesises a
  hook POST to the local agent (the §4.1 shape, reading `$TMUX`/`$TMUX_PANE` from its own env when
  present, or `--pane` override) to verify the wiring end-to-end. Prints the resulting HTTP status.

**Merge mechanism:** parse the target file as generic JSON (`map[string]any`), splice our tagged
`hooks` entries under `hooks.<Event>`, re-encode. The AgentMon marker is a sentinel inside each
injected hook entry so uninstall can find and drop exactly our commands without disturbing the user's.

### 7.1 Tests
- `print` emits valid JSON with one command per §1.2 event and the correct port/headers/token mode.
- `install` into a `t.TempDir()` file: creates it; second run is idempotent (no duplication); an
  existing unrelated hook in the file survives; `uninstall` removes only our block. **No test ever
  references `~/.claude`.**

---

## 8. Wiring — `agent/cmd/agentmon-agent/main.go`

- Construct `machine := state.New(nil)`.
- Inject `machine` into `SessionsHandler`.
- If `cfg.HookToken != ""`: write the token file (if `HookTokenFile` set) and mount
  `mux.Handle("POST /hook", api.RequireHookAuth(cfg.HookToken, hooks.HookHandler(cfg, machine, nil)))`.
- Existing `/healthz`, `/sessions`, `/panes/{paneId}/io` unchanged except `/sessions` now carries
  state.

---

## 9. Safety (memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]])

This host runs the production hub **and** Claude Code's own tmux on the **default** socket (session
`0` = the running agent). Every M6 test and any manual verification MUST:
- never write `~/.claude/settings.json` (installer tests use `t.TempDir()` only; the CLI has no
  implicit `~/.claude` target);
- never drive or install hooks into Claude's own default-socket session — any live hook experiment is
  scoped to a **throwaway tmux socket** (as the §1 spike was), or a temp `--settings` file;
- for any agent run, build to scratch and bind a loopback port; never touch `deploy/data` or the live
  container.

The §1 spike already followed this and left session `0` + the `agentmon` demo panes intact.

---

## 10. Acceptance (M6 done when)

1. `shared.Session` carries `State`; `RollUp` + machine pass their table-driven tests under `-race`.
2. `POST /hook` (token + loopback gated) turns each real captured payload into the right state; soft
   drops never `5xx`; never breaks Claude.
3. `GET /sessions` reflects rolled-up state from prior hooks.
4. `hooks print/install/uninstall` produce a correct, idempotent, reviewable settings block with **no**
   implicit `~/.claude` path; `hook-test` exercises the loop.
5. Full agent Go suite green (`go test ./... -race`), `go vet` clean, hub/web untouched.
6. `/multi-review --codex` run; everything but nitpicks fixed (per the kickoff).
7. Manual smoke (optional, safe): a throwaway-socket Claude with `hooks install --settings <tmp>`
   drives `/hook` on a scratch agent; `GET /sessions` shows `working`/`blocked`/`done` transitions.

---

## 11. Carried minors (m5-carryover)

The carried items are web/Phase-4/5 (xterm lazy-load, relay concurrency cap, mobile next/prev,
nitpicks). None are in agent Go code, so **none are folded into M6**; they belong to M7/M8 where that
code is touched.
