# AgentMon

Self-hosted fleet monitor + browser terminal for tmux-based agent sessions, plus a hub-native orchestrator that runs GitHub epics (plan → implement → review → PR → gate-merge). Go hub + agents, React/TS web UI.

## Layout
- `hubd/` — hub server: API, registry, embedded web UI, orchestrator. Go.
- `agent/` — per-host agent: tmux bridge, lifecycle hooks, epic runner. Go.
- `shared/` — types shared by hub + agent. Go.
- `web/` — the SPA (React 18, TS, Vite, Tailwind, TanStack Query/Router, xterm).
- `go.work` ties the three Go modules. `spike-0.5/` is a scratch spike — ignore it.

## Build & test (the gate)
- **Go:** `make test` (all 3 modules) — or `go test ./shared/... ./agent/... ./hubd/...`.
  If `GOCACHE` is read-only, prefix `GOCACHE=/tmp/agentmon-go-cache`.
- **Web:** `cd web && npm run typecheck && npm run test:run`.
  Add `npm run build` when you touch the service worker (`web/src/sw.ts`) or code it bundles.
- **Full artifact:** `make build` (embeds the SPA + cross-compiled agents into the hub binary).

## Conventions
- **Commits:** conventional prefixes (`feat(web):`, `fix(review):`, …). **Never** add a `Co-Authored-By:` / AI-attribution trailer.
- **`web/src/lib/contracts.ts` hand-mirrors the Go `shared` types** — change one, change the other; a new field must traverse DB → API → contract → UI.
- **Verdict JSON keys are CAPITALIZED:** the Go `Verdict` struct carries only yaml tags, so `json.Marshal` emits `Findings`/`Unresolved`/… — `parseVerdict` reads those, not lowercase.
- **Terminals are shared tmux sessions:** an attached browser client's resize frame resizes the REAL pane. Watch-only views (e.g. the board preview) must pass `readOnly` to `TerminalView`, which suppresses input, resize, and focus.

## Deploy
`docker compose up -d --build` from the repo root (prod is behind Caddy). When rolling out orchestrator/agent changes, **rebuild the hub first, then update agents** — a mixed fleet degrades gracefully but isn't wire-compatible mid-migration.
