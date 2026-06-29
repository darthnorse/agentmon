# M7 — Hub state aggregation (Phase 3b)

**Status:** approved design, pre-implementation.
**Date:** 2026-06-29.
**Phase:** 3 (Claude Code state via hooks), sub-milestone **b** of 3 (M6 agent → **M7 hub** → M8 web).
**Builds on:** M1–M6 (multi-server terminal spine + web SPA + agent-side state via hooks), all merged & live-accepted.
**Authoritative sources:** `agentmon-design.md` §8.2 (public API), §8.3 (WS control frames), §9 (state model),
§10.4 (fallback heuristics), §14 (frontend UX), §17 (Phase 3 deliverables), §18-Q9 (attention alerts — Phase 4);
`docs/superpowers/m6-carryover.md` (the M6→M7 handoff); `docs/superpowers/specs/2026-06-29-agentmon-m6-agent-state-design.md`
(§3.2 M7 handoff note + the known-staleness box).
**Branch:** `phase-3-m7-hub-state`, off `main@cb3bda9`.

---

## 0. Scope

M7 delivers the **hub half** of Phase 3: turn the agent-side state M6 exposes into the supervision data
plane. The hub learns state changes proactively, stores state **events**, derives current state, projects
per-principal **seen**, rolls state up to session/server dots, and serves it to clients (public payload +
SSE + the terminal-WS state frame). M8 (web) then renders dots, blocked-first sorting, and the inbox on top.

**In scope (M7):**
- **Agent (additive):** a `GET /state` internal endpoint exposing per-pane `{state, transitionSeq, doneSeq,
  epoch, claudeSessionID, lastChangeAt}`, fed by the existing M6 state machine. The machine gains the
  transition/done counters and captures the `$TMUX` server-pid epoch. (Folded into M7, not a separate M6.1.)
- **Hub poller:** a background goroutine that polls each active server's `GET /state` (default ~3s),
  detects transitions, and ingests them.
- **State event storage:** writes `session_state_events`; derives current state from events (don't only
  store latest — §9.4).
- **Aggregation + rollup dots:** session and server rollups (§9.2 priority) surfaced in the public payload.
- **Per-principal seen:** `principal_seen` projection (`done → idle` once *this* principal focuses — §9.3)
  + `POST /api/v1/seen`.
- **Delivery:** `GET /api/v1/events` (SSE, snapshot + deltas + reconnect) **and** the `{t:"state",…}` control
  frame on the existing terminal WS (§8.3), both fed by one in-process broadcaster, both seen-projected.
- **Epoch / prune:** use the tmux server-pid epoch to supersede stale (pane-id-reused) state; drop vanished
  panes via the live-tree overlay (the deferred M6 prune item, codex C4).

**Out of scope (later):**
- M8 (web): the dots/sorting/inbox UI, blocked-first ordering, `contracts.ts` consumption. M7 ships only the
  additive payload/SSE/WS *shapes* M8 will consume.
- Phase 4: toast/sound/Web-Push **alerts** (§18-Q9). M7 lays the data path only.
- Fallback heuristics (§10.4): not built (consistent with M6 — hooks are the sole source).

**Non-goals / deliberate simplifications:**
- The hub's in-memory current-state projection is **derived**, not authoritative storage; the durable record
  is `session_state_events`. After a hub restart the projection is empty until the next poll (≤ one interval)
  repopulates it — acceptable, and the event history is intact.
- No new audit events: state ingest is too chatty to audit; `seen` is high-frequency UX state, not
  security-relevant (§13.5 does not list it).
- `done → done` re-alert is made **correct** (via `doneSeq`), but transient `working` flicker between polls
  may still collapse — those are non-sticky and irrelevant to seen/alerts (see §3).

---

## 1. The KEY DECISION — agent→hub state transport (resolved: option B)

M6 is **pull-only**: the hub reads the rolled-up `Session.State` from the agent's `GET /sessions` only when a
browser asks. Two problems the whole-branch review surfaced (M6 carryover, M6 spec §3.2):

