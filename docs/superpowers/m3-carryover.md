# M3 → M4 carry-over (from the M3 reviews)

M3 (hub: auth + registry + REST) is complete, reviewed, merged. This captures the
consciously-deferred items and the M4 wiring reminders so the next session can fold
them in. M3 review path: SDD per-task reviews (spec + quality; one opus task review
on the login keystone) → whole-milestone verification → **opus whole-branch review
("Ready to merge: Yes", no Critical)** → a focused post-review hardening commit. The
FULL `/multi-review --codex` gate for the hub is at **M4** (hub = M3+M4), per the
Phase-1 cadence — it was NOT run at M3.

M3 branch: `phase-3-m3`, 18 commits off `main@d20a9dd`. All Critical/Important
findings were fixed before merge; the rest are below.

## Project-wide change made during M3 (heads-up)
- **Go bumped 1.23 → 1.26.4 (latest)** and **`golang.org/x/crypto` → v0.53.0
  (latest)**, applied consistently across `go.work`, all three `go.mod`,
  `deploy/Dockerfile` (`golang:1.26-alpine`), and CI (`go-version: "1.26"`).
  Driven by argon2id needing x/crypto, and Patrik's standing rule: **always use the
  latest version of everything; verify "latest" from a live source, never from model
  memory** (now a saved preference). Re-verify/refresh these when M4 lands.

## Post-review hardening already DONE in M3 (commit `a314405`)
The opus whole-branch review's two Important items were (partly) addressed now, not deferred:
- **argon2 verify concurrency cap** — a package-level `verifySem` (buffered chan, cap 4)
  bounds concurrent `VerifyPassword` calls (~64 MiB each). The per-username limiter does
  NOT throttle a *distinct-username* flood (the limiter keys on the attacker-supplied
  username), so without this a burst of distinct unknown usernames = N×64 MiB unthrottled.
  `-race` test with 12 concurrent logins added.
- **Secure-cookie startup guard** — `main()` logs a loud WARNING when `external_origin`
  is `https://…` but `trust_forwarded_proto` is false (cookie would be issued WITHOUT
  `Secure` behind Caddy — silent downgrade footgun). **Patrik MUST set
  `trust_forwarded_proto: true` in his real behind-Caddy config.**
- **429 throttle now audited** — the rate-limit branch records a `login.failure` before
  returning 429 (closes the "sustained brute-force after the limit leaves no audit trail"
  gap).

## Deferred to M4 (hub relay) — the actual M4 scope
M4 = **WS relay + HMAC directive MINTING + audit `terminal.open`** (spec §8 M4). This is
the second half of the hub; the M3+M4 pair is gated by `/multi-review --codex` after M4.

### M4 directive-minting wiring reminders (carried from m2-carryover; the hub mints, the agent only verifies)
- **Reuse `agent/internal/directive.Sign(key, shared.Directive)`** — the HMAC mint
  primitive. The crypto + canonical form already exist (`shared/directive.go`
  `CanonicalJSON`). Consider promoting `Sign` to a shared spot if the hub shouldn't
  import the agent module.
- **Format `Exp` as `time.RFC3339`, NOT `RFC3339Nano`** — the verifier parses strict
  RFC3339 and returns `ErrMalformed` otherwise.
- **URL-escape the pane id when dialing** the agent WS: pane id `%3` → path segment
  `%253` (`url.PathEscape`), which the agent's `PathValue` decodes back.
- The directive's **`Mode` is authoritative**; the agent requires the URL `?mode=` to
  equal it. P1 hub always mints `rw`.
- The agent caps `Exp` at **`maxLifetime` = 5m** and rejects an empty `Nonce`; mint short
  (~60s) expiries with a unique non-empty nonce per directive.
- **Proactive pong-based client liveness** (flagged in M2, low): in the relay add
  `conn.SetReadDeadline(now+pongWait)` in `readPump` + a `SetPongHandler` that pushes the
  deadline forward + ping period < pongWait, tuned for the relayed topology. (M2 shipped
  without it, faithful to the spike.)
