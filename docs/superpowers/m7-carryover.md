# M7 → next carry-over (hub state aggregation — Phase 3b)

M7 (the **hub half** of Phase 3 — turn agent-side Claude state into the supervision data plane) is complete,
reviewed (per-task + opus whole-branch + `/multi-review --codex`), **live-accepted on this host**, and ready
to merge. M7 is the second of three Phase-3 sub-milestones: **M6 agent → M7 hub → M8 web**. This captures the
contract, decisions, deferrals, and the M8 handoff.

Branch: `phase-3-m7-hub-state`, off `main@cb3bda9`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m7-hub-state-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m7-hub-state.md`. Ledger: `.superpowers/sdd/progress.md`.

## The KEY DECISION (resolved with owner): transport = option B
Agent→hub is still **pull** (no agent→hub push), but the agent now exposes a richer pull surface:
- The M6 state machine gained per-pane **counters** — `transitionSeq` (bumped on every state change) and
  `doneSeq` (bumped on every entry into `done`, including `done→done`) — plus the **`$TMUX` epoch**.
- New additive agent endpoint **`GET /state`** (agent-bearer auth, like `/sessions`) returns
  `shared.AgentState{Panes []shared.PaneState}`.
- The hub **poller** ingests transitions exactly once: it writes a `session_state_events` row when a pane's
  state changes **or** `doneSeq` increments (the new-finished-turn re-alert, the lossy-`done→done` case M6
  flagged) **or** the epoch changes. This made `principal_seen` (§9.3) and the SSE/alerts data path correct
  rather than best-effort. The agent change was folded into M7 (not a separate M6.1).
- **Graceful degradation:** an un-upgraded agent (no `/state`, 404) makes the poller fall back to
  snapshot-diffing `GET /sessions` (`source="snapshot"`); rollout order is free in either direction. **M7
  requires an agent redeploy** to get the non-lossy path.

## What shipped (15 SDD tasks, TDD throughout)
Agent: `shared.PaneState`/`AgentState`; `state.Machine` counters/epoch/`Snapshot`; hook epoch capture;
`GET /state`. Hub: `registry.Client.State` (+ `ErrStateUnsupported`); `db.StateEvent` storage
(`AppendStateEvent`/`LatestSessionEvent`) + `db.PrincipalSeen` storage; in-memory `state.Projection`; the
`state.Poller` (ingest → events + projection; per-server exponential backoff; `/state`-404 degrade;
ghost-pane prune); public payload **server + session rollup dots** (overlaid from the projection, seen-
projected per principal); `state.SeenProject` (`done→idle`); `POST /api/v1/seen`; the in-process
`state.Broadcaster`; `GET /api/v1/events` (SSE: snapshot + seen-projected deltas + heartbeat + clean
teardown); the terminal-WS `{t:"state"}` control frame (single browser-writer relay refactor); hub `main`
lifecycle (poller goroutine + graceful shutdown) + config (`state_poll_interval` 3s, `sse_heartbeat` 25s);
migration `0003_state_received_index.sql`.

## Locked decisions / invariants (M8 must respect)
- **Canonical session "target" key = the agent-reported per-session label** (`shared.Session.Target`, e.g.
  `"default"`). Everything keys on it end-to-end: poller `Change.Target` / event `target_id` / projection /
  payload overlay / `POST /seen` body. M8's `POST /api/v1/seen` body MUST send the session's own `target`
  (from the payload), not a hardcoded value.
- **Single hub clock for "seen".** `received_at` (events) and `last_focused_at` (`principal_seen`) are both
  hub-stamped via one formatter (`state.HubTS`, `"2006-01-02 15:04:05.000"`); `SeenProject` string-compares
  them. The agent's `event_ts` is never used for the projection.
- **`done` is the only maskable state.** A focused session reads `idle` instead of `done`; `blocked`/
  `working` are never masked (you clear `blocked` by acting, which emits `working`). A *new* finished turn
  after focus re-surfaces as `done` (the re-alert).
- **`source`** column: `"poll"` for the normal hub-polled path, `"snapshot"` for the degraded fallback.
- No new audit events; no fallback heuristics (hooks remain the sole state source).

## Public payload shapes M8 consumes (all additive)
- `GET /api/v1/servers` → `[{id,name,labels,enabled,state}]` — `state` is the server rollup dot
  (`state,omitempty`; absent before the first poll).
- `GET /api/v1/servers/{id}` → `ServerDetail` with the same `state` field.
- `GET /api/v1/servers/{id}/sessions` and `…/{name}` → each `Session.state` is the projection's global state
  with the requesting principal's seen projection applied (falls back to the agent's inline state pre-poll).
- `GET /api/v1/events` (SSE): first frame `event: snapshot` `data:` = array of `{server,target,session,
  state}` (seen-projected); then `event: state` deltas of the same shape; `: ping` heartbeats. **The seen
  map is captured at connect time** — a `POST /seen` mid-stream isn't reflected until the EventSource
  reconnects (it auto-reconnects → fresh snapshot). Reconnect = re-snapshot, no `Last-Event-ID` replay.
- `POST /api/v1/seen` `{serverId,target,sessionName}` (cookie auth + **CSRF** required; `204`).
- Terminal WS additionally emits `{"t":"state","state":"…","session":"…"}` JSON text frames for the open
  pane's session, interleaved with the binary terminal stream.

## Review path
Per-task SDD reviews on all 15 (spec + quality). Fix rounds on A6 (poller reliability), A7 (target-key
unification + `serverRollup` "unknown" leak), B3 (seen ctx), B4 (broadcaster deadlock), B5 (re-alert test),
each re-reviewed clean. Opus **whole-branch review**: Ready-to-merge YES; its 2 Important fixed (`/events`
authz gate; restart-`received_at` reseed) + 1 follow-up (multi-pane publish suppression). **`/multi-review
--codex`** (general-purpose[specialist] + code-simplifier + deep-scan + codex gpt-5.5): 16 clusters fixed
(headline: SSE subscribe-before-snapshot race [codex+deep-scan]; `SeenProject` empty-anchor bug; nil-`Bcast`
guard; `LatestSessionEvent` index; poller dedup; gofmt), each with regression tests; opus re-review
confirmed behavior-preserving.

## LIVE acceptance — DONE this session (2026-06-29, on this host)
SAFETY (memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]]): built hubd+agent to scratch; ran on
**loopback** (:19378 hub / :19377 agent) against a **`.backup` COPY** of `deploy/data` (prod DB + container
untouched); the copy proved **migration 0003 applies on the real prod-shaped DB**; revoked the prod server in
the copy + seeded a scratch agent; set patrik's password on the copy; scoped to a **throwaway** tmux socket
`agentmon-m7accept` (session `m7proj %0`, plain shell — never session 0 / the `agentmon` demo panes /
`~/.claude/settings.json` / `deploy/data`). Drove the proven M6 `/hook` contract (synthesized hooks) and
observed the full path: agent `/state` (counters+epoch) → poller → `session_state_events` (`source="poll"`)
→ projection → public payload server+session **rollup dots** → SSE **snapshot + seen-projected deltas**
(`working→done→blocked`) + heartbeat → `POST /seen` **`done→idle`** → **new-turn re-alert `done`** (via
`doneSeq`) → `principal_seen` row with its event anchor. Post-test: prod hub container Up, prod agent (pid
842374) alive, default session 0 + demo panes intact, `deploy/data` byte-identical, throwaway socket removed,
repo clean. (The WS `{t:"state"}` frame was not driven live — it needs a full terminal WS relay + directive +
pane control client; it is covered by B7 unit tests + the opus concurrency review, and the M4 relay itself
was live-accepted earlier.)

## Reminders for the next milestone (M8 — web: dots / blocked-first / inbox)
- **The SPA already has the seam.** `SessionList`/`Sidebar`/`GridView` + the TanStack-Query layer render dots
  + blocked-first sorting the moment the payload carries `state` (see `m5-carryover.md`). M8 mirrors the
  shapes above in `web/src/lib/contracts.ts`.
- **Subscribe to `GET /api/v1/events` for live updates**; on reconnect, re-apply the fresh `snapshot`.
- **The terminal WS is delta-only** for state — it emits no initial `{t:"state"}` snapshot at open, so seed
  the focused tile's dot from the REST payload / SSE snapshot, then apply WS deltas.
- **`POST /seen` on focus**, sending the session's own `serverId`/`target`/`sessionName` (CSRF header
  required); the dot then reads `idle` for this principal until the next finished turn.
- Toast/sound/Web-Push **alerts** are Phase 4 — M7 only laid the data path (§18-Q9).

## Deferred (with rationale — surfaced to owner during `/multi-review`, owner approved deferral)
- **Multi-target per agent** (codex rated HIGH): the poller polls `State("")`=all-targets but
  `Sessions("")`=default-only, and `paneToSession` is keyed by pane id, so a non-default-target pane could be
  mis-attributed. **Out of M7 scope** (the design treats one target label per session; the deployment is
  single-target `"default"`). The key *divergence* half was fixed (paneKey now uses the session target);
  full multi-target support (per-target polling / `(target,pane)` keying + skip-unresolvable) is a later
  feature.
- **Multi-pane session seen anchor** (codex): `LatestReceivedAt` advances on any pane's event, so in a
  multi-pane session a non-`done` event could trigger a false re-alert. A Claude session is single-pane in
  practice; deferred. Fix would track latest-*done* `received_at` separately.
- **`LatestSessionEvent` runs under the poller mutex** on the first post-restart tick (deep-scan): restart-
  only, tiny scale now. Move the DB lookups outside the lock if it ever matters.
- **No rate-limit on `/seen` / no SSE per-principal connection cap** (deep-scan): YAGNI for a single-user
  authenticated LAN tool (login already has rate limiting). Add if multi-user lands.
- **Kept by design (conflicts with spec §5.1):** `POST /seen` records seen for an *unenrolled* server id on
  purpose (durability across rename/deregister); not "fixed".
- **Nitpick:** `SeenHandler` decodes the body before `authorizeOr403` (Phase-1 harmless — the resource needs
  body fields; `RequireAuth` already gates).
- Carried per-task Minors (none above Minor) are listed in the ledger's "Minor findings" section.

## Verification at merge
Full Go suite green (`shared` + `agent` + `hubd`, `-race`, uncached); `go vet` clean; `CGO_ENABLED=0 go build
./...` OK across all three modules; `gofmt -l` clean on all M7-touched files (pre-existing M1–M5 files carry a
separate go1.26 trailing-newline skew, left untouched). Live five-state path + seen + re-alert accepted on
this host against a loopback hub + a copy of the prod DB.