1. The proactive deliverables (SSE, future alerts) need the hub to learn changes **without** a browser asking
   → a hub-side poller (or agent push) is required in every option except the status quo.
2. A snapshot pull is **lossy for `done → done`**: a *new* finished turn while the session was already `done`
   is invisible to a snapshot diff — yet that is exactly the "unseen done" transition `principal_seen` must
   catch. The machine's `changed` flag does not help (`done → done` is `changed=false`).

**Decision: option B — enriched snapshot + per-pane transition counter.** The agent exposes per-pane state
**plus counters** (`transitionSeq`, `doneSeq`) and the `$TMUX` epoch on a pulled `GET /state`; the hub poller
stores an event whenever a pane's state changes **or** `doneSeq` increments **or** the epoch changes.

Why B over the alternatives:
- **vs A (poll current `/sessions` snapshot, no agent change):** A is lossy exactly at the headline signal
  (the unseen-done re-alert). B fixes it for the cost of two integer counters the machine already has the data
  for.
- **vs C (agent event buffer + `GET /state?since=<cursor>`):** C is the most faithful to §9.4 but adds cursor
  management + agent-restart/boot-id detection + ring-buffer eviction. B catches the same new-done with a
  plain `doneSeq >` check and no cursor/replay/boot-id machinery, and stores **transitions** (not every chatty
  `PreToolUse`), which is what should actually live in `session_state_events`.
- **vs D (agent→hub push):** lowest latency but a new outbound trust direction (agent needs hub creds/address);
  M6 rejected it and alerts are Phase 4, so sub-second latency buys nothing yet.

**Sticky-state rationale (why polling is sufficient).** `blocked` and `done` are *sticky* — they persist until
the human/agent acts — so a 3s poll reliably catches single entries into them. The one important non-sticky
case is a *new* `done` landing on an existing `done`; `doneSeq` makes that observable. Transient `working`
flicker can collapse between polls, but it is irrelevant to seen/alerts.

**Graceful degradation.** `GET /state` is purely additive. An un-upgraded agent returns `404`; the hub then
falls back to snapshot-diffing that server's `GET /sessions` (option-A behavior: state changes detected, but
`done → done` lossy for that server). New-hub/old-agent and old-hub/new-agent both work, so rollout order is
free in either direction. **M7 does require an agent redeploy** to get the non-lossy path (M6 set that
precedent).

---

## 2. Agent additions — `agent/internal/state` + `agent/internal/api`

### 2.1 Machine counters + epoch (`state.go`)

`paneState` gains:
```go
type paneState struct {
    State           shared.State
    LastEvent       string
    ClaudeSessionID string
    Epoch           string    // $TMUX server-pid field, e.g. "12345"; "" if unknown
    TransitionSeq   uint64    // bumped on every state CHANGE for this pane
    DoneSeq         uint64    // bumped each time the pane ENTERS done (incl. done→done)
    UpdatedAt       time.Time
    ChangedAt       time.Time // time of the last state change (for lastChangeAt)
}
```
`Event` gains `Epoch string` (parsed from `X-AgentMon-Tmux` field 2 by the hook handler; see §2.3).

`Apply` semantics (extends M6; counters are monotonic per live pane entry):
- Compute `next` and `changed` as today.
- `TransitionSeq`: increment iff `changed` (state differs from prior).
- `DoneSeq`: increment iff `next == done` **and** the event is a done-*producing* event (`Stop`, or
  `Notification` non-permission) — i.e. count each finished **turn**, including a `done → done` re-finish.
  A `SubagentStop`/unknown event that merely *preserves* `done` does **not** bump `DoneSeq`.
- `ChangedAt`: set to the event time when `changed`.
- `Epoch`: store the event's epoch (last-writer-wins; a changed epoch is a new tmux server — see §6).
- `SessionEnd` still deletes the pane entry (counters die with it; a fresh entry restarts at 0 — the hub
  treats a counter reset / missing pane as "new", see §3.3).

