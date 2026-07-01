# Kill Session → carry-over

The UI "kill tmux session" feature is complete, reviewed (5 per-task + opus whole-branch +
`/multi-review --codex`), live-accepted (SAFE), and ready to merge. It mirrors the create/rename
plumbing end-to-end; **desktop sidebar only**.

Spec: `docs/superpowers/specs/2026-07-01-agentmon-kill-session-design.md`.
Plan: `docs/superpowers/plans/2026-07-01-agentmon-kill-session.md`.

## What shipped

- **Agent** — `POST /sessions/kill?target=<label>` (`tmux.KillSession` → `kill-session -t "="+name`
  via the no-shell arg-array Runner). The socket is resolved from the agent's OWN `cfg.ResolveTarget`,
  never client input, so kill **cannot** touch the default socket / session 0. Bounded by the Phase-5
  `withTmuxTimeout` (10s). Unknown session → 404. The `"="` **exact-match prefix** is load-bearing (see
  the cross-model finding below).
- **Hub** — `POST /api/v1/servers/{id}/sessions/kill` behind `RequireAuth` (CSRF), `authz.SessionKill`
  (authorize-before-decode, deny audited), `registry.Client.KillSession` (agent 404→ErrNoSession,
  400→ErrInvalidSession), `session.kill` audit on **success only**, 200 `{"name":...}`.
- **Web (desktop sidebar only)** — `api-client.killSession`; `KillSessionModal` (names session+host,
  single Kill + Cancel, Escape/backdrop/Cancel close, "mid-task" warning when state is working/blocked,
  focus lands on Cancel, Kill disabled while in-flight); `SessionActionsMenu` (⋯: **Rename…** reuses the
  editor via a new additive `autoEdit`/`onDone` on `SessionNameEditor`, **Kill session…** opens the modal).
  On kill success: `closePane` + invalidate the sessions query; a **404 is treated as success**; a non-404
  failure shows a `toast.error` and leaves the row. The **mobile inbox (`SessionList`) and grid tile header
  are unchanged** (they keep their inline `SessionNameEditor`).

## Reviews

- **5 per-task reviews** (all Spec ✅ / Approved). Task 5 had one Important fix round (the menu test now
  asserts the post-kill `closePane` + query-invalidate via a stable `vi.hoisted` spy).
- **Whole-branch (opus): Ready-with-fixes**, no Critical. Verified socket-scoping / no-shell / CSRF /
  audit-on-success / untouched mobile+grid / no-XSS end-to-end. 2 Important FIXED (`a28d8cb`):
  (1) a **real regression** — `SessionActionsMenu`'s outer `<span onClick={stop}>` swallowed name-clicks
  so the sidebar name no longer opened the session; fixed by removing the outer stop AND adding
  `stopPropagation` to the modal backdrop (both halves of the trap) + a bubbling regression test;
  (2) spec §3 — a non-404 kill failure closed the modal silently; now a `toast.error`. Also folded modal
  a11y (dialog role/aria-labelledby on the panel, focus Cancel) + Kill-button busy-disable.
- **`/multi-review --codex` (4 lenses): CROSS-MODEL CATCH.** codex + deep-scan **both** independently
  found a MEDIUM the opus review missed: `kill-session -t <name>` used tmux's fuzzy target resolution
  (exact → start-of-name prefix → fnmatch glob), so on this destructive op a stale/renamed name could
  **prefix-match a different session**, and a name with `*`/`?`/`[`/`:` could be read as a pattern and kill
  the wrong session (the opus review reasoned only about *shell* injection and missed tmux's own target
  layer). FIXED (`6e40c52`): pass `"="+name` to force tmux **exact match** + regression tests. specialist
  lens clean.

## SAFE live acceptance (2026-07-01, throwaway socket only — NEVER default/agentmon)

Validated the `"="` fix against **real tmux** on a throwaway `tmux -L ksaccept` socket:
- `kill -t '=*'` → *can't find session* (glob treated literally; nothing killed) ✅
- `kill -t '=proj'` (no exact `proj`) → *can't find session*; `project2` survives (no prefix-match) ✅
- contrast `kill -t 'proj'` (no `=`) → **killed `project2`** — exactly the prefix-match bug the fix prevents ✅
- `kill -t '=keeper'` → killed exactly `keeper` ✅
Throwaway socket killed after; the live `agentmon` socket (real dnsmon/jarvis/streammon sessions) and the
`default` socket (session 0) confirmed **untouched**. Full workspace: `go test ./... -race` GREEN (shared+
agent+hubd), vet clean, `CGO_ENABLED=0` builds both binaries, web **242** tests + build. (The HTTP plumbing
is fake-driven unit-tested; the destructive tmux behavior — the real risk — is what the live check exercised.)

## Deferred (reviewer-endorsed fast-follow — NOT done, none are correctness bugs)

- Go test-hygiene: `KillSession`'s stdout not-found path untested (only the err-text path); a hung-tmux
  timeout test for the kill handler (rename has one) + a generic-500 test; the hub deny test named
  "…Audited" only asserts 403 (deny audit is `authorizeOr403`'s contract).
- Codebase cleanups (out of this feature's scope — they refactor EXISTING shipped code): a
  `registry.Client.postToAgent` helper (dedupe Create/Rename/Kill POST scaffolding) and an `audit.marshalMeta`
  helper (dedupe Create/Rename/Kill meta marshalling); consolidate `doKill` teardown into a `finally`.
- UX: a non-404 kill failure toasts but the row stays (safe fallback); a success toast is intentionally
  omitted (the row disappearing is feedback).

## Deploy notes (owner runs on the dedicated box — NOT done automatically)

Touches BOTH halves → an **agent rebuild + restart AND a hub `docker compose up -d --build`** on the
dedicated box; **no DB migration, no config change**. Confirm with the owner before deploying. Rides
naturally with the still-pending Phase-5 (agent-timeouts half) + Shift+Enter + keybar deploys. Leftover
`agentmon-review-*` throwaway sockets from the review agents' live experiments are harmless (empty servers
on isolated `-L` sockets); clean up with `tmux -L <name> kill-server` if desired.
