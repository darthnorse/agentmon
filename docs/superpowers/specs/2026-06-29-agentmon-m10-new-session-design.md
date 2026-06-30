# M10 — New-session flow (Phase 4b): create a session from the UI

## 1. Goal

Today a session can only be created by hand via `tmux` on the host. M10 adds **`POST /sessions`** end to end —
agent (tmux) → hub (endpoint, authz, audit) → web ("New session" form → create → open) — so the user can spin
up a project session from the dashboard. This is the §8.2 / §12.2 create path with the §13.6 safety rules.

M10 is the **second of three Phase-4 sub-milestones** (M9 alerts+PWA+push → **M10 new-session** → M11 polish).
It is greenfield on all three layers but each seam already has a read-path twin to mirror (`Sessions`).

## 2. Locked decisions (resolving design §18-Q6 conservatively for v1 single-user)

- **Name (required, sanitized, verbatim — §9.5/§13.6).** One rule in `shared`: `ValidateSessionName` accepts
  `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` — 1–64 chars, must start alphanumeric, only `A–Z a–z 0–9 _ -`. This
  excludes `.` `:` (tmux disallows them in session names), whitespace, slashes, and a leading `-` (tmux
  option confusion). Used by **both** the hub handler (early `400`) and the agent (authoritative, pre-exec).
- **Directory (optional, allow-listed).** The agent validates a requested `cwd`: it must be an **absolute,
  existing directory within an allowed root**. Allowed roots come from a new agent config `session_dirs`
  (list); when unset it defaults to the agent user's home (`os.UserHomeDir()`). Validation cleans + resolves
  symlinks (`filepath.EvalSymlinks`) and requires the result to be **within** an allowed root (prefix on a
  path-separator boundary) — blocks `..` traversal and symlink escape. Omitted `cwd` → the first allowed root.