New read method for the endpoint:
```go
// Snapshot returns a copy of every known pane's record for a target (or all
// targets when target==""). Stable, sorted by (target, pane) for test determinism.
func (m *Machine) Snapshot(target string) []PaneSnapshot
```
where `PaneSnapshot` carries `{Target, Pane, State, TransitionSeq, DoneSeq, Epoch, ClaudeSessionID,
LastChangeAt}`. `Pane`/`Rollup` are unchanged (still back `GET /sessions`).

### 2.2 `GET /state` endpoint (`agent/internal/api/state.go`)

```text
GET /state?target=<label>        # target optional; empty → all configured targets
Authorization: Bearer <agent-token>     # same RequireBearer as /sessions, NOT the hook token
→ 200 { "epoch": "<current $TMUX pid or '' >", "panes": [ PaneSnapshot, … ] }
```
- Auth: the existing hub→agent `RequireBearer` (the `srv.Bearer` path), exactly like `/sessions`/`/healthz`.
  The hook token is **not** involved (that gates the inbound `/hook` only).
- Mounted **only when the state machine is present** (i.e. whenever the agent runs; the machine is always
  constructed). Returns an empty `panes` array when no hooks have been seen.
- Tolerant `target` resolution mirroring `/sessions` (`ResolveTarget`); unknown target → `404`.
- This is an **internal agent↔hub** surface (design §12.2), not the public payload — it never reaches the
  browser directly.

### 2.3 Hook handler captures epoch (`agent/internal/hooks`)

`HookHandler` already parses `X-AgentMon-Tmux` (`<socket>,<server-pid>,<idx>`) to derive the socket. Extend
it to also extract field 2 (the server pid) as `Event.Epoch`. Non-fatal if absent (epoch `""`). No other
behavior change; `/hook` still soft-drops and returns `204` on any problem.

### 2.4 Agent wiring + tests

- `main.go`: mount `GET /state` behind `RequireBearer` alongside `/sessions`.
- Tests (TDD, `-race`): counter rules table-driven (change→`TransitionSeq++`; each done-producing event →
  `DoneSeq++` incl. `done→done`; preserve events bump neither); epoch capture; `Snapshot` determinism;
  `/state` httptest (auth required, target resolution, empty machine → `{panes:[]}`); concurrency.

---

## 3. Hub poller + ingest — `hubd/internal/state` (new package)

### 3.1 Poller

A single goroutine owned by hub `main`:
```go
type Poller struct { /* reg, agentClient, store, broadcaster, interval, now */ }
func (p *Poller) Run(ctx context.Context)   // ticks until ctx done
```
- Each tick: `reg.List(active)` → for each server, `GET /state` (bounded concurrency, e.g. 4) with a short
  per-poll timeout.
- **Backoff:** per-server exponential backoff on dial/HTTP failure (base = interval, cap ~30s) so a dead
  agent is not hammered; reset on success. `TouchLastSeen` on success.
- **Degraded fallback:** on `404` for `/state`, fall back to that server's `GET /sessions` and diff the
  rolled-up `Session.State` per session (option-A; no `doneSeq`). Logged once per server when first degraded.
- Default interval 3s (config-overridable, see §8); injected `now` for tests; a manual `Tick(ctx)` method for
  deterministic tests (no real sleeping).

### 3.2 Change detection → events

The poller holds the last-seen per-pane record keyed `(serverID, target, pane)`. For each polled pane it
writes a `session_state_events` row when **any** of:
- `state` differs from the last-seen state, **or**
- `doneSeq` increased (a new finished turn — even `done → done`), **or**
- `epoch` differs from the last-seen epoch (tmux server restart — §6).

A pane seen for the first time writes an event (prior = implicit unknown). The row:
| column | value |
|---|---|
| `id` | uuid |
| `server_id` | registry id (FK-valid, enforced) |
| `target_id` | the target label ("" / "default") |
| `tmux_session_name` | the session that owns the pane (from the live tree — see §3.4) |
| `tmux_pane_id` | the pane id |
| `source` | `"hook"` (poll-observed hook-derived) / `"snapshot"` (degraded fallback) |
| `raw_event` | compact JSON of the polled `PaneSnapshot` (debug/history) |
| `derived_state` | the state string |
| `payload` | reserved (NULL for now) |
| `event_ts` | `lastChangeAt` from the agent (the transition time) |
| `received_at` | hub clock at ingest |

