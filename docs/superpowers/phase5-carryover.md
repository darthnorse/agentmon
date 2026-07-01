# Phase 5 (Hardening) → carry-over

Phase 5 is complete, reviewed (4 per-task reviews + opus whole-branch + `/multi-review --codex`),
and merged to `main`. It is a deliberately SMALL milestone: the six-item hardening backlog carried
through Phases 1–4 was triaged with the owner against the real threat model — a **single LAN operator,
no adversary** (split-horizon DNS → `127.0.0.1` off-LAN, not internet-reachable, weak admin password is
an accepted choice) — and trimmed to the **two** changes that bound *unbounded resource spawn from a
misbehaving client* (a reconnect storm / runaway loop, a demonstrated pattern: WS teardown bugs were
fixed in M4/M5/M11/rename). The other four items were consciously deferred as YAGNI-for-single-operator.

Spec: `docs/superpowers/specs/2026-07-01-agentmon-phase5-hardening-design.md`.
Plan: `docs/superpowers/plans/2026-07-01-agentmon-phase5-hardening.md`.

## What shipped

1. **Per-principal concurrency cap on terminal-WS relays** (the headline — protects the dev box).
   - New primitive `authn.Gauge` (`hubd/internal/authn/gauge.go`): a per-key LIVE slot counter
     (`Acquire`/`Release`/`InUse`), distinct from the existing sliding-window `authn.Limiter` (a rate
     counter). Reject-newest: at the cap `Acquire` returns false without incrementing (an existing relay
     is never evicted). `Release` decrements and deletes the key at zero, so key churn can't grow the map.
   - Wired into `PaneRelayHandler` (`hubd/internal/api/ws.go`): after authorize + the Origin re-check and
     **before** the agent dial, `Acquire(p.ID)` with an immediately-registered `defer Release(p.ID)` — so
     the slot frees on **every** exit path (dial/mint/url/upgrade failure + normal `relayPanes` teardown).
     Over the cap → HTTP **429** `"too many terminal sessions"`; the rejection is **not audited** (it's a
     resource rail, not an auth event — contrast the login 429, which stays audited).
   - Cap = **32 per principal**, wired only in `main.go` via `authn.NewGauge(32)`. `Deps.RelayCap` is
     **nil-guarded** (nil ⇒ unlimited), so every existing test is unaffected.
   - Why it matters: each relay makes the agent spawn a `tmux -C attach` subprocess **on this dev box**
     (which also runs the operator's real tmux + Claude). A client bug opening unbounded relays = a
     subprocess fork-bomb on the workstation. This is the one item with a concrete "could take down your
     machine" story; the client's 6-tile grid soft-cap is a *client* bound a bug bypasses.

2. **Agent HTTP hardening** (`agent/cmd/agentmon-agent/main.go` + `agent/internal/api/sessions.go`).
   - `newAgentServer` replaces the bare `http.ListenAndServe`: `ReadHeaderTimeout 10s`, `ReadTimeout 30s`,
     `IdleTimeout 120s`, **no WriteTimeout** — identical to the hub's, WS-safe because gorilla's `Upgrade`
     clears the http.Server deadline on hijack (so the pane-IO terminal WS is never severed). fd/goroutine
     hygiene for half-open conns; corrects a hub/agent asymmetry.
   - A 10s per-request `context.WithTimeout` (via `withTmuxTimeout(r)`) bounds the three tmux-**shelling**
     plain-HTTP handlers — `SessionsHandler`, `CreateSessionHandler`, `RenameSessionHandler` — so a hung
     `tmux` can't pin the goroutine (the server ReadTimeout only covers reading the request, not the
     shell-out). `agentTmuxTimeout` is a `var` (test-shortenable). `exec.CommandContext` makes the deadline
     SIGKILL a real hung `tmux` child, not just a cooperative test fake.
   - **Untouched:** `StateHandler` (in-memory `m.Snapshot`, no shell-out) and the WS pane-IO handler (the
     delicate long-lived relay — deliberately left alone).

## Review outcomes

- **4 per-task reviews** (haiku/sonnet impl; sonnet/opus review): all Spec ✅ / Approved, no
  Critical/Important, no fix rounds.
