# Session rename (post-v1 focused feature)

## Goal
Rename a tmux session from the UI — inline-edit the name in the open terminal's header AND a rename action in
the session list/sidebar. Mirrors the M10 new-session flow, smaller.

## Contracts (additive)
- **Agent** (bearer REST, sibling of `POST /sessions`): `POST /sessions/rename?target=<target>` body
  `{ "from": "<current>", "to": "<new>" }` → `200 {"name":"<new>"}` / `400` (invalid `to` / empty `from`) /
  `404` (no such session `from`) / `409` (a session named `to` already exists).
- **Hub** (cookie + CSRF): `POST /api/v1/servers/{id}/sessions/rename` body `{from, to}` → `201 <Session>`
  (the renamed session, re-listed) / `400` / `404` / `409` / `502`.
- **shared**: `RenameSessionRequest{ From, To string }`.

## Rules / safety
- `to` must pass the existing `shared.ValidateSessionName` (`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`), enforced at
  **both** the hub boundary and the agent. `from` need only be non-empty (it's an existing tmux name; passed
  as a positional `-t` arg).
- tmux is invoked ONLY via the arg-array `Runner` (`tmux rename-session -t <from> <to>`) — no shell, no
  interpolation. Duplicate target → `ErrSessionExists` → 409; unknown `from` → `ErrNoSession` → 404.
- New action `authz.SessionRename` ("session.rename", trivially allowed in v1). Hub authorizes **first**,
  enforces CSRF, and **audits** `session.rename` (resource = the new session id; `from`+`to` in meta).

## Web
- `api-client.renameSession(serverId, from, to, target?)` (auto-CSRF) → returns the renamed `Session`.
- **Open-pane re-key:** a session's pane id is `serverId:target:session:paneId`; renaming changes only
  `session`, so the terminal WS (keyed by `paneId`) **survives** — no reconnect. Add `usePanes.renamePane(
  oldId, newSession)` that rewrites the pane's `session` + `id` (and `focusedId` if it was focused); the
  focus/seen machinery (`useFocusedSeen`) follows the pane's new key automatically.
- **Surfaces:** an editable name (pencil) in the terminal header (desktop tile header + mobile terminal
  header) and a rename action on each session row (`SessionList` mobile + `Sidebar` desktop). On success:
  `renamePane` (if open) + invalidate `["sessions", serverId]`. A live client-side name check mirrors the
  shared regex; 409 → inline "name already exists".

## Caveat (documented, not blocking)
Live state survives a rename (state is keyed to the pane, not the name), but per-session **"seen" + history
are keyed on the session name** — so right after a rename the session may briefly re-surface as unseen/`done`
until refocused, and prior `session_state_events` stay filed under the old name. Acceptable for v1.

## Testing / acceptance
TDD throughout: `ValidateSessionName` already covers `to`; `tmux.RenameSession` (fake Runner asserts the arg
array + duplicate/no-session mapping) + a real-tmux integration test on a **throwaway `-L` socket**; agent +
hub handler tests (authz/CSRF, error mapping, re-list); web tests (`renameSession` body+CSRF, `renamePane`
re-key, the header/list affordances). Then opus whole-branch + `/multi-review --codex` (fix all but nitpicks)
→ SAFE accept (suites + rename integration test + a hub route probe) → merge → redeploy hub + agent.

## Out of scope
session kill/delete; renaming across servers/targets; multi-target.