### 3.3 Current-state projection (in memory)

The poller maintains `current map[sessionKey]sessionState`, where `sessionKey = (serverID, target,
sessionName)` and `sessionState` holds the rolled-up state + the latest event id/ts (for seen comparison) +
per-pane states. Updated on every poll. On a counter **reset** (a pane's `doneSeq`/`transitionSeq` goes
backwards, i.e. agent restarted) the poller drops its last-seen for that pane and re-ingests as new. This
projection backs both the public-payload overlay (§4) and the broadcaster (§5).

### 3.4 Pane → session mapping

`session_state_events` needs `tmux_session_name`, and rollups are per session, but the agent's `/state` is
per **pane**. The poller resolves pane→session from the live tree. To avoid a second agent round-trip per
tick, the poller pulls `GET /sessions` once per server per tick **alongside** `/state` (both are cheap GETs;
they already run on the same dial path) and builds the pane→session map; panes absent from the live tree are
dropped (natural ghost prune, §6). (If this proves too chatty at scale it can be cached, but at single-host /
handful-of-servers scale a paired GET every 3s is negligible.)

### 3.5 Storage layer — `hubd/internal/db/state.go`

Raw SQL via `*sql.DB`, copying `db/audit.go`/`db/servers.go` shape:
```go
func (d *DB) AppendStateEvent(ctx, e StateEvent) error
func (d *DB) LatestSessionEvent(ctx, serverID, target, session string) (StateEvent, error) // for seen anchor
func (d *DB) UpsertSeen(ctx, s PrincipalSeen) error
func (d *DB) GetSeen(ctx, principalID, serverID, target, session string) (PrincipalSeen, bool, error)
func (d *DB) ListSeenForPrincipal(ctx, principalID string) ([]PrincipalSeen, error) // SSE snapshot projection
```
`StateEvent`/`PrincipalSeen` structs mirror the table columns. Uses the existing
`idx_state_events_session(server_id, tmux_session_name, event_ts)`.

### 3.6 Tests
Fake agent (httptest) driving the poller: change detection incl. `done→done` via `doneSeq`; epoch supersede;
first-seen; agent-down backoff; `/state`-404 degraded fallback; pane→session mapping + vanished-pane drop;
`AppendStateEvent`/`LatestSessionEvent` round-trip against a temp sqlite (FK enforced); `Tick` determinism
with injected `now`; `-race` on the projection.

---

## 4. Rollup & public payload — `hubd/internal/api`

### 4.1 Rollup dots
`shared.RollUp` (exists) reduces pane→session and session→server using the §9.2 priority. The hub derives:
- **session** rollup = `RollUp` of the session's pane states (from the projection), then the **seen
  projection** applied for the requesting principal (§5).
- **server** rollup = `RollUp` of that server's session states (post-seen-projection, so a server whose only
  `done` was seen reads calmer).

### 4.2 Session payload overlay
`ServerSessionsHandler` / `SessionDetailHandler`: keep pulling the live tree on demand (windows/panes/cwd/
command — fresh), but set each `Session.State` from the hub projection **with the requesting principal's seen
projection applied**, instead of trusting the agent's inline rollup. Sessions/panes absent from the live tree
simply have no projection overlay (and stale state for vanished sessions never appears — §6). When the
projection has nothing yet for a session (e.g. just after hub start, before first poll), fall back to the
agent's inline `Session.State` so the payload is never worse than M6.

### 4.3 Server payload rollup
`ServersHandler` / `ServerHandler`: add a `state` field to the server summary/detail (the server rollup,
§4.1), so M8's sidebar can render server dots without extra calls. Additive JSON field.

