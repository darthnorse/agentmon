# M10 — New-session flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: subagent-driven-development / executing-plans. Steps use
> checkbox syntax. Executed via an ultracode Workflow (shared first; agent ‖ hub ‖ web; each implement
> pipelined into an adversarial verify).

**Goal:** Create a tmux session from the UI — `POST /sessions` across agent → hub → web, with §13.6 safety.

**Architecture:** Each layer mirrors its read-path twin (`Sessions`). The agent execs tmux via the existing
arg-array `Runner` (no shell). A single `shared.ValidateSessionName` rule is enforced at both the hub
(browser boundary) and the agent (exec boundary). The hub re-lists after create and returns the full
`Session` so the web opens the new terminal atomically.

**Tech stack:** Go (`net/http`, `os/exec` via the tmux Runner seam, `modernc.org/sqlite`), TS/React (Vite,
zustand, TanStack Query/Router), vitest.

## Global Constraints

- **Additive only.** No change to existing endpoints/wire shapes/the relay/the poller. New code only.
- **No shell interpolation, ever.** tmux is invoked through the existing `tmux.Runner`
  (`exec.CommandContext(ctx,"tmux",args...)`), positional args only. Reuse `with(...)` / `socketArgs(...)`.
- **One name rule:** `shared.ValidateSessionName` — `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` — used by hub + agent.
- **Custom commands rejected** in v1 (non-empty `command` → 400). Shell only.
- `CGO_ENABLED=0` keeps building; `-race` clean; `gofmt` clean on touched files.
- Commit footer on every commit: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- No prod touch in any unit test (no real tmux, no prod hub/agent). tmux integration tests use a throwaway
  `-L` socket and sit behind the existing integration-test guard.

## File Structure

- `shared/session.go` (modify) + `shared/session_test.go` — `CreateSessionRequest`/`CreateSessionResponse`,
  `ValidateSessionName`.
- `agent/internal/tmux/create.go` (new) + `create_test.go` — `CreateSession`, `ValidateCwd`, `ErrSessionExists`.
- `agent/internal/api/sessions.go` (modify) + `sessions_test.go` — `CreateSessionHandler`.
- `agent/internal/config/config.go` (modify) — `SessionDirs []string`.
- `agent/cmd/agentmon-agent/main.go` (modify) — register `POST /sessions`.
- `hubd/internal/authz/authz.go` (modify) + test — `SessionCreate` action.
- `hubd/internal/audit/audit.go` (modify) + test — `Recorder.SessionCreate`.
- `hubd/internal/registry/client.go` (modify) + test — `Client.CreateSession` + error sentinels.
- `hubd/internal/api/sessions.go` (modify) + `sessions_test.go` — `ServerCreateSessionHandler`.
- `hubd/internal/api/router.go` (modify) — register `POST /api/v1/servers/{id}/sessions`.
- `web/src/lib/contracts.ts` + `lib/api-client.ts` (+ test) — types + `createSession`.
- `web/src/components/NewSessionForm.tsx` (new) + test; `web/src/routes/index.tsx` (modify) — mount + wiring.

## Parallelization map

Task 1 (shared) FIRST (both Go modules depend on it). Then parallel: **agent chain** 2→3 (+main route),
**hub chain** 4→5→6 (+router), **web** 7. Each implement → adversarial verify (HARD on 1/2/3/6).

---

### Task 1 — shared: types + ValidateSessionName (HARD)

**Files:** `shared/session.go` (modify), `shared/session_test.go` (modify).
**Produces:**
- `type CreateSessionRequest struct { Name string \`json:"name"\`; Cwd string \`json:"cwd,omitempty"\`; Command string \`json:"command,omitempty"\` }`
- `type CreateSessionResponse struct { Name string \`json:"name"\` }`
- `func ValidateSessionName(name string) error`

