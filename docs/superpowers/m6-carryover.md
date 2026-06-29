# M6 → next carry-over (agent-side Claude Code state detection — Phase 3a)

M6 (the **agent half** of Phase 3 — Claude Code state via hooks) is complete, reviewed (per-task +
opus whole-branch + `/multi-review --codex`), **live-accepted on this host**, and merged. M6 is the
first of three Phase-3 sub-milestones: **M6 agent → M7 hub → M8 web**. This captures decisions,
deferrals, and the M7 handoff.

Branch: `phase-3-m6-agent-state`, off `main@725be73`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m6-agent-state-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m6-agent-state.md`. Ledger: `.superpowers/sdd/progress.md`.
Hook-contract spike evidence: `scratchpad/hook-spike/FINDINGS.md` (Claude Code **v2.1.195**, tmux 3.5a).

## The verified hook contract (de-risked first, design §18-Q3 resolved)
- settings schema: `{"hooks":{"<Event>":[{"matcher":...,"hooks":[{"type":"command","command":...}]}]}}`;
  the command gets the event JSON on **stdin** and **inherits the pane env**. `claude --settings <f>`
  loads additively without touching `~/.claude/settings.json`.
- Events that fire (v2.1.195): `SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Notification,
  PermissionRequest, Stop, SubagentStop, SessionEnd`. Design guesses that DON'T exist: `SubagentStart,
  StopFailure, PreCompact`. `PermissionRequest` DOES exist. Every payload carries `session_id`
  (Claude UUID, not tmux), `transcript_path`, `cwd`, `hook_event_name`.
- **Correlation:** the payload has NO tmux pane/session. The installed hook **command** reads
  `$TMUX_PANE` (pane id) and `$TMUX` (`<socket-path>,<pid>,<idx>`) and passes them as the
  `X-AgentMon-Pane` / `X-AgentMon-Tmux` headers; the agent maps socket→target and uses the pane id
  directly. No `cwd` fuzzy-matching.

## Locked decisions
- **Pull-only seam (no agent→hub push).** The agent exposes state on `GET /sessions` (additive
  `Session.State`); the hub (M7) reads it via the existing pull. The agent runtime has no outbound
  hub client and no hub address — building push was rejected as a new trust direction unjustified
  while real-time alerts are Phase 4.
- **Agent emits only the 5 global states** (`blocked/done/working/idle/unknown`). The per-principal
  `done→idle` "seen" projection (§9.3) is **hub-side (M7)** — the agent has no "seen" concept.
- **Hooks are opt-in:** `POST /hook` is mounted only when `hook_token` is set.
- **`/hook` never breaks Claude:** soft-drop → `204`; only `401` (bad token), `403` (non-loopback).
  Tolerant parse (unknown fields/events ignored). Body bounded 1 MiB.
- **Installer:** `hooks print` (stdout, no writes, default) + `hooks install|uninstall --settings
  <PATH>` (idempotent tagged-marker merge). **No implicit `~/.claude` path** — can't accidentally hit
  the live agent's settings. Token delivered via a `0600` file (`hook_token_file`) or baked literal
  (with a stderr warning). `hook-test` exercises the loop.
- **State machine:** pure, in-memory, mutex-guarded, keyed by `(target, pane)`; `SessionStart→idle`,
  prompt/tool→`working`, `PermissionRequest`/`Notification(permission)`→`blocked`, `Notification`
  (other)/`Stop`→`done`, `SubagentStop`/unknown-event→preserve, `SessionEnd`→evict the pane entry.

## What shipped (8 SDD tasks, TDD throughout)
`shared.State`+`RollUp`+`Session.State` (the M6/M7/M8 contract) · `agent/internal/state` (machine) ·
`agent/internal/config` `hook_token`/`hook_token_file` · `agent/internal/hooks` (`POST /hook` intake +
`RequireHookAuth`/`RequireLoopback` + installer `Command/Snippet/Merge/Unmerge/LoadSettings/
SaveSettings/WriteTokenFile/InstallWarnings`) · `GET /sessions` state stamping · `hooks`/`hook-test`
CLI. Hub/web untouched (additive field only).

## Review path
Per-task SDD reviews on all 8 (spec + quality), all Approved. Opus **whole-branch review**: Ready to
merge YES. **`/multi-review --codex`** (feature-dev:code-reviewer + code-simplifier + deep-scan +
codex gpt-5.5) — fixed everything but nitpicks (per product owner), each re-reviewed clean.

### Fixes applied from review (each test-locked unless noted)
- **HIGH security (specialist):** loopback check ran AFTER token auth → a remote caller got `401` on a
  wrong token vs `403` on the right one = **token-validity oracle**. Fixed: `RequireLoopback` runs
  OUTERMOST (`RequireLoopback(RequireHookAuth(HookHandler))`); non-loopback → `403` regardless of
  token. Regression test added.
- **Security (codex + deep-scan, cross-model):** `Command()` shell-single-quotes the hook token and
  token-file path (no breakage/injection from `env:`/`file:` values with shell metacharacters).
- **Security (codex + deep-scan, cross-model):** `WriteTokenFile` + `SaveSettings` `chmod 0600` after
  write (enforce on pre-existing files, not just on create).
- Cleanups: removed dead `derive` `SessionEnd` branch; check `http.NewRequest` err; warn on IPv6-only
  (`::1`) listen; `SessionEnd` frees the pane entry; `bytes.TrimSpace`; single nil-guard in
  `stampState`; dropped rot-prone `§`-doc refs from comments.

### Declined / deferred (with rationale)
- **Kept** `RequireHookAuth` vs `api.RequireBearer` duplication — idiomatic per-package middleware
  (the `hooks` package owns its HTTP middleware, now incl. `RequireLoopback`); opus review endorsed.
- **Deferred pane-state pruning to M7** — prune-on-`/sessions`-stamp would add a TOCTOU race that
  could drop a fresh `blocked`; M7 owns aggregation and polls the live set, so it can prune/epoch
  durably (see the M7 reminders).

## LIVE acceptance — DONE this session (2026-06-29, on this host)
SAFETY (memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]]): scoped to a **throwaway tmux
socket** `agentmon-m6smoke` + a scratch agent on loopback `:18377` + a temp `--settings` — NEVER the
default socket / session 0, the `agentmon` demo panes, `~/.claude/settings.json`, or `deploy/data`.
Drove a real Claude through the **full loop** (real Claude `--settings` → installed hook `curl` → agent
`POST /hook` → `GET /sessions` rollup) and observed **all five states**: `unknown` (baseline shell) →
`idle` (SessionStart) → `working` (prompt) → `done` (Stop) → `working` → `blocked` (permission prompt).
Security fixes verified live: hook-token file `0600`; generated command shell-quotes `$(cat '<path>')`;
install used explicit `--settings`. Post-test: prod agent (pid 842374) up, default session 0 alive,
demo panes (`demo-web %0`/`demo-db %1`) intact, `~/.claude/settings.json` has no hooks, throwaway socket
+ scratch removed, repo tree clean.

## Reminders for the next milestone (M7 — hub aggregation)
- **M7 deliverables:** poll/ingest agent state into `session_state_events` (Phase-0 schema already has
  the table); per-principal `principal_seen` projection (`done→idle` once a principal focuses);
  `GET /api/v1/events` (SSE) + `POST /api/v1/seen`; the public `/servers/{id}/sessions` payload carries
  state + server/session **rollup dots**.
- **`changed` is NOT consumable over the pull (whole-branch Important #1).** The hub reads only the
  rolled-up `Session.State` via `/sessions`; the agent's `Apply` `changed` flag is discarded in
  production. Pull is lossy for `done→done` — the *new* finished-turn transition that `principal_seen`
  must catch. So M7 must EITHER add a per-pane transition surface to the agent (an events buffer or a
  `GET /state?since=<cursor>` that exposes `changed`/transitions) OR accept lossy `done→done`. Decide
  at M7 kickoff. (`changed` is kept in the machine, ready for a cursor/push path.)
- **Pane-state staleness/prune (multi-review C4).** State is keyed `(target, pane)` with no prune; a
  killed-Claude leftover or a tmux **server** restart (pane ids restart at `%0`) can show stale state.
  M7 already polls the live set — prune there (or key on the tmux server-pid epoch from `$TMUX`)
  without racing the intake.
- **Fallback heuristics (§10.4)** were intentionally NOT built in M6 (hooks are the sole source; a
  pane with no hook is `unknown`). Add later if needed.
- The SPA (M8) `SessionList`/`Sidebar` + TanStack-Query layer are already wired to render dots +
  blocked-first sorting the moment the public payload carries state (see `m5-carryover.md`).

## Verification at merge
Full Go suite green (`shared` + `agent` + `hubd`, `-race`); `go vet` clean; `CGO_ENABLED=0 go build
./agent/...` OK; hub/web untouched. Live five-state loop accepted on this host against a throwaway
socket.