### 4.4 Tests
Handler tests with a fake registry + fake projection: session state reflects projection + seen; server
rollup correct; pre-poll fallback to inline state; vanished session omitted. Contract shape asserted for M8.

---

## 5. Seen (`done → idle` per principal) — `hubd/internal/api` + `db`

### 5.1 `POST /api/v1/seen`
```text
POST /api/v1/seen   (RequireAuth → CSRF enforced; authorize SessionView)
body: { "serverId": "...", "target": "default", "sessionName": "api-refactor" }
→ 204
```
- Authorize `SessionView` on `shared.SessionID(serverId, target, sessionName)` via `authorizeOr403`.
- Upsert `principal_seen` keyed on `principal.ID + serverId + target + sessionName` (the PK, with `target_id`
  holding the target label, default `""`), stamping `last_seen_event_id` = id of the **latest stored event**
  for that session at call time (via `LatestSessionEvent`; `NULL`/empty if none yet) and `last_focused_at =
  now`.
- Unknown server → still record seen (durable key is server/target/session; the session need not currently
  exist). Validate inputs are non-empty + sane (reuse session-name sanity where applicable).

### 5.2 Projection helper
```go
// SeenProject returns the state shown to principal p: done→idle iff p has looked
// at/after the session's latest done. blocked/working/idle/unknown pass through.
// latestDoneReceivedAt is the hub-clock received_at of the session's most recent
// stored event (only consulted when global==done — at which point that event IS a
// done event). seen/ok come from principal_seen for (p, server, target, session).
func SeenProject(global shared.State, latestDoneReceivedAt string, seen PrincipalSeen, ok bool) shared.State
```
Rule: when `global == done`, return `idle` iff `ok && seen.LastFocusedAt >= latestDoneReceivedAt` (the
principal focused at or after the latest finish); otherwise return `done`. All other states pass through —
`blocked` is **never** masked (you clear it by acting, which emits `working`). A principal with no seen record
always sees `done`. After a *new* finished turn, the fresh event's `received_at` exceeds the old
`last_focused_at`, so the session correctly re-reads `done` (the re-alert).

> **Decision (ambiguity resolved — single clock).** The comparison uses **hub-clock** timestamps only:
> `received_at` (stamped by the hub at ingest) vs `last_focused_at` (stamped by the hub at `POST /seen`). It
> deliberately does **not** compare against the agent-supplied `event_ts` (a different host's clock — skew
> would corrupt the projection), and not by uuid (event ids are unordered). `last_seen_event_id` is still
> recorded in `principal_seen` for audit/debug ("which event did they ack"), but the projection ignores it.

### 5.3 Tests
`SeenProject` table: done+unseen→done; done+seen→idle; blocked/working pass through; no-seen-record→done;
seen-then-new-done→done again (the re-alert). `POST /seen` httptest: upsert + re-focus updates anchor; CSRF
required; authorize path; bad body → 400.

---

## 6. Epoch / prune (deferred M6 item, codex C4)

- Every event/snapshot carries the tmux **server-pid epoch** (`$TMUX` field 2). When a pane's polled epoch
  differs from the last-seen epoch, the poller treats it as a **new tmux server** (pane ids restart at `%0`):
  it supersedes the prior-epoch last-seen for that pane and writes an epoch-change event, so a stale `%0`
  cannot roll up onto a freshly-reused pane id.
- **Vanished panes/sessions** drop out of the public payload via the live-tree overlay (§4.2): the hub only
  overlays state onto panes/sessions present in the current discovery. The projection also prunes pane keys
  not seen in the latest poll for a server.
- **Residual known-minor (same as M6):** a hard-killed Claude whose tmux pane *survives* without a clean
  `SessionEnd` keeps its last state until its next hook or until the pane itself goes away. Documented, not
  solved (a real Claude pane self-corrects on its next `SessionStart`/`UserPromptSubmit`).

---

## 7. Delivery — SSE + terminal-WS state frame

