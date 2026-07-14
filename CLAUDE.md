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
- **Platform requirements fail the gate closed:** `Decide` iterates
  `Project.Requirements` and escalates unless each id is reported `met` in the
  verdict's `Requirements` block (`json.Marshal` emits the CAPITALIZED
  `Requirements` key). `ParseVerdict` rejects out-of-domain `status`/`via`,
  empty ids, duplicate ids, and **multi-document blocks** (fail-closed, like a
  bad schema — it decodes exactly one YAML document and requires EOF, because
  `yaml.Unmarshal` silently drops everything after a `---` and a second document
  could smuggle a contradictory requirement past validation). A requirement
  added *after* an epic was imported — so the runner never reported it — fails
  **closed** as `(missing)`; that is the intended safe drift direction, not a bug.
- **Project `requirements` are the platform-invariant source of truth (epic #1):** each is `{id,text,check_cmd?}` stored as JSON in `projects.requirements` (TEXT, `NOT NULL DEFAULT '[]'`, mirrors `required_reviews`/`marshalStrings`). The `id` is a **server-derived, stable lowercase-kebab slug** (`normalizeRequirements`/`slugify`, `hubd/internal/api/requirements.go`): derived from `text` when absent, slugified (never re-derived from *edited* text) when supplied, and it is the **join key** the epic-02 gate/verdict match on — duplicate resolved ids are rejected (400). Snake_case DTO json tags (`id`/`text`/`check_cmd`), distinct from the CAPITALIZED Verdict caveat. The field is **inert**: epic-02 (gate reads it) and epic-03 (runner injects it + runs `check_cmd`) are the consumers.
- **Runner requirement carrier trust boundary (v1):** `plan-epics` copies exact
  platform requirement records into each private-repository epic issue body, and
  `epic-pipeline` executes a carried `check_cmd` verbatim. This is a conscious v1
  trust decision: issue editors are limited to the owner/runners and can already
  edit repository code the runner executes, matching `gate.go`'s owner/runner
  provenance assumption for PR verdict bodies. Hardening by executing commands
  from authoritative `Project.Requirements` or using signed delivery is deferred
  to v2; until then, never rewrite a carried command and always fail closed from
  its real exit status.
- **Terminals are shared tmux sessions:** an attached browser client's resize frame resizes the REAL pane. Watch-only views (e.g. the board preview) must pass `readOnly` to `TerminalView`, which suppresses input, resize, and focus.

## Deploy
`docker compose up -d --build` from the repo root (prod is behind Caddy). When rolling out orchestrator/agent changes, **rebuild the hub first, then update agents** — a mixed fleet degrades gracefully but isn't wire-compatible mid-migration.
