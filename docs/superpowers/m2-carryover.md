# M2 → M3/M4 carry-over (from the M2 reviews)

M2 (agent: terminal WS + HMAC directive) is complete, reviewed, merged. This
captures the consciously-deferred items and the M4 wiring reminders so the next
session can fold them in. M2 review path: per-task reviews (incl. an opus task
review on the WS keystone) → opus whole-branch review ("Ready to merge: Yes")
→ `/multi-review --codex` (feature-dev:code-reviewer + code-simplifier +
deep-scan + Codex gpt-5.5). All Critical/Important/non-nitpick findings were
fixed before merge; the rest are below.

M2 branch: `phase-2-m2`, commits `c7a367d`..`a17f1cd` (9), off `main@176acf3`.

## DECISION — RATIFIED by Patrik 2026-06-27: keep the logged-skip ✅

> Patrik: "Keep the logged-skip; it's the right call." SETTLED — do not revert to a
> hard error. The rest of this section is the rationale, kept for context.

The M2 kickoff pre-task said "make a malformed tmux `-F` record an **ERROR**, not
a silent skip." I shipped that first (hard error). The opus **whole-branch review**
then showed the hard error has an unacceptable blast radius: `splitFields` runs on
the still-escaped line, so a single record whose escaped form contains the `\037`
delimiter token (a name carrying a literal `\037`/raw 0x1f, or a raw 0x1f in a path)
aborted the **entire** session loop — one oddly-named session would 500 the whole
`/sessions` endpoint and hide every other session on that target.

I changed it (commit `185eefc`) to a **logged per-record skip** (session list +
panes + pane resolution): still "never a *silent* drop" (the M1 failure this work
replaces — the skip is logged with the offending record), but a single pathological
name no longer blinds the operator to all other sessions. The trigger is
near-impossible in practice (human-named sessions/windows/paths are escape-free),
the WS pane-resolution path only reads structural `pane_id`/`session_id` (so it was
already immune), and behavior is strictly better than M1.

(Originally flagged for ratification because it softens the literal kickoff wording.
Patrik ratified keeping the logged-skip on 2026-06-27 — closed.)

## Deferred to M4 (hub relay) — must do there

- **Proactive pong-based client liveness.** Flagged by BOTH deep-scan and the
  whole-branch review (both **low**). Today `readPump` sets no read deadline and
  `writePump` pings (20s) without a pong handler, so a half-open client (peer
  crash / network partition with no FIN/RST) lingers — the per-conn goroutines AND
  the live `tmux -C attach` subprocess stay up until TCP retransmission gives up
  (~minutes). It is NOT a hard leak (it does tear down), and the writer-side ping
  write-timeout bounds it. The standard fix —
  `conn.SetReadDeadline(now+pongWait)` in `readPump` + `conn.SetPongHandler` that
  pushes the deadline forward + ping period < pongWait — belongs with M4 where (a)
  real browser clients connect through the relay and (b) `pongWait`/`pingPeriod`
  get tuned for the relayed topology. (Kept M2 faithful to the validated spike,
  which shipped without it.)

### M4 directive-minting wiring reminders (the hub mints; M2 only verifies)
- **Reuse `agent/internal/directive.Sign(key, shared.Directive)`** — it's the HMAC
  mint primitive (the agent only `Verify`s). The hub's minting/relay (`authorize()`
  → mint → dial agent with bearer+directive) is M4; the crypto + canonical form
  already exist (`shared/directive.go` `CanonicalJSON`). Consider promoting `Sign`
  to a shared spot if the hub shouldn't import the agent module.
- **Format `Exp` as `time.RFC3339`, NOT `RFC3339Nano`** — the verifier parses strict
  RFC3339 and returns `ErrMalformed` otherwise.
- **URL-escape the pane id when dialing** the agent WS: a pane id is `%3`; the path
  segment must be `%253` (`url.PathEscape`), which the agent's `PathValue` decodes
  back. (The agent-side handling + tests already do this.)
- The directive's **`Mode` is authoritative** for ro/rw and the agent requires the
  URL `?mode=` to equal it — the hub must mint and dial with the same mode (P1 hub
  always mints `rw`).
- The agent caps `Exp` at **`maxLifetime` = 5m** in the future and rejects empty
  `Nonce`; mint short (~60s) expiries with a unique non-empty nonce per directive.

## Minor (opportunistic, not blocking)

- **`CapturePane` bypasses the `Runner` seam** (`agent/internal/tmux/pane.go`) — it
  calls `exec.CommandContext` directly, so it skips ExecRunner's stderr-folding and
  isn't unit-testable through the injected seam. Harmless (a capture error just skips
  the scrollback frame). Route through `Runner` if/when M3+ touches it.
- **Directive Minors (from the Task A review):** mode check is ordered after
  server/resource/target, so the *error identity* reveals partial field-match — not
  exploitable (needs a valid HMAC; the handler maps all to a generic 403). `ErrExpired`
  is overloaded for past-exp AND the far-future cap. No `now==exp` boundary test
  (logic is correct). All optional.
- **Skipped nitpicks** (multi-review): group two test consts; map-literal→if in a
  test helper (`ws_test.go`).

## Still-open M1/M0 carry-overs (NOT addressed in M2 — fold into the hub work)

From `docs/superpowers/m1-carryover.md` / `m0-carryover.md`:
- **`resolveRef` secret hardening — BEFORE auth (M3).** Require an `env:`/`file:`
  scheme for secret fields (reject bare literals); make the hub + agent loaders
  symmetric. (`agent/internal/config/config.go`, `hubd/internal/config/config.go`.)
- **`migrate()` transactions** — wrap in a txn once a non-idempotent (`ALTER`)
  migration lands. (`hubd/internal/db/migrations.go`.)
- **Project-wide `http.Server` timeouts** — the agent (and the M0 hub scaffold) use
  `http.ListenAndServe` with no Read/Write/Idle timeouts (Slowloris). Mitigated by
  LAN-only deployment; add when hardening. **Note for M4:** the long-lived terminal
  WS needs careful timeout choice (a global WriteTimeout would kill the stream —
  use per-message deadlines, which the WS handler already does, and avoid a server
  WriteTimeout on the WS route or use ResponseController).
- **Discovery has no request timeout** — a hung `tmux` ties up `/sessions`; derive
  `context.WithTimeout` from `r.Context()`. `/sessions` also spawns 1+N tmux procs
  with no caching (fine at M1/M2 scale).
- **`.dockerignore` / Dockerfile** — `.dockerignore` omits `hubd/internal/webui/dist/`;
  Dockerfile lacks a `go mod download` cache layer (fold into hub build, M3/M4).
- **hubd test hygiene** (`repo_test.go` `d, _ := Open`, `directive_test.go` discarded
  error / unasserted `UserID`, `health_test.go` no version assert) — fix with the hub
  milestones.

## M3 teed up — Hub: auth + registry + REST

Spec §8 M3. **Cannot be verified unattended** (its done-when needs the two real LAN
servers + Caddy + a real cert + secrets Patrik controls), which is why the M2
overnight run STOPS here. Scope: SQLite repos + migrations live; login (argon2id) +
HttpOnly/Secure/SameSite cookie + CSRF + login rate-limit + `/me`/`logout`; the
`authorize()` chokepoint (trivial body in v1); config server registry; `GET /servers`,
`/servers/{id}`, `/servers/{id}/sessions`, `/sessions/{name}` (hub→agent bearer);
audit login + denies. Do the `resolveRef` secret hardening (above) BEFORE auth lands.
M4 then adds the hub WS relay + directive minting (review gate 2), reusing the M2
agent contract documented above.