- [ ] **Step 1: failing test** (`session_test.go`): a table for `ValidateSessionName` — accept `"dockmon"`,
  `"streammon-api"`, `"a"`, `"A_b-9"`, 64-char; reject `""`, `"-leading"`, `".dot"`, `"has space"`,
  `"a/b"`, `"a:b"`, `"a.b"`, 65-char, `"foo!"`.
- [ ] **Step 2: run → fail** (`go test ./shared/ -run ValidateSessionName -v`).
- [ ] **Step 3: implement** — `var sessionNameRe = regexp.MustCompile(\`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$\`)`;
  `ValidateSessionName` returns a descriptive error when it doesn't match; add the two structs.
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(shared): CreateSession types + ValidateSessionName (M10 T1)`.

---

### Task 2 — agent tmux: CreateSession + ValidateCwd (HARD)

**Files:** `agent/internal/tmux/create.go` (new), `agent/internal/tmux/create_test.go` (new).
**Read first:** `agent/internal/tmux/runner.go` (`Runner`, `ExecRunner`), `discovery.go` (`with`,
`socketArgs`), `control.go:42-51` (the `ValidatePaneID` injection-defense precedent),
`discovery_integration_test.go:28` (the `new-session -d -s … -c …` arg template).
**Produces:**
- `var ErrSessionExists = errors.New("session already exists")`
- `func CreateSession(ctx context.Context, run Runner, socket, name, cwd string) error`
- `func ValidateCwd(cwd string, allowed []string) (resolved string, err error)` — **exported** (the agent
  REST handler in T3 calls it; production passes `cfg.SessionDirs`)

- [ ] **Step 1: failing tests** (`create_test.go`):
  - `CreateSession` with a fake `Runner` that records args → asserts the runner received exactly
    `["-L", socket, "new-session", "-d", "-s", name, "-c", cwd]` (via `with`/`socketArgs`); returns nil.
  - Fake Runner returning `(out="duplicate session: proj", err=exitErr)` → `CreateSession` returns
    `ErrSessionExists`.
  - `ValidateCwd`: with `allowed=[t.TempDir()]`: a subdir of it → ok (resolved abs); `""` → defaults to
    `allowed[0]`; a path outside (`/etc`) → error; a relative path → error; a non-existent path → error;
    a `..` traversal that escapes the root → error.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** `create.go`:
  ```go
  func CreateSession(ctx context.Context, run Runner, socket, name, cwd string) error {
      out, err := run(ctx, with(socketArgs(socket), "new-session", "-d", "-s", name, "-c", cwd)...)
      if err != nil {
          if bytes.Contains(bytes.ToLower(out), []byte("duplicate session")) { return ErrSessionExists }
          return fmt.Errorf("tmux new-session: %w: %s", err, bytes.TrimSpace(out))
      }
      return nil
  }
  func ValidateCwd(cwd string, allowed []string) (string, error) {
      if len(allowed) == 0 { return "", errors.New("no session_dirs configured") }
      if cwd == "" { cwd = allowed[0] }
      if !filepath.IsAbs(cwd) { return "", fmt.Errorf("cwd must be an absolute path") }
      resolved, err := filepath.EvalSymlinks(filepath.Clean(cwd))
      if err != nil { return "", fmt.Errorf("cwd not found: %w", err) }
      if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() { return "", fmt.Errorf("cwd is not a directory") }
      for _, root := range allowed {
          r, err := filepath.EvalSymlinks(filepath.Clean(root))
          if err != nil { continue }
          if resolved == r || strings.HasPrefix(resolved, r+string(filepath.Separator)) { return resolved, nil }
      }
      return "", fmt.Errorf("cwd %q is outside the allowed session_dirs", cwd)
  }
  ```
  (Confirm `with`/`socketArgs` signatures against discovery.go; match them exactly.)
- [ ] **Step 4: run → pass.** Also add a tmux **integration** test behind the same build/skip guard the other
  `*_integration_test.go` use: create on a throwaway `-L agentmon-m10-itest` socket, then `list-sessions`
  shows the name; kill the socket in cleanup.
- [ ] **Step 5: commit** — `feat(agent): tmux CreateSession + cwd allow-list validation (M10 T2)`.

---

### Task 3 — agent REST: CreateSessionHandler + config (HARD)

**Files:** `agent/internal/api/sessions.go` (modify), `agent/internal/api/sessions_test.go` (modify),
`agent/internal/config/config.go` (modify), `agent/cmd/agentmon-agent/main.go` (modify).
**Read first:** `agent/internal/api/sessions.go` `SessionsHandler(cfg, discover Discoverer, m)` — it injects
a `Discoverer` func (NOT a raw Runner) and gets the socket from `cfg.ResolveTarget(label) (Target, bool)` →
`Target.SocketName` / `Target.Label`. `agent/internal/api/bearer.go` `RequireBearer`,
`agent/cmd/agentmon-agent/main.go` (how `SessionsHandler` is constructed + bound to `tmux.Discover`/
`tmux.ExecRunner`), `agent/internal/config/config.go` (config is **TOML**, `Target{SocketName toml:"socket_name"}`,
`ResolveTarget`).
**Consumes:** T1 `shared.CreateSessionRequest`/`ValidateSessionName`, T2 `tmux.CreateSession`/`ValidateCwd`.
**Produces:**
- `type SessionCreator func(ctx context.Context, socket, name, cwd string) error` (DI seam, mirrors
  `Discoverer`; production binds `func(ctx, socket, name, cwd){ return tmux.CreateSession(ctx, tmux.ExecRunner, socket, name, cwd) }`).
- `func CreateSessionHandler(cfg config.Config, create SessionCreator) http.HandlerFunc`.
- `config.SessionDirs []string \`toml:"session_dirs"\`` (TOML, not YAML).

