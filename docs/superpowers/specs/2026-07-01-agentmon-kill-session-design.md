# AgentMon — Kill Session — Design

**Date:** 2026-07-01
**Status:** approved-scope, spec under review
**Scope:** add a destructive "kill tmux session" action, reachable from the desktop sidebar.

## 1. Motivation

AgentMon can *create* (M10) and *rename* sessions from the UI, but not *remove* them — an
asymmetry. Today the only way to terminate a session (e.g. stop a stuck or runaway Claude) is to
SSH into the host and `tmux -L <socket> kill-session`. Leaving an **idle** session is harmless, but
an **active** one keeps consuming resources after you've "closed the window" (the grid `✕` only
detaches the viewer — it does NOT kill the session). This adds a UI kill so the operator can stop an
agent without SSHing.

The whole feature mirrors the existing create/rename plumbing end-to-end; it introduces no new
architecture. Threat model unchanged: **single LAN operator** — the guardrails below are about
preventing an *accidental* self-inflicted kill, not defending against an adversary.

## 2. Backend — mirror rename exactly

### Agent — `POST /sessions/kill?target=<label>`
- Body: `{"name": "<session>"}`. Behind the existing `RequireBearer`.
- New DI seam `SessionKiller func(ctx, socket, name string) error` (mirrors `SessionCreator`/
  `SessionRenamer`), production-bound to a new `tmux.KillSession(ctx, run, socket, name)` that runs
  **`tmux -L <socket> kill-session -t <name>`** via the **no-shell arg-array `ExecRunner`**
  (`with(socketArgs(socket), "kill-session", "-t", name)`), exactly like `tmux.RenameSession`.
- **Socket scoping:** the socket is resolved from `cfg.ResolveTarget(target)` (the agent's own
  configured target/socket), never from client input — so kill **cannot** touch the default socket /
  session 0. This is the non-negotiable safety property (the hazard that once tore down Claude's own
  tmux; see [[dev-host-runs-hub-and-claude]]).
