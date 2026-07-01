# AgentMon — Phase 5 (Hardening) — Design

**Date:** 2026-07-01
**Status:** approved-scope, spec under review
**Scope:** ONE small robustness milestone — two changes only.

## 1. Motivation & threat model (why this is small)

AgentMon's deployment is a **single LAN operator**: not internet-reachable, split-horizon
DNS returns `127.0.0.1` off-network, weak admin password is an accepted owner choice. There
is **no adversary**. So classic "hardening" items justified only by *"an attacker could…"*
(Slowloris-as-attack, distinct-username limiter floods, rate-limiting cheap idempotent
endpoints) are near-worthless here and are **explicitly deferred** (see §6).

The one risk that IS real is **the operator's own client misbehaving** — a reconnect storm
or a runaway loop. This codebase has a demonstrated history of WS teardown/reconnect bugs
(fixed across M4, M5, M11, rename). Phase 5 therefore closes exactly the two gaps where a
client bug can spawn **unbounded resources**, and nothing else.

The full six-item hardening backlog was triaged with the owner (2026-07-01); four items were
consciously cut as YAGNI-for-single-operator and parked in the carryover.

## 2. In scope — two changes

### A. Server-side concurrency cap on terminal-WS relays  *(the headline)*

**Gap.** `PaneRelayHandler` (`hubd/internal/api/ws.go`) opens one browser↔hub↔agent relay per
call, and each relay makes the agent spawn a `tmux -C attach` **subprocess on the agent host**.
The web client soft-caps the desktop grid at 6 tiles, but that is a *client* bound — a
reconnect-storm bug or a scripted client bypasses it and spawns unbounded relays, i.e.
unbounded tmux subprocesses on the machine that (on this dev host) **also runs the operator's
real tmux + Claude session**. Nothing server-side bounds this today.

**Fix.** A per-principal live **concurrency gauge**: acquire a slot when a relay opens, release
it when the relay returns. Over the cap → reject the newest with **429** (never kill an
existing relay). Cap = **32 per principal** (≈10× the realistic multi-device + reconnect-overlap
working set; a runaway opens far more; each slot is a cheap subprocess so the ceiling is high
by design — a false rejection of a legitimate terminal is worse than the near-zero cost of a
higher ceiling).

**New primitive — `authn.Gauge`** (new file `hubd/internal/authn/gauge.go`, ~30 lines,
colocated with `Limiter` because both are keyed-limit primitives the `api` package already
reaches into):

```go
type Gauge struct {
    mu    sync.Mutex
    max   int
    inuse map[string]int
}
func NewGauge(max int) *Gauge
func (g *Gauge) Acquire(key string) bool  // inuse[key] < max → ++ , true ; else false
func (g *Gauge) Release(key string)       // -- ; delete key at 0 (no unbounded map growth)
```

