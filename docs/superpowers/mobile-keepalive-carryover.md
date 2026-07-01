# Mobile Keep-Alive Terminals → carry-over

Instant, flash-free mobile session-tab switching via a route-local mounted pane pool.
**Web-only.** Complete + reviewed (per-task + whole-branch opus + `/multi-review --codex`) + merged.
Spec: `docs/superpowers/specs/2026-07-01-agentmon-mobile-keepalive-terminals-design.md`.
Plan: `docs/superpowers/plans/2026-07-01-mobile-keepalive-terminals.md`.

## What shipped

1. **`lib/pane-identity.ts` `paneIdentity(serverId, target, paneId)`** — shared `${serverId}:${target}:${paneId}`
   helper, name-independent (a rename never re-identifies a pane). Extracted from `MobileSessionTabs`' private
   `tabIdentity` and reused there + in the pool.
2. **`hooks/useMobilePanePool.ts`** — pure reducer + hook holding the route-local pool: `panes` (insertion
   order), `focusedId`, `lru`. `open`/`openAndFocus`/`focus`, dedupe by identity, LRU-evict past
   `MOBILE_POOL_CAP = 4`, focused pane **never** evicted. Pure `poolReducer` is unit-tested (6 cases).
3. **`components/TerminalView.tsx`** — new optional `active?: boolean` prop; when it flips true a
   `useEffect` focuses the xterm. Defaults to `undefined` → **desktop `GridView`/`usePanes` untouched**.
4. **`components/MobileTerminalStack.tsx`** — renders the pool single-visible: every pooled pane stays
   mounted (its own socket + scrollback), only the focused one is `display:flex` (rest `display:none`),
   keyed by `paneIdentity`. Only the focused pane gets `showKeyBar` + `active`. Mirrors GridView's
   keep-mounted trick.
5. **`routes/terminal.tsx` (`MobileTerminalRoute`)** — seeds the pool from the URL (pre-paint
   `useLayoutEffect`), eager-warms up to the cap once the session list arrives (focused always included),
   switches tabs **in-state** (`pool.openAndFocus`, no `navigate`), drives `useFocusedSeen` off the focused
   pane (+ live-list name so a rename reflects). The old per-switch `navigate` and the `TerminalView`
   `key`-remount hack are gone — that remount was the "connecting…" flash + cross-session bleed.

**Level-1 scope:** the pool is route-local, so **‹ Back** tears down every pooled socket. Re-opening a
session is a fresh connect (Level-2 cross-boundary warmth was deferred as YAGNI).

## Reviews

- **5 per-task reviews** — all Spec ✅ / Approved, **zero fix rounds**. The LRU reducer (Task 2) and the
  route integration (Task 5) were reviewed on opus.
- **Whole-branch (opus): Ready-to-merge = with verification, not code.** Zero Critical, no code changes
  required. Confirmed grid/`usePanes` genuinely untouched, and all invariants hold (identity, keep-alive,
  LRU with focused-never-evicted [adversarially probed], Level-1, rename reflection).
- **`/multi-review --codex`** (correctness[feature-dev sub] + simplifier + deep-scan + codex gpt-5.5):
  - **1 fix applied** (`3f7fdf2`): pool was seeded in a post-paint `useEffect` → a one-frame blank terminal
    on entry; switched the seed to `useLayoutEffect` (pre-paint). deep-scan.
  - **1 HIGH refuted** (codex): "inactive warmed panes steal focus" — false positive. Hidden panes are
    `display:none`, so their `useTerminalSession` onOpen `focus()` is a DOM no-op; deep-scan independently
    agreed; desktop GridView already mounts multiple keep-alive TerminalViews with the same onOpen focus and
    ships fine. codex's fix would have regressed desktop (plan forbids touching GridView).
  - Nitpicks left as-is (unused-but-spec'd `pool.focus()`; GridView could reuse `paneIdentity` — out of
    scope; hoist `focusedIdent`; export `idOf`; hidden panes keep parsing = intended keep-alive cost).

Full web suite **271** green, `tsc --noEmit && vite build` clean (verified on the merge result).

## Known limitation (accepted, owner decision)

**Focused-pane name fallback (codex C2).** After an in-state switch, if the focused pane's row disappears
from the live list (session killed elsewhere, or a transient list-load failure), `focusedName` falls back to
the entry URL `session`, so the tab label / `/seen` POST can briefly carry the wrong session **name**
(`serverId`+`target` stay correct). The plan-compatible fix would be to fall back to the tab's last-known
name; the fuller fix (store the name on `PoolPane`) was **rejected** — it reverses the plan's deliberate
identity-only `PoolPane` (plan line 667). Left as a known edge; revisit only if it bites in practice.

## Owner acceptance — on-device (the only unverifiable surface here)

Keyboard focus handoff on iOS is the fiddly bit and needs a real device (no headless mobile browser here).
Confirm after deploy:
- Switching tabs is **instant, no "connecting…"**, and shows the correct session (no bleed).
- The **soft keyboard stays up** across a switch and typing lands in the newly-focused terminal.
  - **If the keyboard drops:** the `TerminalView` `active` focus is a passive post-paint effect (outside the
    tap's gesture window), which iOS Safari may not honor. Contingency fix: move the focus into the tab's
    synchronous `onPointerDown`/`onMouseDown` handler (gesture-scoped), mirroring `MobileKeyBar`'s
    `focusTerminal()`. The plan (Task 6 Step 2) said not to pre-apply this unless observed.
- **Rename** the focused session → the tab label updates after the brief refetch, pool undisturbed.
- **‹ Back** returns to the list; re-opening reconnects (expected Level-1).
- With **>4 sessions**, switching still works (older panes reconnect on revisit via LRU).
- If refit is stale after a reveal (wrong cols/rows), add a resize nudge on `active` flip — only if observed.

## Deploy notes

**Web-only → hub rebuild only** (`docker compose up -d --build` on the dedicated box; the SPA is
`//go:embed`'d). **No agent change, no DB, no config, no relay change** (≤4 live sockets per mobile viewer is
well within the hub's per-principal cap of 32). After the rebuild, hit "Check for updates" in ⚙ (PWA cache).
Rides with the other pending web deploys. Confirm with the owner before deploying.