- **Whole-branch (opus, 20f4496..4b3d596): Ready-to-merge YES**, no Critical/Important. Verified 3
  cross-file invariants: slot held for the full relay lifetime (`relayPanes` blocks on 3×`<-done` until
  teardown); the 10s ctx SIGKILLs a real tmux (`exec.CommandContext`); ReadTimeout is WS-safe (gorilla
  clears the hijacked-conn deadline).
- **`/multi-review --codex`** (feature-dev:code-reviewer[specialist] + code-simplifier + deep-scan +
  codex gpt-5.5): codex + deep-scan CLEAN; 4 low/info findings, **3 fixed** (commit `8c2ea1a`): a missing
  `TestRenameSessionHandlerTimesOutOnHungTmux`, `Gauge.Release` explicit guard/evict, and a
  `withTmuxTimeout(r)` DRY helper. 1 nitpick NOT applied ("drop `Phase 5:` comment prefixes") — it
  conflicts with the codebase's milestone-prefix house style (`// M4:`/`// M7:`/`// M9:`).

## Acceptance (owner-approved 2026-07-01: accept on test+review evidence, skip live)

The 429-at-cap path is exercised by a unit test that drives the **real** `PaneRelayHandler` through a
**real gorilla WS handshake** (`TestRelayConcurrencyCapRejectsOverCap`) + release-on-early-return +
release-after-teardown, all `-race`; the agent timeouts fire a real `context` deadline. No DB migration,
no config change, no new external surface. Full workspace verified: `go test ./... -race` GREEN (hubd +
agent + shared), `go vet` clean, `CGO_ENABLED=0 go build` both binaries. No live scratch-hub run was done
(the cap rejects before any agent/tmux dial, so a live test re-exercises mostly-unchanged paths).

## Deferred (resolve if multi-user ever lands — NOT built, with rationale)

- **Session-create rate-limit** — create is a manual-only UI action; a runaway needs a specific bug in a
  flow with no automation.
- **`POST /seen` rate-limit** — cheap, idempotent, indexed endpoint; a runaway is harmless to SQLite.
- **SSE `/events` per-principal cap** — goroutine/subscription leak is cheaper than a relay (no
  subprocess); low stakes for one operator.
- **`authn.Limiter` map periodic sweep** — its only trigger is a distinct-username flood = an attacker
  not in the threat model; with one user the map holds ~1 key. (Note: empty keys are already evicted
  on access; only never-revisited stale keys persist.)
- **Terminal idle-disconnect timeout** — worse UX (walk away → dead terminal) than the near-zero cost;
  the relay gauge is the real bound.
- **`pending`-row TTL/cap** — already per-IP rate-limited + approval-gated; unbounded *total* is slow +
  low-value.
- **Absolute cap on total live sessions** — the (deferred) create rate would already bound a fork-bomb;
  a total-count cap needs live tracking for little gain.

**Confirmed still-holding (re-read this session, NOT rebuilt):** the M3 spine (`ResolveSecretRef` requires
an `env:`/`file:` scheme; argon2 param bounds; the login 429 audits and keys on the trusted-XFF client IP,
consistent with enroll/install) and the pane-id / control-mode input hardening (pane id validated by
`^%[0-9]+$` at three layers; `send-keys -H` hex-encodes every input byte → no command injection). A
confirm-test (`TestHubPaneIDReRejectsInjection`) now locks the hub's `hubPaneIDRe`.

## Accepted trade-off noted (deep-scan non-finding, not a bug)

If the 10s ctx fires mid `tmux new-session -d`, the SIGKILLed client may leave a detached session created
just as the handler returns 500 "create failed" — indeterminate but **self-healing** (the next `/sessions`
list surfaces it), and only on a genuinely hung tmux, i.e. exactly the accepted robustness-rail trade-off.

## Deploy notes (owner runs on the dedicated box — NOT done automatically)

Merged to `main` + pushed to `origin/main`. **Deploy is a two-part redeploy on the dedicated box** (the
same as any change touching both halves):
- Relay cap = **hub-only**: `git pull && docker compose up -d --build` (re-embeds nothing new; pure Go).
- Agent timeouts require an **agent rebuild + restart** too (rebuild → `systemctl stop agentmon-agent` →
  `install -m0755 … /usr/local/bin/agentmon-agent` → `start`).
No DB migration, no config change, no VAPID/push change. Confirm with the owner before deploying (per the
kickoff). See [[live-deployment]] for the full recipe.
