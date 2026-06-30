# AgentMon

**A multi-server, mobile-first dashboard for supervising AI coding agents.**

AgentMon shows the live terminal sessions from every one of your development servers in one place, surfaces
the ones that need you (an agent waiting for input or approval) first, and lets you jump into any of them —
from your desktop or your phone — to answer, approve, or course-correct. When a session you're *not* looking
at gets blocked, AgentMon tells you: an in-app toast and sound, or a push notification when your phone is
asleep.

It's built for the common reality of running several Claude Code (or similar) sessions across several boxes:
status *is* navigation, and the thing you most need is "which agent needs me, and let me get to it now."

> **Status:** v1. Single-operator by design (each operator runs their own hub). LAN / private-network use —
> not built for public internet exposure. See [Security & network posture](#security--network-posture).

---

## Features

- **One hub, many servers.** A single browser/PWA UI aggregates `tmux` sessions from any number of servers,
  each running a lightweight agent. Agents are never browser-reachable — the hub is the only user-facing
  surface and the single authorization point.
- **Real terminals, real persistence.** Sessions are `tmux` sessions, so your agents keep running when you
  close the tab, your phone sleeps, the hub restarts, or you SSH in directly. Full scrollback, touch
  scrolling, and a mobile key bar (Esc / Ctrl / Tab / arrows / paste).
- **Live state, blocked-first.** Claude Code hooks drive per-session state — `blocked` / `done` / `working`
  / `idle` — rolled up to colored dots per session and server, with blocked sessions always floated to the
  top. "Next blocked" jumps you to the next one.
- **Cross-session attention alerts.** When a *different* session goes `blocked`, you get an in-app
  toast + sound + vibrate while AgentMon is open, and a **Web-Push** notification when it's backgrounded or
  your phone is asleep (install it as a PWA first — required on iOS).
- **Installable PWA** — add it to your home screen for a full-screen mobile terminal and push notifications.
- **Create sessions from the UI** — spin up a new project session on any server (see
  [`session_dirs`](#agent-agenttoml)).
- **Per-user preferences** — terminal theme + font size (separately tunable for desktop and mobile), and an
  optional "alert me when a session finishes" toggle.

---

## How it works

```text
        (your browser / phone)                        (internal LAN)

  You ───────────────────────▶  AgentMon HUB  ─────────────┬──▶ agentmon-agent @ server-A ──▶ tmux
 browser   HTTPS + WSS         single container            ├──▶ agentmon-agent @ server-B ──▶ tmux
 mobile/PWA                    hubd + embedded SPA          └──▶ agentmon-agent @ server-C ──▶ tmux
                               auth + relay + state
```

- **`agentmon-hubd`** — one container, anywhere on your LAN. Serves the web UI, authenticates you, relays
  terminal WebSockets to the agents, aggregates state, sends push notifications, and stores everything in a
  single embedded SQLite file. It's the only thing you expose to a browser.
- **`agentmon-agent`** — one small static Go binary per server, installed as a systemd service bound to the
  internal LAN. It drives `tmux` (via control mode), reports sessions and state to the hub, and mechanically
  enforces what the hub authorizes. Agents hold per-server secrets that never reach the browser.

The hub and agents live on the same internal network. How *you* reach the hub (on-site, or via your own VPN)
is your network's concern — AgentMon only assumes the hub is reachable by your browser and the agents are
reachable by the hub.

---

## Requirements

- **Hub:** Docker + Docker Compose (the hub ships as a single container). Put it behind a TLS reverse proxy
  (Caddy/nginx) for HTTPS, or terminate TLS however you like — cookies/CSRF want a stable origin.
- **Each server:** Linux with `systemd`, `tmux` (3.x), and `curl`. amd64 or arm64. A non-root service
  account is recommended.
- **For live agent state:** Claude Code (hooks are how AgentMon learns `blocked` / `done` / `working`).

---

## Quick start

### 1. Run the hub

```bash
git clone <this-repo> agentmon && cd agentmon
cp deploy/hub.config.example.yaml deploy/data/config.yaml   # then edit it (step 2)
docker compose up -d --build
```

The compose project name is pinned to `agentmon`, so `docker compose up -d --build` always rebuilds and
recreates the same container from the repo root. The config + SQLite DB live in `./deploy/data` (bind-mounted
to `/data`).

### 2. Configure the hub (`deploy/data/config.yaml`)

```yaml
# Where the hub binds. Direct LAN: 0.0.0.0:8378. Behind Caddy: 127.0.0.1:8080.
listen: "0.0.0.0:8378"

# The URL browsers + agents use to reach the hub (baked into the served installer,
# and enforced as the required Origin for logins / mutating requests).
external_origin: "https://agentmon.example.lan"

# true ONLY behind a trusted TLS proxy that sets X-Forwarded-Proto/For; then `listen`
# above MUST be loopback/firewalled. false for direct LAN (http).
trust_forwarded_proto: true

data_dir: "/data"
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit:  { max_attempts: 5,  window: "15m" }
enroll_rate_limit: { max_attempts: 30, window: "1m" }

# Optional. Web-Push needs a VAPID "subject" (a mailto:/URL contact); defaults to external_origin.
# vapid_subject: "mailto:you@example.com"
```

Apply config-only changes with `docker compose restart agentmon-hub` (config is read at startup).

### 3. Create your login

```bash
docker compose exec agentmon-hub /agentmon-hubd user set-password --username you --config /data/config.yaml
# prompts for a password on stdin (or set AGENTMON_PASSWORD)
```

Open `external_origin` in a browser and log in.

### 4. Install an agent on each server

The hub serves a templated installer. On each box you want to monitor:

```bash
curl <external_origin>/install.sh | sudo bash
# optional: --hostname=H --user=U --socket=S --dry-run
```

It downloads the right binary (checksum-verified), enrolls with the hub, and installs + starts the
`agentmon-agent` systemd unit. `--dry-run` shows exactly what it would do without changing anything. The
installer is **idempotent**: re-running it on a host that's already installed just swaps the binary in place
and restarts (keeping its enrollment + config) — so the same command is also the [upgrade path](#updating).
(Prefer a manual install? See `deploy/agent.example.toml` and `deploy/agentmon-agent.service`.)

### 5. Admit the agent

Enrolled agents are pending until you approve them:

```bash
docker compose exec agentmon-hub /agentmon-hubd server list
docker compose exec agentmon-hub /agentmon-hubd server approve <hostname>
```

The server now appears in the UI with its live sessions. (`server revoke` / `server rm` to undo.)

### 6. Enable Claude Code state (hooks)

For live `blocked` / `done` / `working` dots, install the agent's hooks into Claude Code on that server:

```bash
agentmon-agent hooks install --settings ~/.claude/settings.json   # or a project .claude/settings.json
agentmon-agent hook-test --session myproject --event PermissionRequest   # verify
```

Without hooks, sessions still show and are fully usable — they just read `unknown` instead of live state.

---

## Configuration reference

### Hub (`config.yaml`)

| Key | Purpose |
|---|---|
| `listen` | Bind address. Loopback when behind a TLS proxy. |
| `external_origin` | The URL browsers/agents use; enforced as the required `Origin`. |
| `trust_forwarded_proto` | `true` only behind a trusted proxy that sets `X-Forwarded-*`. |
| `data_dir` | Where `agentmon.sqlite` + `config.yaml` live (bind-mounted). |
| `session_cookie` | `{ name, ttl }` for the login cookie. |
| `login_rate_limit` / `enroll_rate_limit` | `{ max_attempts, window }`. |
| `vapid_subject` | Web-Push contact (`mailto:`/URL); defaults to `external_origin`. |

### Agent (`agent.toml`)

Usually written by the installer at `/etc/agentmon/agent.toml`. Key fields:

```toml
listen        = "0.0.0.0:8377"          # LAN interface the hub dials
server_id     = "web-01"
hub_token     = "file:/etc/agentmon/hub_token"      # per-server bearer secret
directive_key = "file:/etc/agentmon/signing_key"    # HMAC key for relay directives
scrollback_lines = 5000

[[targets]]
  os_user     = "dev"     # which OS user's tmux
  socket_name = ""        # tmux -L socket (empty = that user's default socket)
  label       = "default"

# Directories in which "New session" is allowed to create a session's working dir.
# Optional; defaults to the agent user's home. See "Creating a session" below.
session_dirs = ["/home/dev/projects", "/srv/work"]
```

> **`session_dirs`** is the allow-list of root directories a UI-created session may start in. When you create
> a session and choose a working directory, the agent requires it to be an existing directory **inside** one
> of these roots (symlinks resolved, `..` traversal blocked) before it runs `tmux new-session -c <dir>`. If
> you omit a directory, the first root is used; if `session_dirs` is unset, it defaults to the agent user's
> `$HOME`. This keeps session creation confined to directories you explicitly sanction rather than anywhere
> on the filesystem. Custom start *commands* are not exposed in v1 — new sessions start your default shell.

---

## Using AgentMon

- **Desktop** — a sidebar tree (servers → sessions, with rollup dots, blocked-first) plus a tiled terminal
  grid (open tiles stay live so scrollback survives). **Next blocked** (button or the `n` key) jumps you to
  the next session needing attention.
- **Mobile** — an attention inbox sectioned into **Needs attention / Done / Working / Idle**, tap into a
  full-screen terminal with the key bar, answer, and move on. Add it to your home screen (PWA) for the full
  viewport and push notifications.
- **Creating a session** — "New session", pick the server, name it (the name becomes the `tmux` session name
  and the project label), optionally choose a directory (constrained to `session_dirs`), and it opens.
- **Attention alerts** — on by default in-app (toast + sound + vibrate) for sessions going `blocked`. Tap
  **Enable alerts** to grant notification permission and receive **Web-Push** when AgentMon is
  backgrounded/asleep. On iOS you must add AgentMon to your home screen first (Apple requires an installed
  PWA for push).
- **Settings** (gear icon) — terminal theme + font size (desktop and mobile separately), and an optional
  "alert when a session finishes (done)" toggle.

---

## Updating

There's no auto-update — agents keep running their current binary until you update them. Update the **hub
first** (so it serves the new agent binary), then the agents.

- **Hub:** `docker compose up -d --build` — rebuilds + recreates the container, re-embeds the latest agent
  binaries, and re-bakes their checksums into the served `install.sh`.
- **Agents:** re-run the same installer on each host. It detects the existing install and swaps the binary in
  place — **no re-enroll, config + secrets preserved** — or reports "already up to date":
  ```bash
  curl <external_origin>/install.sh | sudo bash
  ```
  (To force a clean re-enroll instead — e.g. after wiping the hub — `rm -rf /etc/agentmon` first, then run it.)

---

## Security & network posture

AgentMon is remote shell access — treat it like SSH.

- **LAN / private network only.** No public internet exposure in v1. Front it with a TLS proxy reachable only
  on your network (or your own VPN).
- **The hub is the only browser-facing surface.** Agents listen on the internal LAN only and are never
  exposed to browsers. Per-server agent tokens and signed relay directives never reach the client bundle.
- **One enforcement point.** Every request resolves to a principal and passes through a single
  `authorize()` chokepoint (single-operator in v1; the seam keeps multi-user/SSO additive later). Login is
  rate-limited; the local password is stored as a hash. Cookies are HttpOnly; CSRF + Origin checks guard
  mutating requests and WebSocket upgrades.
- **Run agents non-root.** Prefer a dedicated service account; constrain `session_dirs`.
- Single-operator by design: a team runs multiple AgentMon instances rather than roles inside one hub.

---

## Development

```bash
make test                 # Go unit tests across all modules
make build                # build the SPA + both Go binaries (CGO_ENABLED=0)
cd web && npm test        # web (vitest)
cd web && npm run dev     # SPA dev server, proxies /api + /ws to a local hubd
```

Repo layout:

| Path | What |
|---|---|
| `shared/` | Wire contracts shared by hub + agent (`agentmon/shared`) |
| `agent/` | Per-server `agentmon-agent` (`agentmon/agent`) |
| `hubd/` | Central `agentmon-hubd` control plane (`agentmon/hubd`); embeds the SPA |
| `web/` | Vite + React + TypeScript SPA |
| `deploy/` | Dockerfile, compose, Caddy + systemd examples |
| `docs/superpowers/` | Phase specs + carryovers |
| `agentmon-design.md` | The full design spec |

---

## License

See [`LICENSE`](LICENSE).