- [ ] **Step 1: failing tests** (`sessions_test.go`, httptest + a fake `SessionCreator`): valid body → 200
  `{"name":...}` and the creator was called with `(socket=t.SocketName, name, cwd)`; bad name → 400;
  `command:"x"` → 400; cwd outside `SessionDirs` → 400; creator returns `tmux.ErrSessionExists` → 409;
  missing bearer → 401 (via RequireBearer).
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — decode with `http.MaxBytesReader`; `shared.ValidateSessionName(req.Name)` →
  400; `if req.Command != ""` → 400 "custom commands are not supported"; `t, ok := cfg.ResolveTarget(r.URL.
  Query().Get("target"))` → 404 if !ok (mirror `SessionsHandler`); `allowed := cfg.SessionDirs; if len==0 {
  home,_ := os.UserHomeDir(); allowed=[]string{home} }`; `cwd, err := tmux.ValidateCwd(req.Cwd, allowed)` →
  400; `err := create(r.Context(), t.SocketName, req.Name, cwd)`; `errors.Is(err, tmux.ErrSessionExists)` →
  409; other err → 500; else 200 `shared.CreateSessionResponse{Name: req.Name}`. Add `SessionDirs []string
  \`toml:"session_dirs"\`` to config (TOML). In main.go, build the `SessionCreator` (binding `tmux.
  CreateSession` + `tmux.ExecRunner`) and register `mux.Handle("POST /sessions", api.RequireBearer(cfg.
  HubToken, api.CreateSessionHandler(cfg, creator)))` next to the GET.
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(agent): POST /sessions handler + session_dirs config (M10 T3)`.

---

### Task 4 — hub authz + audit (MEDIUM)

**Files:** `hubd/internal/authz/authz.go` (modify) + `authz_test.go`, `hubd/internal/audit/audit.go`
(modify) + `audit_test.go`.
**Read first:** `authz.go:17-23` (action enum), `audit/audit.go:25` (`write`), `:47` (`TerminalOpen`).
**Produces:** `SessionCreate Action = "session.create"`; `func (r *Recorder) SessionCreate(ctx, principalID,
resource, sessionName, ip, ua string)`.

- [ ] **Step 1: failing tests:** `Authorize(p, SessionCreate, "server:x")` allows for a non-empty principal;
  `Recorder.SessionCreate` writes a row with action `"session.create"`, the resource, and the session name
  in `Meta` (mirror the `TerminalOpen` audit test).
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — add the enum constant; add the recorder method modeled on `TerminalOpen`
  (put `sessionName` into the `meta` JSON).
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(hub): session.create authz action + audit (M10 T4)`.

