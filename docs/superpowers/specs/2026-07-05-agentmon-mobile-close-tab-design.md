# AgentMon — Mobile Close-Tab (Explicit Open Set) — Design

**Date:** 2026-07-05
**Status:** approved-scope, spec under review
**Scope:** mobile-web-only. Give the mobile session tab bar an explicit, persisted **open set** with a per-tab close (`×`) that hides the tab without terminating the tmux session. Desktop untouched. No Go/agent/API/DB changes.

## 1. Motivation

On mobile the session tab bar (`MobileSessionTabs`, rendered by the `terminal.tsx` route) is built from the **entire** live session list — every session on every server becomes a tab (`buildTabs(rows, …)`). The owner typically works in only ~2 sessions, but there is no way to remove the rest from the bar, and the active tab's only affordance is inline rename.

**Ask:** replace the tab's rename affordance with a close (`×`) that **removes the tab from the bar without killing the tmux session** (close ≠ kill; the session keeps running and stays re-openable from the home list).

**Model decision (settled in brainstorming):** adopt an **explicit open set** rather than a subtractive hidden set.

- The owner curates ~2 sessions; an open set lets them curate once and keeps new sessions from ever intruding on the bar.
- **Model parity with desktop**, which already uses an open set (`usePanes` — `openPane`/`closePane`, where "close" already means *remove the tile, don't kill*). This design extends that proven concept to mobile.
- **Cleaner data flow:** today the tab bar comes from *all* sessions while the pane pool warms a *subset*. The open set collapses those into one source — it drives both the tabs and which panes warm.

Accepted cost: the bar no longer shows every session at a glance, and new sessions do **not** auto-appear. You add a session to the bar by opening it from the home list (`SessionList`), which still lists every session with state dots. That gesture doubles as "reopen."

## 2. Goals / Non-goals

**Goals**
- A per-tab `×` on every mobile tab that removes it from the bar and frees its relay socket, leaving the tmux session running.
- An explicit, **per-device-persisted** open set that drives the mobile tab bar and pane warming.
- Opening a session from the home list adds it to the open set and focuses it.
- Rename remains available on mobile via the home list; the active tab drops the inline rename editor.

**Non-goals**
- No change to desktop (`usePanes`, `GridView`, sidebar).
- No `kill-session` / tmux termination path is added or reused. This is purely client-side UI state.
- No new tab-bar "+" picker — adding a tab is done from the home list (same gesture as reopen). (Possible later enhancement; YAGNI now.)
- No Go, hub, agent, API, or DB changes.

## 3. Architecture overview

| Unit | Responsibility |
| --- | --- |
| `web/src/store/mobile-open-tabs.ts` (**new**) | Persisted (localStorage, per-device) ordered open set of tab identities. Pure state + actions. |
| `web/src/hooks/useMobilePanePool.ts` (**changed**) | Add a `close(id)` action to the reducer/hook (evict a pane, clean `lru`, clear/redirect `focusedId`). |
| `web/src/components/MobileSessionTabs.tsx` (**changed**) | `buildTabs` derives from the open set (∩ live rows). Every tab renders a `×`. Active tab drops the inline editor. New `onClose(tab)` callback. |
| `web/src/routes/terminal.tsx` (**changed**) | On entry, add the entered pane to the open set + focus. Warm the open set (not all rows). Wire `onClose` → neighbor-focus + open-set removal + pool eviction; last-tab close navigates Back to home. |

All four units are independently unit-testable (the store and both reducers are pure; `buildTabs` is pure; the component renders from props).

## 4. Data model & identity

- **Identity key:** the existing `paneIdentity(serverId, target, paneId)` → `"${serverId}:${target}:${paneId}"`. It is stable across **rename** (uses immutable `target`, not the mutable session name) and across **reload** (the tmux `paneId` persists for the life of the pane). No new identity scheme.
- **Open-set entry:** stored with enough to render/warm without a live row — `{ serverId, target, paneId }` (a `PoolPane`-shaped record; the session *name* is always resolved from the live rows, never persisted, so a rename is reflected on refetch). Ordered array; order is insertion order and is the tab render order.

### `mobile-open-tabs.ts` store (zustand + `persist`)

Mirrors the `usePrefs` persistence pattern (localStorage, versioned storage key).

```ts
export interface OpenTab { serverId: string; target: string; paneId: string; }

interface MobileOpenTabsState {
  open: OpenTab[];                 // ordered = tab render order
  add(t: OpenTab): void;           // append if absent (idempotent); no-op if present
  remove(id: string): void;        // remove by paneIdentity
  has(id: string): boolean;
}
```

- `add` is idempotent — re-entering an already-open session does not duplicate or reorder it.
- Keyed operations use `paneIdentity(...)` internally so callers pass either the parts or the id consistently with the rest of the app.
- **No auto-prune** of entries whose session is not currently live: a briefly-offline server should not silently drop the tab. Rendering intersects with live rows (§5), so a dead entry simply shows no tab until it returns; if the session is truly gone it lingers harmlessly in localStorage.

## 5. `buildTabs` from the open set (`MobileSessionTabs.tsx`)

`buildTabs` changes source from "all rows" to "the open set, resolved against live rows":

- Input: `openTabs: OpenTab[]`, `rows: SessionRow[]`, `current: CurrentSession`, `stateOf`.
- For each open-set entry **in order**, find the matching live row by `paneIdentity`. If found, emit a `SessionTab` (name from the row, or the URL name for the active/current tab per today's mid-rename rule; state via `stateOf`). If **not** found in live rows, **skip** it (no dead tab), but keep it in the store.
- The **current** (entered/focused) session is guaranteed present because entering adds it to the open set (§6). The existing synthetic-current prepend is retained as a first-paint fallback for the window before the session list loads.
- Ordering stays stable (open-set insertion order), matching today's "don't reshuffle tabs under the user's thumb" rule; the state dot flags attention instead.

`MobileSessionTabs` render changes:
- **Every tab** (active and inactive) renders a `×` button with a thumb-friendly hit area, `aria-label={`Close ${name}`}`, that calls `onClose(tab)`. It must not trigger the tab's switch handler (stop propagation).
- The **active tab** no longer hosts `SessionNameEditor` — it renders `state dot + plain name + ×`. Inactive tabs render `state dot + name + ×` (tap the name/body to switch, tap `×` to close).
- The `×` is still shown when only one tab is open; closing the last tab is handled by the route (navigate Back — §6), so no "disable on last tab" rule is needed.

New prop signature:

```ts
MobileSessionTabs({
  tabs, onSwitch, onClose,
}: {
  tabs: SessionTab[];
  onSwitch(tab: SessionTab): void;
  onClose(tab: SessionTab): void;
})
```

`onRenamed` is removed (rename no longer lives here).

## 6. Route behavior (`terminal.tsx`)

**Entry (mount):** the entered pane is added to the open set and focused.
- Replace the mount-time `pool.openAndFocus({ serverId, target, paneId })` flow with: `openTabs.add({ serverId, target, paneId })` **and** `pool.openAndFocus(...)`. This makes "tap a session on home → enter" the single gesture that opens/reopens a tab. (`add` is idempotent, so re-entering an already-open session is a no-op on the set and just refocuses.)

**Warming:** the eager-warm loop warms **open-set members that resolve to a live session row** (up to `MOBILE_POOL_CAP`), not all rows and not stale entries. The focused/entered pane always counts first and is seeded independently (mount effect), so first paint never waits. Non-focused warming intersects the open set with the live rows (gated on `rows.length`), mirroring `buildTabs` — a stale/dead persisted entry (session killed elsewhere, server offline, tmux restart with reused pane ids) is therefore **never** warmed, so it can't open a socket or steal a pool slot from a real tab. Rows are React-Query-cached (staleTime 15s), so on a normal home→terminal navigation they're already present and warming is immediate; only a cold hard-reload directly onto a terminal URL briefly defers non-focused warming until the list loads.

**Open set vs. pool cap:** the open set is the tab-bar source and may exceed `MOBILE_POOL_CAP` (e.g. 6 tabs open, cap 4). That is fine and intentional: tabs are lightweight; the pool bounds only how many terminals hold a live socket at once, evicting via LRU. Switching to a non-pooled tab warms it on demand and evicts the least-recently-focused — the existing pool behavior. For the ~2-session target user, open set < cap, so the two are identical in practice.

**Switch:** `onSwitch(tab)` → `pool.openAndFocus(...)` (unchanged; only ever targets a visible/open tab).

**Close (`onClose(tab)`):**
1. Compute `id = paneIdentity(tab...)`.
2. If `tab` is **not** the active tab: `openTabs.remove(id)` + `pool.close(id)`. Focus is unchanged.
3. If `tab` **is** the active tab:
   - Determine the neighbor in the **current visible tab order**: the next tab to the right; if none, the previous tab to the left.
   - **If a neighbor exists:** `pool.openAndFocus(neighbor)` (or `pool.focus(neighborId)` if already pooled), then `openTabs.remove(id)` + `pool.close(id)`.
   - **If no neighbor** (closing the last open tab): `openTabs.remove(id)` + `pool.close(id)`, then `navigate({ to: "/" })` — back to the home list (natural terminal state).

**Invariant:** the focused pane is always a member of the open set and a live tab — entry adds+focuses; switch/neighbor only target visible tabs; closing the focused one either refocuses a neighbor or leaves the route. So `buildTabs` never needs to special-case a "focused-but-hidden" pane.

## 7. `useMobilePanePool` — new `close(id)` action

Add to `poolReducer`:

```ts
| { type: "close"; id: string }
```

- Remove the pane with `idOf(p) === id` from `panes` and from `lru`.
- If `state.focusedId === id`, set `focusedId = null` (the route drives the next focus explicitly via the close handler's neighbor logic; a `null` focus for one render is safe — the stack simply shows nothing for that pane, which is immediately superseded by the neighbor focus dispatched in the same handler).
- No-op if the pane is absent.
- Expose `close: (id: string) => dispatch({ type: "close", id })` from the hook.

Unmounting the closed pane's `TerminalView` (it disappears from `panes`) closes its WebSocket/relay — freeing the socket. The tmux session is server-side and untouched.

## 8. Edge cases

- **Reopen a closed session:** go to the home list, tap it → route entry `add`s it back (idempotent) and focuses it. Same as first open.
- **Rename while open:** name resolves from live rows each render, so a rename reflects on refetch; identity is `target`-based, so the tab/pane survives the rename without a remount (unchanged from today).
- **Session killed elsewhere** (e.g. desktop sidebar kill): it drops out of live rows, so `buildTabs` stops emitting its tab; the open-set entry lingers harmlessly. If it was the focused/only tab, the vanished-row limitation is the same accepted one noted for keep-alive; not worsened by this change.
- **Server briefly offline:** its sessions vanish from rows → their tabs disappear, but open-set entries are retained → tabs reappear when the server returns.
- **tmux server restart (KNOWN LIMITATION, accepted):** the open set persists `paneId` (e.g. `%0`), which is stable across a *page reload* but NOT across a *tmux server restart* (host reboot / tmux killed), after which pane ids are reassigned to brand-new, unrelated sessions. A lingering entry `{s1, default, %0}` could then intersect a live row again and *resurface* as a tab the user never opened on this device (labeled with whatever now holds `%0`). Impact is low — close ≠ kill, so this is only a surprise tab, never a wrong-kill; the user closes it again. Accepted as-is for v1 (desktop `usePanes` avoids this only by not persisting). Future hardening if it annoys: prune an open-set entry when its server is reachable but its pane has been absent for N consecutive session-list refreshes.
- **First run / migration:** no migration. On first entry after deploy the open set is empty except the just-entered session, so the bar shows one tab; the owner opens their other working session(s) from home once, and the set persists thereafter. (Deliberately not seeded from "all sessions" — that would reintroduce the clutter this feature removes.)
- **Duplicate open:** `add` is idempotent (no dup tab, no reorder).

## 9. Testing

- **`mobile-open-tabs` store:** add appends/idempotent; remove by id; `has`; ordered; persistence round-trip (rehydrate from storage).
- **`poolReducer` `close`:** evict non-focused (panes/lru cleaned, focus intact); evict focused (focusedId → null); absent id → no-op; lru has no dangling id.
- **`buildTabs`:** derives from open set in order; open-set entry absent from rows is skipped (no dead tab); current/entered pane always present; synthetic-current fallback before rows load.
- **`MobileSessionTabs` render:** `×` present on every tab incl. active; `×` click calls `onClose` and not `onSwitch`; active tab renders plain name (no `SessionNameEditor`); inactive tab body switches.
- **Close-behavior (route-level or a small pure helper for neighbor selection):** non-active close removes + evicts, focus unchanged; active close focuses next-right neighbor; active close with no right neighbor focuses left; last-tab close triggers navigate-home. Extract neighbor selection into a pure function to unit-test without the router.

## 10. Out of scope / non-changes

- Desktop (`usePanes`, `GridView`, `DesktopShell`, sidebar `⋯`).
- Any tmux `kill-session` path (explicitly not reused; close is client-only).
- A tab-bar "+" add-picker (home list covers add/reopen).
- Go / hub / agent / API / DB. Threat model unchanged. Deploys via a hub rebuild (web-only).