- **WS server timeouts:** the M3 `http.Server` deliberately has NO `WriteTimeout` (so the
  long-lived terminal WS isn't killed). Keep per-message deadlines on the WS route; do not
  add a global WriteTimeout (or use `http.ResponseController`).

## Hardening / minors deferred to M4 (from the opus whole-branch review — triaged carry-OK)
Bounded by the single-user, LAN-only, behind-Caddy deployment; none blocked the M3 merge.
- **`clientIP` trusts `X-Forwarded-For` unconditionally for the audit `ip`** (in
  `authn/login.go` AND `api/servers.go`), while proto-trust IS gated by
  `trust_forwarded_proto`. The audit source IP is therefore spoofable (attribution-only;
  rate-limiting keys on username, not IP). Fix: gate XFF the same way (and take the
  right-most/first untrusted hop; XFF may be a comma-list). The `api` path needs
  `TrustForwardedProto` threaded into `api.Deps`.
- **IP-based pre-verify throttle** — add in M4 to complement the per-username limiter +
  the new verify semaphore (defense in depth against the distinct-username flood).
- **Unbounded limiter map** (`ratelimit.go` `fails`) — pruned per-key but never swept;
  a distinct-username flood grows it monotonically. Add a periodic sweep or cap.
- **`VerifyPassword` robustness** (`authn/password.go`) — doesn't validate the parsed
  argon2 `version` against `argon2.Version`, and casts parsed `m,t,p` to `uint32/uint8`
  with no lower-bound guard. Corrupt-DB-row hardening only (rows are only written by
  `HashPassword` with valid params).
- **Registry client builds URLs by string concat** (`registry/client.go`:
  `srv.URL + "/sessions"`) → a configured trailing slash yields `//sessions`. Use
  `url.JoinPath` or trim.
- **`SessionDetailHandler` hardcodes target `"default"`** in the authorize resource and
  the hub queries the agent with `target=""`; sessions on non-default targets are invisible.
  Revisit when targets are modeled / real authz policy lands.
- **Unknown `/api/v1/...` paths + wrong methods fall through to the SPA catch-all**
  (`router.go` `mux.Handle("/", WebUI)`) → HTML 200 instead of a 404/405 JSON envelope.
  API hygiene. (Tie to the M5 SPA-serving work or add an `/api/` 404 guard in M4.)
- **Audit `ts` is second-precision** (`datetime('now')`) and `Recent` orders by `ts DESC`
  → same-second rows order nondeterministically. Display-only; consider a monotonic/rowid
  tiebreak if ordering matters.
- **Test-coverage nits** (all non-blocking): `health.go` uses inline `json.Encode` not the
  `writeJSON` helper (within-package style dup); `audit_test` lacks a negative assertion
  that `/audit` omits ip/meta/user_agent (add a `len(row)==5` guard); `displayName` not
  asserted in `/me`+middleware tests; CSRF only POST-tested (switch covers all mutating
  methods); `SecureFromRequest` `r.TLS` branch untested; `session.go` `s.now()` read
  outside the mutex in `New` (harmless — `now` set once at construction);
  `TestAuditAppendAndRecent` still uses `d, _ := Open`; rename `TestLoadUnsetEnvRefErrors`
  (now tests bare-literal rejection).

## Still-open M2/M1/M0 carry-overs NOT addressed in M3 (re-confirm in M4)
- **`migrate()` transactions** — STILL deferred (correctly): M3 added NO migration (sessions
  are in-memory; `db.SetPassword` is a repo method, not a schema change). Wrap each
  migration file in a txn the moment a non-idempotent (`ALTER`-style) migration lands.
  (`hubd/internal/db/migrations.go`.)
- **`CapturePane` bypasses the `Runner` seam** (`agent/internal/tmux/pane.go`) — route
  through `Runner` if/when M4+ touches it.
- **`.dockerignore` omits `hubd/internal/webui/dist/`; Dockerfile lacks a `go mod download`
  cache layer** — fold into the hub build hardening (M4/M5). The Dockerfile builder image
  was bumped to `golang:1.26-alpine` in M3.
- **Project-wide `http.Server` timeouts** — the hub now sets ReadHeader/Read/Idle timeouts
  (no WriteTimeout, WS-safe). The **agent** still uses `http.ListenAndServe` with no
  timeouts (Slowloris; mitigated LAN-only) — add when hardening the agent.
- **Discovery has no request timeout** (agent) — a hung `tmux` ties up `/sessions`.

## LIVE acceptance — DEFERRED TO PATRIK (the M3 done-when that needs his stack)
Built + unit/httptest-tested fully; the live run needs the two real LAN servers + Caddy +
a real cert + the real secrets Patrik controls, so it could not be verified unattended.
Tee'd up for him:
1. Put real values in `config.yaml` (the two servers' `url` + `token_ref`/`signing_key_ref`
   via `env:`/`file:`), set **`trust_forwarded_proto: true`** and `external_origin:
   https://<host>`, set the agent tokens, and `agentmon-hubd user set-password --username
   <u>` (password via `AGENTMON_PASSWORD` env or stdin).
2. Behind Caddy (real cert), **log in over HTTPS** → cookie set with `Secure` (verify the
   startup WARNING is absent, i.e. trust_forwarded_proto is on).
3. `GET /api/v1/servers/{id}/sessions` returns **project-labelled sessions from BOTH real
   servers**; unauth → 401; CSRF/origin enforced; login success/failure audited.
4. (Optional, also available on this dev box) point one registry entry at a LOCAL `tmux -L`
   agent for a real hub→agent `/sessions` smoke — the httptest fake-agent integration
   already covers the wire contract (Bearer header, `/sessions` envelope, server-id stamp,
   agent-error → 502), so this is confirmation, not a gap.

## M3 design decisions (locked; do not re-litigate)
- **Sessions are in-memory, server-side** (single process, single user). Real `Store.Delete`
  logout (true revocation); restart → re-login (acceptable for Phase 1; persist in a later
  phase if wanted). No sessions table → no migration → the `migrate()` txn item stays
  correctly deferred. Cookie is opaque server-minted token (no fixation).
- **CSRF = synchronizer token** tied to the session, returned in the login/`/me` JSON body
  (the HttpOnly cookie is unreadable to JS), required as `X-CSRF-Token` on cookie-authed
  mutations. Companion control: `SameSite=Lax`.
- **`authorize()` is the single chokepoint**, called authorize-first in every resource
  handler; v1 body allows any authenticated principal, denies empty. The seam is real
  (M4 WS upgrade calls it too); denies are audited.
- **Registry is config-driven**, loaded at boot; secrets (token/signing key) are hub-side
  only and never appear in the browser-facing DTOs (`ServerSummary`/`ServerDetail` =
  id/name/labels/enabled[/healthy]).
- **hub→agent REST** dials with the per-agent **bearer** only (no directive for REST — the
  directive is WS-only, minted in M4); the hub **stamps the registry server id** on returned
  sessions (never trusts the agent's self-report); agent failure → **502**.
- NOT in M3 (M4+): WS relay, directive minting, `POST /sessions`, hook listener, web SPA.