---

### Task 5 — hub → agent client: CreateSession (MEDIUM)

**Files:** `hubd/internal/registry/client.go` (modify), `client_test.go` (modify).
**Read first:** `client.go:24` `Sessions` (request build, Bearer, decode, error handling).
**Consumes:** T1 shared types.
**Produces:** `var ErrInvalidSession`, `var ErrSessionExists` (registry sentinels);
`func (c *Client) CreateSession(ctx, srv db.Server, target string, req shared.CreateSessionRequest) (shared.CreateSessionResponse, error)`.

- [ ] **Step 1: failing tests** (httptest agent): 200 → decodes `{name}`; 400 → `ErrInvalidSession`; 409 →
  `ErrSessionExists`; 5xx/transport → a wrapped error. Assert the outgoing request: `POST .../sessions?target=…`,
  `Authorization: Bearer <srv.Bearer>`, JSON body round-trips.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — mirror `Sessions`: build the POST with `url.Values{"target"}`, set Bearer +
  `Content-Type: application/json`, `json.NewEncoder(body)`; switch on `resp.StatusCode` to map 400/409 to
  the sentinels and decode 200.
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(hub): registry client CreateSession (M10 T5)`.

---

### Task 6 — hub handler: ServerCreateSessionHandler (HARD)

**Files:** `hubd/internal/api/sessions.go` (modify), `hubd/internal/api/sessions_test.go` (modify),
`hubd/internal/api/router.go` (modify).
**Read first:** `sessions.go:38` `ServerSessionsHandler` (authz, `Reg.Get`, `Agent.Sessions`, `overlayState`,
`TouchLastSeen`), `router.go:31`, the `AgentLister`/`Deps.Agent` interface (add `CreateSession` to it).
**Consumes:** T4 `authz.SessionCreate` + `Audit.SessionCreate`, T5 `Agent.CreateSession` + sentinels.
**Produces:** `func (d Deps) ServerCreateSessionHandler() http.HandlerFunc`; the `Deps.Agent` interface gains
`CreateSession(...)`.

- [ ] **Step 1: failing tests** (httptest + fake agent client + fake audit): without CSRF → 403; valid +
  CSRF → 201 returning the re-listed `Session` (with a pane id) and an audit `session.create` row recorded;
  bad name → 400 (before hitting the agent); agent `ErrSessionExists` → 409; agent `ErrInvalidSession` →
  400; agent transport error → 502.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — `id := r.PathValue("id")`; decode; `shared.ValidateSessionName(req.Name)` →
  400; `p, ok := d.authorizeOr403(w, r, authz.SessionCreate, "server:"+id)`; `srv, ok := d.Reg.Get(...)` →
  404; `target := r.URL.Query().Get("target")`; `resp, err := d.Agent.CreateSession(ctx, srv, target, req)` →
  map `ErrSessionExists`→409, `ErrInvalidSession`→400, other→502; re-list `d.Agent.Sessions(ctx, srv,
  target)`, find by `resp.Name`, apply `overlayState` (mirror `ServerSessionsHandler`); `d.Audit.
  SessionCreate(ctx, p.ID, "session:"+id+"/"+target+"/"+resp.Name, resp.Name, clientIP(r), r.UserAgent())`;
  `d.Reg.TouchLastSeen(ctx, id)`; `writeJSON(w, 201, sess)`. Add `CreateSession` to the `Deps.Agent`
  interface + register the route in `router.go`.
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(hub): POST /servers/{id}/sessions handler (authz, audit, re-list) (M10 T6)`.

