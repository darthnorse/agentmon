# Agent Onboarding → M4 carry-over

The **Agent Onboarding** milestone (enrollment + dynamic DB-backed registry + a
one-command installer), pulled in ahead of M4, is complete, reviewed, and merged.
This captures the consciously-deferred items, the live-acceptance hand-off to
Patrik, and the M4 wiring reminders so the next session can fold them in.

Branch: `agent-onboarding`, 11 commits off `main@c9966f6`. Spec:
`docs/superpowers/specs/2026-06-28-agentmon-agent-onboarding-design.md`. Plan:
`docs/superpowers/plans/2026-06-28-agentmon-agent-onboarding.md`.

## What shipped (8 SDD tasks, TDD throughout)
1. **Transactional `migrate()` + `0002_enrollment.sql`** — each migration file now runs
   in a transaction (rollback-on-error, tested); `0002` drops+recreates `servers` into the
   enrollment shape (the first non-idempotent migration; fresh volume so no data to migrate).
2. **`servers` repo** (`db.Server` + Enroll/Get/Find/List(status)/SetStatus/Delete/TouchLastSeen).
3. **Dynamic registry** — `registry.New(db)` reads the DB **live** per lookup (active-only),
   so a CLI approve/revoke/rm (a separate process on the shared WAL DB) takes effect on the
   running hub **without a restart**. Client dials with the DB row's bearer; stamps the
   registry id on returned sessions. No-secrets browser DTOs preserved.
4. **Audit** — `server.enroll/approve/revoke/remove` (id + hostname + ip; never a secret).
5. **`POST /api/v1/enroll`** — open, LAN-only, per-IP rate-limited, approval-gated. Generates
   independent 32-byte CSPRNG bearer + signing key; stores a `pending` row; returns creds over
   HTTPS only. Dial URL derived via `authn.ClientIP(r, trust_forwarded_proto)` (trusted-proxy
   correct — last XFF hop behind Caddy, RemoteAddr direct on LAN).
6. **`GET /install.sh` + `GET /dl/agent-linux-{amd64,arm64}`** — templated bash installer
   (hub URL + per-arch sha256 computed from the served bytes) + embedded agent binaries
   (`//go:embed`, placeholder pattern). `shellcheck`-clean; traversal-safe `/dl`.
7. **Admin CLI** `agentmon-hubd server list|approve|revoke|rm` (direct DB access; live registry).
8. **Config/deploy** — `config.yaml` lost its `servers:` block (+`enroll_rate_limit`); the
   Dockerfile cross-compiles both agent arches into the embed dir before building hubd; CI
   guards the embed placeholders + `shellcheck`s the installer; `make build-hub` likewise embeds
   real agents locally (restores placeholders after).