- `Acquire` returns `false` without incrementing when at the cap (reject-newest).
- `Release` decrements and **deletes the key at zero**, so the map cannot grow unbounded
  (mirrors `Limiter.prune`'s empty-key eviction).
- A `nil *Gauge` is treated as unlimited by the handler (see wiring), so existing tests that
  construct `Deps` without it are unaffected — the cap is wired only in `main`.

**Wiring in `PaneRelayHandler`:**

1. After `d.authorizeOr403(...)` returns the principal `p` (so we have `p.ID`) and after the
   Origin re-check, **before** `d.Reg.Get` / mint / agent dial:
   ```go
   if d.RelayCap != nil {
       if !d.RelayCap.Acquire(p.ID) {
           writeJSONError(w, http.StatusTooManyRequests, "too many terminal sessions")
           return
       }
       defer d.RelayCap.Release(p.ID)
   }
   ```
   Acquiring **before** the dial means a rejected relay does no wasted agent work. The `defer`
   sits immediately after a successful `Acquire`, before every other early-return, so the slot
   is released on *every* exit path (dial failure, upgrade failure, or normal `relayPanes`
   return). This is the M9 push-dispatcher "release-on-every-path" pattern; it is tested
   explicitly.
2. New nil-guarded field on `api.Deps`: `RelayCap *authn.Gauge`.

**Rejection is not audited.** A cap rejection is a resource rail tripped by the operator's own
client, not an auth event. The success path still audits `terminal.open` as today; a rejection
opens no terminal, so there is nothing to audit. (Contrast the login 429, which stays audited —
that one *is* an auth event.)

### B. Agent HTTP server timeouts + per-request tmux timeout

**Gap.** The agent serves with `http.ListenAndServe(cfg.Listen, mux)` (`agent/cmd/agentmon-agent/
main.go:96`) — **no** Read/ReadHeader/Idle timeouts, while the hub already sets them
(`hubd/cmd/agentmon-hubd/main.go:150`). A half-open client (laptop sleeps mid-request) leaks an
fd/goroutine on the agent indefinitely; a wedged `tmux` invocation ties up a `/sessions` request
with no bound (the server ReadTimeout only covers *reading the request*, which has already
completed by the time the handler shells out).

**Fix — two parts, both mirroring the hub:**

1. Replace the bare `ListenAndServe` with a configured server:
   ```go
   srv := &http.Server{
       Addr:              cfg.Listen,
       Handler:           mux,
       ReadHeaderTimeout: 10 * time.Second,
       ReadTimeout:       30 * time.Second,
       IdleTimeout:       120 * time.Second,
       // No WriteTimeout: the long-lived pane-IO WS relay must not be killed
       // mid-stream (same reason the hub omits it). ReadTimeout is WS-safe:
       // after Upgrade the conn is hijacked and the server timeout no longer applies.
   }
   log.Fatal(srv.ListenAndServe())
   ```
   Values are identical to the hub's, verified WS-safe there in production.

2. Bound each **plain-HTTP** tmux-shelling agent handler with a per-request context so a hung
   `tmux` cannot pin the goroutine. In `SessionsHandler` (discovery — the named "hung tmux ties
   up `/sessions`" gap), `CreateSessionHandler`, `RenameSessionHandler`, and `StateHandler`,
   derive:
   ```go
   ctx, cancel := context.WithTimeout(r.Context(), agentTmuxTimeout) // 10s
   defer cancel()
   ```
   and pass `ctx` to the injected runner/discoverer. A deadline surfaces as the existing error
   path (500 "discovery failed" / "create failed" etc.) — no new error contract.
   `agentTmuxTimeout` is a package const (10s), overridable in tests.

   **The WS pane-IO handler is deliberately left alone.** It is the delicate long-lived relay,
   already carries per-write deadlines, and is torn down when the client disconnects — bounding
   its bootstrap adds risk to the relay path for little value and is out of scope.

## 3. Non-goals / deferred (parked in carryover, NOT built)

| Item | Why deferred |
|------|--------------|
| Session-create rate-limit (#3) | Create is a manual-only UI action; a runaway needs a specific bug in a flow with no automation. |
| `/seen` rate-limit (#4) | Cheap, idempotent, indexed endpoint; a runaway is harmless to SQLite. |
| SSE `/events` per-principal cap (#5) | Goroutine/subscription leak is cheaper than #2 (no subprocess); low stakes for one operator. |
| Limiter map periodic sweep (#6) | Only trigger is a distinct-username flood = an attacker not in the threat model; with one user the map holds ~1 key. |
| Terminal idle-disconnect timeout | Worse UX (walk away → dead terminal) than the near-zero cost; #2's gauge is the real bound. |
| `pending`-row TTL/cap | Already per-IP rate-limited + approval-gated; unbounded *total* is slow + low-value. |
| Absolute cap on total live sessions | #3's rate would already bound a fork-bomb; a total-count cap needs live tracking for little gain. |

Each is recorded in `docs/superpowers/phase5-carryover.md` under "Deferred (resolve if
multi-user lands)" with this rationale, consistent with where earlier phases parked them.

**Already-holding hardening was re-confirmed by reading (this session), not rebuilt:** the M3
spine (`ResolveSecretRef` requires an `env:`/`file:` scheme; argon2 param bounds; the login 429
audits and keys on the trusted-XFF client IP) and the pane-id / control-mode input hardening
(pane id validated by `^%[0-9]+$` at three layers; `send-keys -H` hex-encodes every input byte,
so no command injection is possible). One small confirm-test is added for the hub's `hubPaneIDRe`
(currently untested) since §2A edits that same file — see §5.

## 4. Components & interfaces summary

| Unit | What it does | Depends on | Tested via |
|------|--------------|-----------|-----------|
| `authn.Gauge` | per-key live slot counter; `Acquire`/`Release`; reject-at-cap; delete-at-zero | stdlib only | direct unit tests (acquire to cap, reject, release-frees, delete-at-zero) |
| `PaneRelayHandler` cap wiring | acquire before dial, defer release | `authn.Gauge` (nil ⇒ unlimited) | `httptest`: N ok, N+1 → 429, disconnect frees a slot |
| agent `http.Server` | Read/ReadHeader/Idle timeouts | stdlib | config-value assertion (behavior identical to hub's, already prod-proven) |
| agent per-request tmux timeout | bound a hung tmux per handler | `context` | inject a slow runner → deadline fires → error path within ~timeout |

## 5. Testing & acceptance

- **TDD** each change (test first, watch it fail, implement).
- `authn.Gauge`: unit tests — acquire up to `max` succeeds, `max+1` fails without incrementing,
  `Release` frees a slot, key deleted at zero, concurrency-safe (race test).
- Relay cap: `httptest` handler tests with a nil-agent stub — `Acquire` path returns 429 at the
  cap; a completed/aborted relay releases; the 429 body + status are asserted; slot released on
  a dial-failure early-return.
- Confirm-test: `hubPaneIDRe` accepts `%0`/`%37`, rejects `%0\ninject`, `%0;x`, `1`, `` — locks
  the injection guard on the file we are editing.
- Agent timeouts: assert the constructed `http.Server` carries the three timeouts + no
  WriteTimeout; a table test that a slow injected runner trips the 10s context deadline and the
  handler returns its error status (not a hang).
- `/multi-review --codex` on the batch; apply findings, defer the rest with rationale.
- **SAFE acceptance** (nothing touches prod): a scratch hub on a loopback port + a fresh/copy DB
  + a **throwaway tmux socket** (`tmux -L <scratch>`), never the default socket (session 0 =
  operator + Claude) and never the live `agentmon` socket (the real agent). Drive N+1 relays to
  the throwaway panes to observe the 429; confirm session 0 untouched afterward.
- **Deploy** (owner-confirmed, after acceptance): the relay cap is **hub-only** (`docker compose
  up -d --build` on the dedicated box); the agent timeouts require an **agent** rebuild + restart
  too. Confirm with the owner before any prod deploy.

## 6. Rollout order

1. `authn.Gauge` primitive (TDD, isolated).
2. Relay cap wiring + `Deps.RelayCap` + `main` construction (`NewGauge(32)`) + confirm-test.
3. Agent `http.Server` timeouts.
4. Agent per-request tmux timeout.
5. `/multi-review --codex` → fold findings.
6. SAFE acceptance → owner deploy confirm → carryover + memory update.