- **Command — NOT supported in v1.** A non-empty `command` is **rejected** with `400` ("custom commands are
  not supported"), never silently dropped. Sessions start the agent user's default shell (tmux default).
  This avoids arbitrary command execution beyond the shell the user already has via the terminal. The field
  exists in the request type for forward-compat.
- **Authz/audit.** New action `authz.SessionCreate` ("session.create"; trivially allowed in v1, the real
  seam). The hub authorizes `session.create` on `server:<id>` and **audits `session.create`** (principal,
  resource `session:<server>/<target>/<name>`, name in `meta`, ip/ua) — design §13.5 requires it.
- **Hub returns the created session (not just `{name}`).** The agent returns `{name}` (per §12.2). The hub,
  on success, re-lists and returns the **full `Session`** (seen-projected, with `windows[0].panes[0].id`) so
  the web can open the new terminal atomically — no client refetch-timing race. Names are unique per tmux
  server, so look-up-by-name is unambiguous.
- **Web flow = create → auto-open.** Submit → `createSession` → on success open the returned session's first
  pane (desktop `openPane`; mobile navigate to `/t/...`) + invalidate `["sessions", serverId]`. Default the
  name field to the chosen `cwd` basename (§9.5).

## 3. Contracts (additive)

Agent (bearer-only REST, like `GET /sessions` — the signed directive is WS-only):
```
POST /sessions?target=<target>
  Authorization: Bearer <agent-token>
  body: { "name": "<required>", "cwd": "<optional abs dir>", "command": "<rejected if non-empty>" }
  → 200 { "name": "<name>" }
  → 400 invalid name / cwd outside allowed roots / non-empty command
  → 409 a session with that name already exists
```

Hub (cookie auth + CSRF, like `POST /seen`):
```
POST /api/v1/servers/{serverId}/sessions
  body: { "name", "cwd"?, "command"? }   (+ X-CSRF-Token)
  → 201 <Session>     (the created session, seen-projected, with windows/panes)
  → 400 invalid name/cwd/command (forwarded from the agent or caught early)
  → 409 duplicate name
  → 502 agent unreachable
```

Shared (`shared/session.go`): `CreateSessionRequest{ Name string; Cwd string \`json:"cwd,omitempty"\`;
Command string \`json:"command,omitempty"\` }`, `CreateSessionResponse{ Name string }`, and
`func ValidateSessionName(string) error`.

## 4. Architecture (per layer; each mirrors its read-path twin)

**shared** — `CreateSessionRequest`/`CreateSessionResponse` + `ValidateSessionName` (pure, the single
charset rule both hub and agent import).

**Agent — `tmux` package** (`agent/internal/tmux/`):
- `CreateSession(ctx, run Runner, opts CreateOpts) error` building `with(socketArgs(socket), "new-session",
  "-d", "-s", name, "-c", cwd)` via the existing `Runner` (`exec.CommandContext(ctx,"tmux",args...)` — arg
  array, no shell). Mirrors the proven shape in `discovery_integration_test.go`.
- `validateCwd(cwd string, allowed []string) (string, error)` — clean → abs → `EvalSymlinks` → must be an
  existing dir within an allowed root. Returns the resolved path or an error.
- Duplicate-name detection: tmux exits non-zero with "duplicate session"; `CreateSession` maps that to a
  sentinel `ErrSessionExists` so the handler can return `409`.

**Agent — REST** (`agent/internal/api/sessions.go` + `agent/cmd/agentmon-agent/main.go`):
- `CreateSessionHandler(cfg, run)` — decode body (MaxBytesReader), `shared.ValidateSessionName`, reject
  non-empty command, resolve target (`cfg.ResolveTarget`), validate cwd against `cfg.SessionDirs` (default
  `$HOME`), `tmux.CreateSession`, return `{name}` (or 400/409). Registered `mux.Handle("POST /sessions",
  RequireBearer(cfg.HubToken, ...))` next to the GET.
- `agent/internal/config/config.go`: add `SessionDirs []string \`yaml:"session_dirs"\``.

**Hub — client** (`hubd/internal/registry/client.go`): `CreateSession(ctx, srv, target string, req
shared.CreateSessionRequest) (shared.CreateSessionResponse, error)` — `POST srv.URL+"/sessions?target="`,
`Bearer srv.Bearer`, JSON body; maps agent 400/409 to typed errors (`ErrInvalidSession`, `ErrSessionExists`).

**Hub — handler** (`hubd/internal/api/sessions.go` + `router.go`):
- `ServerCreateSessionHandler()` — decode, `shared.ValidateSessionName` (early 400), `authorizeOr403(authz.
  SessionCreate, "server:"+id)`, `d.Reg.Get`, `d.Agent.CreateSession`, on success re-list (`d.Agent.
  Sessions`) + `overlayState` to build the created `Session`, `d.Audit.SessionCreate`, `TouchLastSeen`,
  `writeJSON(201, session)`. Map client errors → 400/409/502.
- `authz/authz.go`: add `SessionCreate Action = "session.create"`.
- `audit/audit.go`: add `Recorder.SessionCreate(ctx, principalID, resource, sessionName, ip, ua)` (modeled on
  `TerminalOpen`; name in `Meta`).
- `router.go`: `mux.Handle("POST /api/v1/servers/{id}/sessions", rd.Auth.RequireAuth(rd.API.ServerCreateSessionHandler()))`.

**Web** (`web/src/`):
- `lib/contracts.ts`: `CreateSessionRequest{name; cwd?; command?}`, response = the existing `Session`.
- `lib/api-client.ts`: `createSession(serverId, body) → POST /servers/{id}/sessions` (auto-CSRF).
- `components/NewSessionForm.tsx` (new): a dialog/popover with name (required, live-validated to the shared
  charset) + optional cwd; submit → `createSession` → auto-open + invalidate. Mounted from the shared header
  (`routes/index.tsx`, next to EnableAlerts/Sign-out) on both surfaces; takes the current `serverId` + target.
- Open: desktop `usePanes.openPane({serverId, paneId: s.windows[0].panes[0].id, target, session: s.name,
  serverName, state})`; mobile `navigate({to:"/t/$serverId/$paneId", params, search})`.

## 5. Data flow

Web form (name [+cwd]) → `POST /api/v1/servers/{id}/sessions` (cookie+CSRF) → hub authorizes `session.create`
+ validates name → `registry.Client.CreateSession` (Bearer) → agent `RequireBearer` → validate name/cwd/
command → `tmux new-session -d -s <name> -c <cwd>` on the agent's socket → `{name}` → hub re-lists, finds the
session, audits, returns `201 <Session>` → web opens its first pane + invalidates the sessions query.

## 6. Security (§13.6)

- **No shell interpolation:** tmux is invoked via the existing arg-array `Runner` (`exec.Command`, never a
  shell string). Name + cwd are positional args, not interpolated.
- **Name** constrained to a safe charset (`shared.ValidateSessionName`) at the hub AND re-validated at the
  agent (the exec boundary) — defense in depth, one shared rule.
- **Directory** restricted to an allow-list, symlink-resolved, traversal-blocked; never an arbitrary path.
- **Command** execution is not exposed in v1 (rejected), so create cannot run arbitrary programs.
- **Authz + CSRF + audit:** `authorize(session.create)` on the browser-facing endpoint (CSRF via
  `RequireAuth`); the agent stays bearer-gated; every create is audited with the session name in `meta`
  (design §13.5). The agent runs non-root (deployment, §13.6) — unchanged.

## 7. Testing — risk-tiered (Go + vitest; tmux integration where a socket is available)

- HARD: `shared.ValidateSessionName` (accept/reject table incl. leading `-`, `.`, `:`, spaces, slashes,
  length bounds, empty); agent `validateCwd` (abs/exists/within-root, `..` traversal, symlink escape,
  default-when-omitted) with a `t.TempDir()` allowed root.
- HARD: agent `CreateSessionHandler` (httptest + a fake `Runner`): valid → 200 `{name}` and the runner got
  `new-session -d -s <name> -c <cwd>` (arg array asserted); bad name → 400; cwd outside root → 400; non-empty
  command → 400; runner "duplicate session" → 409; bearer required.
- HARD: hub `ServerCreateSessionHandler` (httptest + fake agent client): authz + CSRF (403 without CSRF),
  early-400 on bad name, 201 returns the re-listed Session, audit row written, client 409/invalid → 409/400,
  client transport error → 502.
- MEDIUM: hub `registry.Client.CreateSession` (httptest agent) request shape + error mapping; `authz`
  SessionCreate allowed; `audit.SessionCreate` writes the expected row.
- MEDIUM (web): `createSession` posts the right body + CSRF; `NewSessionForm` validates the name live
  (disable submit on invalid), calls `createSession`, and on success opens the returned pane + invalidates;
  duplicate-name (409) surfaces an inline error.
- LIGHT: contracts; the agent `tmux.CreateSession` arg-array unit (fake Runner) + a tmux **integration**
  test behind the existing integration-test guard (throwaway socket) mirroring `discovery_integration_test`.

## 8. Acceptance (SAFE — needs a real tmux, so a THROWAWAY socket)

Per memory [[dev-host-runs-hub-and-claude]]: **never** the default tmux socket / session 0 / the `agentmon`
demo panes / `~/.claude` / `deploy/data`. Static: full Go suite (`-race`, vet, gofmt) + web (tsc, vitest,
build) green. Runtime: a **scratch agent on a throwaway socket** (`tmux -L agentmon-m10accept`) + a loopback
hub on a **fresh DB**; drive `POST /api/v1/servers/{id}/sessions` and confirm: bad name → 400, valid → 201
with a real pane id, the session appears on the **throwaway socket only** (`tmux -L agentmon-m10accept
ls`), duplicate → 409, cwd outside the allowed root → 400, audit `session.create` row present. Tear down the
throwaway socket; confirm prod hub Up, prod agent alive, default session 0 + demo panes intact, `deploy/data`
untouched. Web render itself is vitest-proxied (no headless browser) — flag the on-device "New session" tap
for the owner.

## 9. Scope boundaries — OUT of M10

- Custom start command / templates (§18-Q6) — rejected in v1; revisit later.
- Session kill/delete (`session.kill` action exists in the namespace but no UI) — later.
- Per-OS-user dir policy / multi-target creation — single-target `default`, single service account (§13.6).
- M11 polish (focus-next-blocked, prefs, theme/font, grid pivot, sectioned inbox, M8-deferred).

## 10. Build sequence (work-list → writing-plans → Workflow)

1. `shared`: `CreateSessionRequest`/`CreateSessionResponse` + `ValidateSessionName` (+ tests). (HARD on validate.)
2. Agent tmux: `CreateSession` + `validateCwd` + `ErrSessionExists` (+ fake-Runner + integration tests). (HARD.)
3. Agent REST: `CreateSessionHandler` + `config.SessionDirs` + route. (HARD.)
4. Hub authz + audit: `SessionCreate` action + `Recorder.SessionCreate` (+ tests). (MEDIUM.)
5. Hub client: `registry.Client.CreateSession` + error mapping. (MEDIUM.)
6. Hub handler: `ServerCreateSessionHandler` + route (authz, CSRF, re-list, audit). (HARD.)
7. Web: contracts + `createSession` + `NewSessionForm` + auto-open/invalidate wiring. (MEDIUM.)

Phasing for the workflow: agent chain (1→2→3) ‖ hub chain (1→4→5→6, after shared) — but shared (1) is a
shared dependency, so do task 1 first, then agent (2→3) ‖ hub (4→5→6), then web (7). Each implement pipelined
into an adversarial verify. Then opus whole-branch → `/multi-review --codex` → SAFE acceptance → merge +
carryover.