## Review path
SDD per-task (spec + quality) reviews on all 8; **opus** review on Tasks 1/5/6 (txn migration,
open enroll endpoint, bash installer). Then **opus whole-branch** review ("Ready to merge: With
fixes" → dial-URL fix applied → re-confirmed "Ready to merge"). Then the **`/multi-review --codex`**
4-lens gate (feature-dev:code-reviewer[specialist fallback] + code-simplifier + deep-scan + Codex
gpt-5.5). All Critical/Important findings fixed before merge.

### `/multi-review --codex` findings fixed (commit, each test-locked)
- **agent.toml perms (cross-model: specialist=critical + codex=high)** — the installer wrote
  `/etc/agentmon/agent.toml` 0600 root-owned but chowned only the secret files; the systemd unit
  runs the agent as the invoking non-root user, which then couldn't read its own config → the
  smoke test would abort the install on the normal `sudo bash` path. **Would have broken Patrik's
  live run; caught pre-merge.** Fixed: chown+chmod agent.toml alongside the secrets.
- **Atomic onboarding rate-limit** — `Allowed`+`Fail` were two separate locked ops (a burst could
  exceed the limit). Added `authn.Limiter.Take` (prune+check+append under one lock); `-race` test.
- **`make build-hub` embedded placeholders** — local hub builds served non-working `/dl` bytes.
  Added an `embed-agents` target mirroring the SPA embed flow.
- **`server list` now shows the dial URL** — the approval gate is the security model, so the
  operator must see which IP the hub will dial before `server approve`.
- **enroll `os`/`agentVersion` validated** (`fieldRe`) — they render in the operator's `server list`;
  this blocks terminal-escape / row-forging injection from the open endpoint.

## Deferred / carried (non-blocking; reviewers + I triaged as carry)
- **Enroll duplicate-id TOCTOU → 500 instead of 409** (`enroll.go`): the GetServer-then-Insert
  pre-check is non-atomic; a concurrent double-enroll for the same hostname hits the PK and yields
  500. Bounded (single-user, rate-limited, non-destructive; the PK is the real backstop). Fix when
  it matters: map a UNIQUE/PK violation from `EnrollServer` to 409.
- **No TTL/cap on `pending` rows** (`enroll.go`): rate-limited but unbounded total; a persistent LAN
  client can accumulate pending rows. Bounded by approval gate + per-IP limiter. Future hardening:
  a stale-pending sweep or per-IP pending cap.
- **Simplification nitpicks** (deliberately not applied, per "fix all but nitpicks"): `captureSink`
  in `audit_test.go` duplicates `fakeSink`; `enrollMax`/`enrollWindow` duplicate the
  `rateMax`/`rateWindow` shape in `main.go`; `ServerApprove/Revoke/Remove` could share a private
  `serverEvent` helper; `servers.go` `args := []any{}` → `var args []any`; `server_cmd.go` could fold
  `list` into the dispatch switch. Plus per-task test-coverage nits (FindServer not-found + non-nil
  Labels round-trip; partial assertions on approve/revoke/remove audit rows).
- **Labels** are modelled (`db.Server.Labels`, JSON column) but enrollment sets none; the column is
  present-but-unpopulated like `tmux_targets` (single default target). M5/CLI can add label mgmt.

## Security model notes (locked; for the live run)
- The three onboarding endpoints are the only new unauthenticated surface; mitigated by LAN-only +
  per-IP rate-limit + the **approval gate** (a stranger lands `pending`, is invisible to the API and
  never dialed). Worst case if an attacker enrolls: the hub may dial a box the attacker controls
  **with a bearer the attacker already holds** (it was minted for that pending row) — no escalation,
  no other server's data is reachable; the operator chooses what to approve and now sees the dial URL.
- **`trust_forwarded_proto: true` precondition:** the hub trusts X-Forwarded-For, so its listen port
  MUST be reachable only by the trusted proxy (the example `docker-compose.yml` loopback bind, or a
  firewall). Noted inline in the compose file. With `trust_forwarded_proto: false` (direct LAN),
  `ClientIP` ignores XFF and uses the direct peer.
- Secrets (bearer/signing key) cross the wire only in the enroll HTTPS response; never logged, never
  in audit rows, never in browser DTOs or `server list`. Verified end-to-end by the whole-branch +
  multi-review passes.

## LIVE acceptance — DEFERRED TO PATRIK (the done-when that needs his two real servers)
Built + unit/httptest-tested fully (incl. the proxied-XFF dial-URL path and a full `docker build`
with both arches embedded); the live run needs the two real LAN servers + Caddy + a real cert.
Tee'd up (spec §7/§8):
1. `docker compose up` → hub serves `/install.sh` + both arch binaries. Ensure `external_origin:
   https://<host>` and `trust_forwarded_proto: true`, and the hub port is loopback/firewalled.
2. On **both** servers: `curl https://<hub>/install.sh | sudo bash` → `✓ <host> enrolled — pending
   approval`. (The agent.toml-perms fix means a non-root run-as user can now read its config — the
   path that was broken before merge.)
3. On the hub: `agentmon-hubd server list` shows both `pending` (with their dial URLs); `server
   approve <host>` each → `active`.
4. `GET /api/v1/servers/{id}/sessions` returns project-labelled sessions from **both** real servers;
   `server revoke` one → it disappears from the API (registry reads the DB live, no restart).
5. Optional: `--dry-run` the installer to inspect the plan with no side effects.

## M4 reminders (the actual next milestone — WS relay + directive minting)
The onboarding milestone did NOT touch the WS relay. Everything in `docs/superpowers/m3-carryover.md`
§"Deferred to M4" still stands and is the M4 scope:
- **WS terminal relay + HMAC directive MINTING + audit `terminal.open`** (spec §8 M4). The hub mints,
  the agent only verifies. Reuse `agent/internal/directive.Sign`; format `Exp` as RFC3339 (not Nano);
  URL-escape the pane id when dialing; `Mode` authoritative (P1 mints `rw`); cap `Exp` short (~60s)
  with a unique non-empty nonce; proactive pong-based client liveness; no global WriteTimeout on the
  WS route (per-message deadlines).
- The hub now has a **per-server `signing_key`** in the DB (minted at enrollment) — M4's directive
  mint reads it from the registry's `db.Server.SigningKey`, no longer from config.
- Carried M3 hardening/minors (m3-carryover §"Hardening/minors deferred to M4") still apply where the
  WS work touches them (e.g. `SessionDetailHandler` hardcodes target `"default"`; unknown `/api/v1`
  paths fall through to the SPA catch-all → add an `/api/` 404 guard).

## Verification at merge
Full per-module suite green (16 pkgs, `CGO_ENABLED=0`); static build OK; `go vet` clean; `-race`
clean (api/authn/db, incl. the atomic-`Take` test); `shellcheck` clean; `gofmt` clean; embed-placeholder
CI guard OK; `docker build` succeeds with both agent arches embedded. Live two-server flow = Patrik's.
