# AgentMon — Agent Onboarding (enrollment + dynamic registry + one-command installer)

*Design spec. A milestone inserted **after M3, before M4** of [Phase 1](2026-06-27-agentmon-phase-1-design.md):
pulled forward from the Phase-2 deployment story because the manual agent-setup path is too
complex to live with. The agent binary itself is unchanged; this is a hub-side feature + a thin
installer.*

Date: 2026-06-28

---

## 1. Purpose & scope

Replace the manual, error-prone agent install (build the binary → hand-write `agent.toml` → generate
secrets → wire systemd → **edit the hub's `config.yaml`**) with a one-command, self-registering flow:

```text
# hub (once):     docker compose up
# each server:    curl https://hub.lan/install.sh | sudo bash
#                 → "✓ web-01 enrolled — pending approval"
# hub (admit it): agentmon-hubd server approve web-01
#                 → web-01's sessions appear in the API
```

The value is that **standing up a new agent is one command and one approval**, with nothing edited by
hand and nothing pasted into the hub config.

### 1.1 In scope

- Hub serves its **own installer** (`GET /install.sh`, templated with the hub URL) and the **embedded
  agent binaries** (`GET /dl/agent-linux-{amd64,arm64}`).
- **Open enrollment** (`POST /api/v1/enroll`) — unauthenticated, LAN-only, rate-limited — that records a
  **`pending`** server and returns generated credentials to the agent.
- **Approval gate:** a server is invisible to the API and never dialed until an operator approves it.
- **Dynamic, DB-backed registry** (replaces M3's static `config.yaml` registry).
- **Admin CLI:** `agentmon-hubd server list | approve | revoke | rm`.
- The **installer script** end-to-end: arch detect, binary download + checksum verify, identity =
  hostname, run-as = invoking user, enroll, write config/secrets, systemd, smoke-test.

### 1.2 Out of scope (deferred, with target)

| Deferred item | Target | Notes |
|---|---|---|
| Browser approve/manage UI | M5 (web) | The CLI endpoints are built so the SPA just wraps them. |
| In-place secret rotation | later | For now, "rotate" = re-run `curl\|bash` to re-enroll. |
| Multi-target per box | later | One default target (current user, default socket) is auto-configured; multiple tmux **sessions** still auto-surface by name. |
| Encryption-at-rest for stored agent secrets | later | The hub's SQLite (bearer + signing key) is root-600 on the data volume — consistent with the hub already holding agent secrets server-side. |
| WS terminal relay + directive minting | **M4** | This milestone is onboarding only; the relay is still next. |
| Enrollment shared-secret / internet exposure | n/a | The hub is LAN-only (decided); the enroll endpoint is open + rate-limited + approval-gated. |

---

## 2. Locked decisions (settled in brainstorming)

1. **Admit model = open enroll + CLI approval.** Identical command on every box (no per-box token); the
   box lands as `pending`; the operator runs `agentmon-hubd server approve <hostname>`. The M5 browser
   button later wraps the same endpoint. (No standalone approve UI is built now.)
2. **Registry = DB-only.** `config.yaml` holds only hub settings; every server lives in the DB via
   enrollment. M3's `registry.New(cfg.Servers)` is refactored to read the DB. Nothing is deployed yet, so
   there is no data to migrate.
3. **Enroll endpoint is open, LAN-only, rate-limited.** No enrollment secret (the hub is not exposed to
   untrusted networks); approval is the gate, and a rate-limit bounds pending-queue spam.
4. **Agent binaries are embedded in the hub image** and served by the hub — `linux/amd64` **and**
   `linux/arm64`. No internet needed at install time, no external release pipeline.
5. **Lifecycle now = approve + revoke + remove.** In-place secret rotation is deferred (re-enroll covers
   it).
6. **Run-as = the invoking user** (root if `sudo`'d as root); `--user=` overrides. No hardcoded `dev`.
   **Identity = `hostname -s`**; `--hostname=` overrides.
7. **Agent stays passive** — it serves; it calls the hub exactly once (enroll). The hub dials it (only
   when `active`). No agent-side authorization decisions, consistent with the parent design.

---

## 3. Architecture

```text
  operator browser                      operator shell (on the hub host)
        │ HTTPS                                │  agentmon-hubd server approve/revoke/rm/list
        ▼                                      ▼  (direct DB access, like `user set-password`)
  ┌──────────────────────────── agentmon-hubd ────────────────────────────┐
  │  open (no RequireAuth), rate-limited:                                  │
  │    GET  /install.sh           → bash installer, templated w/ hub URL   │
  │    GET  /dl/agent-linux-{amd64,arm64}  → embedded agent binary         │
  │    POST /api/v1/enroll        → create `pending` server, return creds  │
  │  authed /api/v1 (M3) now reads the DB registry (status=active only)    │
  │  DB: servers(status,bearer,signing_key,…)  ·  audit_log                │
  └───────────────────────────────────────────────────────────────────────┘
        ▲  curl | sudo bash                    ▲  bearer (per server, hub→agent; only when active)
        │  (one-time, at install)              │
  ┌─────┴───────────────┐                ┌─────┴───────────────┐
  │ agentmon-agent srv-A │   …enroll…    │ agentmon-agent srv-B │
  │ (passive; serves)    │               │ (passive; serves)    │
  └──────────────────────┘                └──────────────────────┘
```

Rules unchanged from the parent design: browsers reach only the hub; agents are LAN-internal; the agent
makes no user-authorization decisions. New: the three onboarding endpoints are the **only**
unauthenticated surface besides `/healthz` and login, and they are LAN-only + rate-limited.

---

## 4. Components

### 4.1 Dynamic registry (`hubd/internal/registry`)
- `New(db)` replaces `New(cfg.Servers)`. `List()`/`Get(id)` return only **`status='active'`** servers, so
  the hub never lists or dials a `pending`/`revoked` server. `Get` of a non-active id → not found (404).
- **The registry reads the DB live** (per lookup), NOT a boot-time snapshot — so a CLI `approve`/`revoke`/
  `rm` (a separate process writing the shared WAL DB) takes effect on the running hub **without a
  restart**. (Per-request DB reads are trivial at single-user scale; no caching/invalidation needed.)
- The hub→agent client is unchanged; it dials with the server's **stored bearer** (now from the DB row,
  not a config `token_ref`).
- A repo layer (`db` package) gains: `EnrollServer`, `GetServer`, `ListServers(statusFilter)`,
  `SetServerStatus`, `DeleteServer`, `TouchServerLastSeen`.

### 4.2 Enroll handler (`hubd/internal/api` or `internal/enroll`)
`POST /api/v1/enroll` — open, rate-limited by client IP (reuse the M3 `authn.Limiter` + `authn.ClientIP`):
- Request `{hostname, os, arch, agentVersion, target:{osUser,socket,label}}`.
- Generate `serverId` (default = `hostname`); generate a random **bearer** + **signing key** (32 bytes
  each, base64url). Insert a `servers` row with `status='pending'`. Audit `server.enroll` (no secrets in
  the audit row).
- Respond `{serverId, bearer, signingKey}` over HTTPS.
- **Duplicate id** (a server with that id already exists) → `409 {"error":"already enrolled; revoke + rm first, or pass --hostname"}`. Bad body → 400.
- The bearer/signing key persist across `pending → active` (no re-issue at approval).

### 4.3 Installer (`GET /install.sh`) + binary hosting (`GET /dl/...`)
- `/install.sh` returns a bash script **templated at request time with the hub's `external_origin`** so
  the downloaded script already knows its hub. It also carries the expected **sha256** of each arch
  binary (baked at hub-build time). Script steps:
  1. Require root; parse `--hostname=`, `--user=`, `--socket=` overrides.
  2. Detect arch (`uname -m` → `amd64`/`arm64`); unsupported → clear error.
  3. Download `/dl/agent-linux-<arch>` → verify sha256 → install to `/usr/local/bin/agentmon-agent`.
  4. Identity = `--hostname` or `hostname -s`; run-as = `--user` or the invoking login user
     (`${SUDO_USER:-$(id -un)}`).
  5. `POST /api/v1/enroll` → receive `{serverId, bearer, signingKey}`.
  6. Write `/etc/agentmon/agent.toml` (listen on the host LAN IP:8377, `server_id`, `hub_token =
     "file:/etc/agentmon/hub_token"`, `directive_key = "file:/etc/agentmon/signing_key"`, one default
     target); write the two secret files **root-600, owned by the run-as user**.
  7. Install the systemd unit (`User=<run-as>`), `daemon-reload`, `enable --now`.
  8. Smoke-test the agent's local `/healthz`; print `✓ <hostname> enrolled — pending approval. Run
     'agentmon-hubd server approve <hostname>' on the hub.`
- `/dl/agent-linux-{amd64,arm64}` serves the embedded binary (`//go:embed` or image `COPY`), set with a
  sensible `Content-Type`/`Content-Length`. Built by CI cross-compiling both arches into the hub image
  (multi-stage Dockerfile).
- A `--dry-run` flag prints the planned actions without touching the system (for testing/inspection).

### 4.4 Admin CLI (`agentmon-hubd server …`)
Same binary, **direct DB access** (the established `user set-password` pattern; SQLite WAL allows the CLI
and the running hub to share the DB). Run on the hub host or via `docker compose exec hub …`:
- `server list` — id, name, hostname, status, os/arch, agent version, last-seen.
- `server approve <id|hostname>` — `status → active` (now listed + dialed). Audit `server.approve`.
- `server revoke <id|hostname>` — `status → revoked` (hub stops dialing; the stored bearer is no longer
  honored hub-side). Audit `server.revoke`.
- `server rm <id|hostname>` — delete the row. Audit `server.remove`.

### 4.5 Router + auth wiring
The three onboarding endpoints mount **open** (outside `RequireAuth`, like `/healthz` and login), each
behind the IP rate-limiter. `POST /api/v1/enroll` is exempt from the cookie/CSRF/origin machinery (no
cookie is involved). Everything else in `/api/v1` is unchanged from M3 (authed).

---

## 5. Contracts

### 5.1 Enroll
```text
POST /api/v1/enroll
  → {hostname, os, arch, agentVersion, target:{osUser,socket,label}}
  ← 200 {serverId, bearer, signingKey}      # over HTTPS; agent writes to root-600 files
  ← 409 {"error":"already enrolled; ..."}    # duplicate id
  ← 429 {"error":"too many attempts"}        # rate-limited (per client IP)
  ← 400 {"error":"bad request"}
```

### 5.2 `servers` table (migration `0002_enrollment.sql`, wrapped in a transaction)
The M0 `servers` table was static-config-shaped (`token_ref TEXT NOT NULL`, `name UNIQUE`). `0002`
reshapes it for enrollment. Nothing is deployed, so `0002` drops and recreates it:
```sql
DROP TABLE IF EXISTS servers;
CREATE TABLE servers (
  id            TEXT PRIMARY KEY,            -- default = hostname
  name          TEXT NOT NULL,              -- display; default = hostname
  hostname      TEXT NOT NULL,
  url           TEXT NOT NULL,              -- http://<lan-ip>:8377
  status        TEXT NOT NULL DEFAULT 'pending',  -- pending | active | revoked
  bearer        TEXT NOT NULL,              -- hub→agent bearer (server-side secret)
  signing_key   TEXT NOT NULL,              -- HMAC directive key (used by M4)
  labels        TEXT,
  os            TEXT,
  arch          TEXT,
  agent_version TEXT,
  last_seen_at  TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
```
This is the first **non-idempotent** migration, so `migrate()` is upgraded to wrap each migration file in
a transaction (the long-deferred M0/M1/M2 carry-over item — finally landed here). `tmux_targets` stays
present-but-unpopulated (single default target, as in M3).

### 5.3 Config change
`config.yaml` loses its `servers:` block (now DB-driven). The hub config loader drops the `Servers`
field. `external_origin` is reused to template `/install.sh`. Everything else (listen, cookie, rate-limit,
trust_forwarded_proto) is unchanged.

### 5.4 Browser-safe projections
Unchanged from M3: `ServerSummary`/`ServerDetail` expose only `id/name/labels/enabled(=status==active)/
healthy` — **never** `bearer`, `signing_key`, or `url`. The enroll response is the *only* place a
generated secret crosses the wire, and only to the enrolling agent over HTTPS.

---

## 6. Security model

- The three onboarding endpoints are unauthenticated **by necessity** (the agent has no credentials until
  it enrolls). Mitigations: **LAN-only** deployment (decided), **rate-limiting** per client IP, and the
  **approval gate** (a stranger who reaches `/enroll` only lands in the pending list and is rejected).
- Generated bearer + signing key are 32-byte CSPRNG values; stored in the hub's root-600 SQLite and (on
  the agent) root-600 files owned by the run-as user. Never logged, never in audit rows, never in
  browser-facing projections.
- Binary integrity: the installer verifies the downloaded binary's **sha256** against the value baked into
  the hub-served script.
- Audit: `server.enroll` (pending created), `server.approve`, `server.revoke`, `server.remove` — id +
  hostname + ip in the row, no secrets.
- A `revoked` server is dropped from the registry immediately (hub stops dialing). The orphaned agent
  keeps running but is unreachable from the hub; `rm` + uninstalling the agent fully removes it.

---

## 7. Testing strategy

- **Unit (CI, pure Go):** enroll handler (creates `pending` row, generates creds, audits, rate-limits,
  rejects duplicate id, 400 on bad body); DB repo (`EnrollServer`/`SetServerStatus`/`ListServers(filter)`/
  `DeleteServer`); registry-from-DB (List/Get filter to `active`); CLI `approve/revoke/rm/list`; the `0002`
  migration (columns exist; `migrate()` txn wraps and rolls back a deliberately-broken migration in a
  test).
- **Integration (httptest):** `POST /enroll` → DB `pending` → `approve` → `GET /servers/{id}/sessions`
  against a fake agent succeeds; a `pending` and a `revoked` server are **not** listed and **not** dialed;
  `/install.sh` returns a script containing the hub URL + checksums; `/dl/agent-linux-amd64` serves bytes.
- **Installer:** `shellcheck` clean; `--dry-run` prints the plan without side effects; a unit test asserts
  the templated script is well-formed (has the hub URL, both checksums, the enroll call).
- **Live acceptance (Patrik) — this REPLACES the manual agent-setup testing:** `docker compose up`;
  `curl https://hub.lan/install.sh | sudo bash` on **both** real servers; `agentmon-hubd server list`
  shows both `pending`; `server approve` each; `GET /servers/{id}/sessions` returns project-labelled
  sessions from both; `server revoke` one → it disappears from the API.

---

## 8. Milestone done-when

- `docker compose up` yields a hub that serves `/install.sh` + both arch binaries.
- One command on a box enrolls it as `pending`; `server approve` makes its sessions appear; `revoke`/`rm`
  remove it; all four operations audited.
- Registry reads the DB (`active` only); `config.yaml` no longer lists servers; the `0002` migration runs
  in a transaction on a fresh volume.
- Full unit + httptest suite green (`CGO_ENABLED=0`); `shellcheck` clean; the two-real-server live flow
  passes on Patrik's stack.

---

## 9. Open questions

1. **`install.sh` distro assumptions** — systemd is assumed (matches the M0 unit). Non-systemd hosts are
   out of scope; the script should detect-and-error rather than half-install. (Lean: detect `systemctl`,
   error clearly if absent.)
2. **`last_seen_at` updates** — populated on each successful hub→agent dial (cheap) or via a periodic
   health poll? Lean: stamp on successful `/sessions`/`/healthz` dials; no separate poller in this
   milestone.
3. **Binary size in the image** — two static agent binaries (~10–15 MB each) add ~30 MB to the hub image.
   Acceptable; revisit only if image size becomes a constraint.