---

### Task 7 — web: createSession + NewSessionForm (MEDIUM)

**Files:** `web/src/lib/contracts.ts` (modify), `web/src/lib/api-client.ts` (modify) + `api-client.test.ts`,
`web/src/components/NewSessionForm.tsx` (new) + test, `web/src/routes/index.tsx` (modify).
**Read first:** `api-client.ts` (`request`, `listSessions`, auto-CSRF), `store/panes.ts` (`openPane`,
`OpenPane`), `components/SessionList.tsx` (`flattenSessions` → first pane), `routes/index.tsx` (header +
desktop/mobile open paths), `lib/query-client.ts` (`queryClient`).
**Produces:** `contracts.CreateSessionRequest`; `api-client.createSession(serverId, body): Promise<Session>`;
`<NewSessionForm serverId target onCreated/>`.

- [ ] **Step 1: failing tests:** `api-client.test.ts` — `createSession` POSTs `/servers/{id}/sessions` with
  the body + `X-CSRF-Token` (mirror `postSeen`). `NewSessionForm.test.tsx` — submit disabled while the name
  is invalid (live-validated against the same charset); a valid submit calls `createSession`, and on success
  calls the `onCreated(session)` callback; a 409 shows an inline "name already exists" error.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — add the contract type; `createSession = (serverId, body) => request<Session>(
  "POST", \`/servers/${encodeURIComponent(serverId)}/sessions\`, body)`; build `NewSessionForm` (name input
  with a client-side regex mirror `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`, optional cwd, submit). Mount it in the
  `routes/index.tsx` header; `onCreated(session)` → resolve `session.windows[0].panes[0].id` and open:
  desktop `usePanes.getState().openPane({serverId, paneId, target, session: session.name, serverName,
  state: session.state})`; mobile `navigate({to:"/t/$serverId/$paneId", params, search:{target, session}})`;
  then `queryClient.invalidateQueries({queryKey:["sessions", serverId]})`.
- [ ] **Step 4: run → pass.** Full `npx vitest run` + `tsc --noEmit` green.
- [ ] **Step 5: commit** — `feat(web): New session form + createSession + auto-open (M10 T7)`.

---

## Self-Review (plan vs spec)

- **Coverage:** §3 contracts → T1 (shapes) + T3 (agent) + T6 (hub); §4 per-layer → T1–T7; §6 security →
  T1 (name), T2 (cwd allow-list + arg array), T3 (command reject + bearer), T4 (authz/audit), T6 (CSRF/authz/
  audit). §13.6 each bullet mapped (sanitize=T1, dirs=T2, no-shell=T2 Runner, arg-arrays=T2, audit=T4/T6).
- **Type consistency:** `CreateSessionRequest`/`CreateSessionResponse` (T1) used identically in T3/T5/T6/T7;
  `ValidateSessionName` (T1) called in T3 + T6 + the web mirror (T7); `ErrSessionExists` exists in BOTH the
  tmux package (T2) and the registry (T5) — distinct sentinels, mapped T3→agent-HTTP-409→T5-registry-
  `ErrSessionExists`→T6-HTTP-409 (documented, not a name clash within one package).
- **Placeholder scan:** the only "confirm against real code" notes (the `with`/`socketArgs` signatures, the
  Runner injection, exporting `ValidateCwd`) are explicit instructions, not gaps.

## Execution

ultracode Workflow: T1 first, then agent (T2→T3) ‖ hub (T4→T5→T6) ‖ web (T7), each implement pipelined into
an adversarial verify (HARD on T1/T2/T3/T6). Then opus whole-branch → `/multi-review --codex` → SAFE
acceptance (throwaway tmux socket + loopback hub + fresh DB) → local merge + `m10-carryover.md`.