### 7.1 Broadcaster — `hubd/internal/state/broadcaster.go`
One in-process pub/sub:
```go
type Change struct { ServerID, Target, Session string; Global shared.State; LatestEventID, EventTs string }
type Broadcaster struct { /* mu; subs map[id]chan Change */ }
func (b *Broadcaster) Subscribe() (id uint64, ch <-chan Change, cancel func())
func (b *Broadcaster) Publish(c Change)   // non-blocking; drop-oldest per slow sub
```
The poller calls `Publish` whenever a session's rolled-up **global** state changes (or a new done lands).
Subscribers (SSE, WS) apply the per-principal seen projection themselves. Per-subscriber buffered channel
(e.g. 64) with drop-oldest semantics so one slow client can't stall the poller; on drop, the next snapshot
reconciles (state is idempotent current-state).

### 7.2 `GET /api/v1/events` (SSE) — `hubd/internal/api/events.go`
- `RequireAuth`; resolve principal. `Content-Type: text/event-stream`, no buffering, flush per write.
- **On connect:** send one `event: snapshot` with the full current state of every visible session, each
  seen-projected for this principal (built from the projection + `ListSeenForPrincipal`).
- **Then stream** `event: state` deltas from a `Subscribe()` channel, each `data:` a JSON
  `{server, target, session, state}` (seen-projected). 
- **Heartbeat:** a `:` comment every ~25s (configurable) to keep the stream alive through Caddy/proxies.
- **Teardown:** on client disconnect (`r.Context().Done()`) call `cancel()` and return; no leaked goroutine.
- **Reconnect:** the browser's `EventSource` auto-reconnects; each reconnect re-runs the snapshot then
  resumes. No `Last-Event-ID` replay needed (current-state is idempotent; a fresh snapshot is authoritative).
- Origin: SSE is a same-origin `GET` carrying the session cookie; rely on `RequireAuth` (no CSRF on GET).

### 7.3 Terminal-WS `{t:"state",…}` frame — `hubd/internal/api/ws.go` refactor
The M4 relay is a transparent dual byte-copy (each direction's goroutine is the sole writer to one conn).
gorilla allows only **one** concurrent `WriteMessage` per conn, so injecting hub-originated state frames
requires serializing all browser-bound writes through a single writer:
- Refactor `relayPanes` so the **browser-writer** is one goroutine consuming from a channel fed by (a) frames
  read from the agent and (b) `Change`s from `Subscribe()` filtered to this pane's session, rendered as
  `{"t":"state","state":"<seen-projected>","session":"<name>"}` JSON **text** frames. Terminal data stays
  **binary**; control stays JSON text (consistent with resolved §18-Q5). The browser→agent direction is
  unchanged.
- Resolve the pane→session name **once** at relay open (one `GET /sessions` the handler already can do, or
  reuse the projection). Filter `Change`s to `(serverID, target, session)`.
- Seen projection uses the relay's principal (`p.ID`). (The focused user has by definition seen the session
  they are viewing — §14.1 tab-aware suppression — so a `done` here typically projects to `idle`; the frame
  still carries `blocked`/`working` transitions live.)
- Preserve all M4 liveness invariants: ping/pong, read deadlines, read limits, both-side teardown so no
  goroutine/subprocess leaks. The single-writer change must keep `WriteControl` (pings/pongs) safe alongside
  the muxed `WriteMessage`.

### 7.4 Tests
Broadcaster: subscribe/publish/cancel; drop-oldest under a slow sub; `-race`. SSE: snapshot-then-delta
ordering; heartbeat; disconnect teardown (no leak). WS: a primed projection change reaches the browser as a
`{t:"state"}` text frame interleaved with binary terminal frames; liveness preserved; teardown on either side.

---

## 8. Config + wiring

- `hubd/internal/config`: add `StatePollInterval time.Duration` (default 3s) and `SSEHeartbeat time.Duration`
  (default 25s), both optional with defaults (mirroring the existing optional-with-default fields). Reuse the
  existing config plumbing/tests.