- Validation: `name` must be non-empty (it's an existing tmux session name from the list). No shell,
  so the arg is passed literally — no injection surface. Wrap the tmux "can't find session" stderr as
  `tmux.ErrNoSession` (reuse rename's detection) → **404**; empty name → **400**; success → **200
  `{"name": "<session>"}`** (or 204 — pick 200 for symmetry with create/rename).
- Bounded by the Phase-5 `agentTmuxTimeout` (10s) like the other tmux-shelling handlers.

### Hub — `POST /api/v1/servers/{id}/sessions/kill`
- Behind `RequireAuth` → **CSRF enforced** on this POST (like create/rename).
- New `authz.SessionKill` action; `authorizeOr403(SessionKill, "server:"+id)` first (deny path
  audited), then decode `{name}` (capped body, reuse `maxCreateSessionBody`), validate non-empty.
- New `registry.Client.KillSession(ctx, srv, target, name)` → forwards to the agent; maps agent 404 →
  `registry.ErrNoSession` → **404**, 400 → **400**, other → **502**.
- **Audit `session.kill`** (principal, `shared.SessionID(id, auditTarget, name)`, name, client IP, UA)
  on success — recorded as soon as the agent confirms the kill (mirrors create/rename audit timing).
- Route registered in `router.go` next to `.../sessions/rename`.

## 3. Web — desktop sidebar only

- **`SessionActionsMenu` (⋯)** — a small overflow menu on the **desktop `Sidebar` rows only**,
  hosting **Rename…** (opens the existing `SessionNameEditor` edit mode) and **Kill session…**
  (opens the modal). On the sidebar this replaces the current click-the-name-to-rename trigger with
  a menu; the **mobile inbox (`SessionList`) and the grid tile header are UNCHANGED** — they keep
  their existing inline rename (and the tile keeps its `✕` close), so no `✕`/kill adjacency and no
  mobile kill for now.
- **`KillSessionModal`** — names the session + host, a single **Kill session** button + **Cancel**.
  When the session's live state (from the SSE state store, `store/session-state`) is `working` or
  `blocked`, it shows an extra warning line: *"This agent is mid-task — killing it stops the agent."*
  A nudge, never a block. The modal is the only confirmation (kill is irreversible — no undo toast,
  because the process tree is already gone).
- **`api-client.killSession(serverId, name, target)`** — `POST` via the existing `request` helper
  (sends CSRF), mirroring `renameSession`.
- **On success:** optimistically remove the session from the list and **close any open pane/tile for
  it** (`usePanes` — drop panes whose `(serverId,target,session)` matches). The relay would also tear
  down on its own once the agent's control client sees the session vanish (`%exit` → writePump close),
  but we close proactively for snappy feedback. On a 404 (already gone) treat as success (it's gone);
  on other errors show an error toast and leave the row.

## 4. Scope, safety, non-goals

- **Kills the whole tmux session** (all its windows/panes) — matching "close tmux session." Not a
  per-window/per-pane kill.
- **Safety recap:** socket-scoped (can't hit the default socket), no-shell arg-array exec,
  name-validated, CSRF-protected, authz-gated, audited, modal-confirmed (irreversible).
- **Desktop only** (owner decision) — mobile inbox gets no kill in this iteration.
- **Non-goals:** bulk/multi-select kill; kill-and-recreate; a trash/archive with undo (impossible —
  kill is irreversible); auto-reaping idle sessions.

## 5. Components & interfaces

| Unit | What it does | Mirrors |
|------|--------------|---------|
| `tmux.KillSession` | `kill-session -t <name>` on the socket via arg-array runner | `tmux.RenameSession` |
| agent `KillSessionHandler` + `SessionKiller` seam | validate + socket-scope + exec + map errors | `RenameSessionHandler` |
| `registry.Client.KillSession` | hub→agent POST + error mapping | `Client.RenameSession` |
| hub `ServerKillSessionHandler` | authz + CSRF + audit + forward | `ServerRenameSessionHandler` |
| `authz.SessionKill` + `Audit.SessionKill` | policy action + audit row | `SessionRename` |
| `api-client.killSession` | web POST w/ CSRF | `renameSession` |
| `SessionActionsMenu` (⋯) | Rename + Kill menu, sidebar rows | (new; wraps `SessionNameEditor`) |
| `KillSessionModal` | confirm + state-aware warning | (new) |
| `usePanes` cleanup | drop panes for the killed session | existing `closePane`/`renamePane` |

## 6. Testing & acceptance

- **TDD each unit.** Agent: `KillSessionHandler` kills via a fake `SessionKiller` asserting the exact
  `(socket, name)` and that the socket is the *configured* one (never client-controlled); 404 on
  `ErrNoSession`; 400 on empty name. Hub: `ServerKillSessionHandler` authz-deny (audited) + CSRF-required
  + `session.kill` audited + 404/400 mapping (httptest, fake agent). Web (vitest): `killSession` sends
  the right POST; the ⋯ menu renders Rename + Kill; the modal shows the working/blocked warning from a
  seeded state store; success removes the row + closes the pane.
- `/multi-review --codex` on the batch; apply findings, defer the rest with rationale.
- **SAFE acceptance:** all tests use fakes. Any live check builds to scratch and drives a **throwaway
  `tmux -L <scratch>` socket** only — NEVER the default socket (session 0), NEVER the live `agentmon`
  socket. Prod untouched.
- **Deploy:** touches BOTH halves (agent kill route + hub route/UI) → an **agent rebuild+restart AND a
  hub `docker compose up -d --build`** on the dedicated box; no DB migration, no config change. Confirm
  with the owner before deploying. (Naturally rides with the pending Phase-5 + Shift+Enter deploy.)

## 7. Rollout order

1. `tmux.KillSession` + agent `SessionKiller`/`KillSessionHandler` + route (TDD).
2. `authz.SessionKill` + `Audit.SessionKill` + `registry.Client.KillSession` + hub
   `ServerKillSessionHandler` + route (TDD).
3. web `api-client.killSession` + `usePanes` cleanup (TDD).
4. web `SessionActionsMenu` (⋯) + `KillSessionModal` wired into `Sidebar` (TDD where machine-testable).
5. `/multi-review --codex` → fold findings.
6. SAFE acceptance → owner deploy confirm → carryover + memory update.