- `main.go`: construct `db`-backed `state.Store`, the `Broadcaster`, and the `Poller`; start
  `go poller.Run(ctx)` with a cancelable context tied to process shutdown (add minimal `signal.NotifyContext`
  + `srv.Shutdown` lifecycle the hub lacks today, so the poller stops cleanly). Inject the projection +
  broadcaster + seen store into `api.Deps`. Register `GET /api/v1/events` and `POST /api/v1/seen` in the
  router.
- `registry.Client`: add `State(ctx, srv, target) (StateResponse, error)` mirroring `Sessions` (bearer dial,
  decode `{epoch,panes}`); `404` surfaced distinctly so the poller can choose the degraded path.

---

## 9. Acceptance (M7 done when)

1. Agent `GET /state` returns per-pane `{state,transitionSeq,doneSeq,epoch,…}`; counter rules pass table tests
   under `-race`; epoch captured from `$TMUX`.
2. The hub poller ingests transitions into `session_state_events` — including `done → done` via `doneSeq` —
   with backoff when an agent is down and a `/state`-404 degraded fallback; FK-valid `server_id`.
3. Current state is **derived** from events; session & server **rollup dots** appear in the public payload,
   seen-projected per principal.
4. `POST /api/v1/seen` records `principal_seen`; `done` reads `idle` for a principal who has looked, `done`
   again after a new finished turn; `blocked` never masked.
5. `GET /api/v1/events` streams a snapshot then seen-projected deltas with heartbeats and clean
   reconnect/teardown; the terminal WS also emits `{t:"state",…}` for its session.
6. Full Go suite green (`shared` + `agent` + `hubd`, `-race`), `go vet` clean, `CGO_ENABLED=0` build OK.
7. `/multi-review --codex` run; everything but nitpicks fixed (per the kickoff).
8. SAFE live acceptance: a loopback hub on a **copy** of `deploy/data` + a throwaway-socket Claude drives the
   full path (hook → agent `/state` → poller → events → payload/SSE/WS) showing the blocked/done/working dots
   and the seen transition — prod DB, container, session 0, and demo panes untouched.

---

## 10. SDD phasing (one spec / one branch)

**Phase A — ingest & aggregate (no per-principal view yet):**
A1 machine counters + epoch + `Snapshot` (+ hook epoch capture). A2 agent `GET /state` + wiring.
A3 `registry.Client.State`. A4 `db/state.go` storage. A5 the poller (change detection, backoff, degraded
fallback, pane→session, projection). A6 rollup dots + public-payload overlay (server + session) with pre-poll
fallback. A7 hub `main` lifecycle wiring (poller goroutine + graceful shutdown).

**Phase B — per-principal view & delivery:**
B1 `db` seen layer + `SeenProject`. B2 `POST /api/v1/seen`. B3 apply seen projection to the payload overlay.
B4 broadcaster. B5 `GET /api/v1/events` SSE. B6 terminal-WS `{t:"state"}` relay refactor. B7 config fields.

Each task: TDD (test first), per-task spec + quality review (subagent-driven-development), then opus
whole-branch review → `/multi-review --codex` (fix all but nitpicks) → safe live acceptance → finish/merge
with `docs/superpowers/m7-carryover.md` for M8. Update the milestone memory when done.

---

## 11. Safety (memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]])

This host runs the **production** hub (docker compose, behind Caddy at https://agentmon.runald.net, container
`agentmon-agentmon-hub-1`, prod config+DB in gitignored `deploy/data/`) **and** Claude Code's own tmux on the
**default** socket (session `0` = the running agent). Every test and any live verification MUST:
- build the hub to scratch and run on a **loopback** port against a **copy** of `deploy/data` (real DB +
  container untouched); set patrik's password on the **copy** (the prod value is unknown — only a hash is
  stored);
- scope any agent/Claude experiment to a **throwaway** tmux socket + temp `--settings` — never session 0,
  `~/.claude/settings.json`, the `agentmon` demo panes, or prod `deploy/data`;
- never redeploy prod or touch `deploy/data` during development; a real redeploy is `docker compose up -d
  --build` from the repo root, and ONLY when the product owner says so.
