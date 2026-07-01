# AgentMon — Desktop Grid + Sidebar UI Polish — Design

**Date:** 2026-07-01
**Status:** approved-scope, spec under review
**Scope:** four small desktop-web-only UI improvements, shipped together (one branch, one deploy).

## 1. Motivation

Desktop grid usability + a follow-up polish on the kill-session menu:
1. **No active-tile indicator** — with several terminals tiled, nothing shows which one has keyboard
   focus (the terminal you're typing into).
2. **Width-based layout** — `GridView` uses `grid-template-columns: repeat(auto-fit, minmax(360px, 1fr))`,
   so on a wide desktop every open terminal lines up in a single row instead of wrapping into a balanced
   grid.
3. **No layout control** — the operator wants to cap how many terminals sit across before wrapping.
4. **Sidebar ⋯ placement** — the kill/rename `⋯` menu (shipped in kill-session) renders inline right after
   the session name; it should sit at the right edge of the sidebar row.

All four are **web-only** (no Go, no API, no DB). Threat model unchanged.

## 2. §1 — Active-tile highlight

Add a `focus-within` ring to each grid tile wrapper in `GridView`: the tile whose terminal (xterm's hidden
textarea) has focus shows a **primary/blue inset ring** — Tailwind `focus-within:ring-2
focus-within:ring-primary focus-within:ring-inset` on the tile `<div>`. **Pure CSS, no new state**; it lights
up the tile you're typing in and clears when focus leaves the grid. Only meaningful in the multi-tile grid
(when one tile is expanded there is a single visible tile — harmless). Header-button focus also triggers it
(acceptable — it still indicates the tile you're interacting with).

## 3. §2 — Balanced count-based layout

**Pure logic** `web/src/lib/grid-layout.ts`:

```ts
export function gridLayout(n: number, maxCols: number): { cols: number; rows: number } {
  const cap = Math.max(1, Math.floor(maxCols));
  const count = Math.max(1, Math.floor(n));
  const rows = Math.ceil(count / cap);
  const cols = Math.ceil(count / rows);
  return { cols, rows };
}
```

This is the standard "balanced grid" algorithm: fill up to `maxCols` per row, then even the columns out
across the required rows. At the default **maxCols=3** it yields exactly the requested mapping:

| n | cols×rows | | n | cols×rows |
|---|-----------|-|---|-----------|
| 1 | 1×1 | | 4 | **2×2** |
| 2 | 2×1 | | 5 | 3×2 |
| 3 | 3×1 | | 6 | 3×2 |

At maxCols=2 → 2-wide stacks; at maxCols=4 → `n=4` sits 4-across, `n=6` → 3×2; at maxCols=1 → a vertical
stack. `n` never exceeds `GRID_TILE_CAP` (6) but the formula is general.

**`GridView` wiring:** when NOT expanded (`activeId === null`), compute
`{cols, rows} = gridLayout(panes.length, gridMaxColumns)` and set
`gridTemplateColumns: repeat(${cols}, minmax(0, 1fr))` + `gridTemplateRows: repeat(${rows}, minmax(0, 1fr))`
so tiles fill the viewport as an even grid. `minmax(0,1fr)` lets tiles shrink without overflow. When a tile
is **expanded** (`activeId !== null`), keep the current single-cell full-screen behavior
(`gridTemplateColumns: "1fr"`, no explicit rows) — unchanged.

## 4. §3 — Max-columns preference

Add to the persisted `usePrefs` store (`web/src/store/prefs.ts`):
- `gridMaxColumns: number` (default **3**), `setGridMaxColumns(n: number): void`, included in `partialize`.
- The setter clamps to **[1, 4]** (the offered range).

A **"Grid columns" `<select>` (1 / 2 / 3 / 4)** in `SettingsPanel.tsx` (⚙), mirroring the existing "Terminal
theme" select, bound to `gridMaxColumns` / `setGridMaxColumns`. Per-device, like the other prefs.

## 5. §4 — Sidebar ⋯ to the right edge

Move the kill/rename `⋯` menu to the right of the sidebar row (it currently renders inline after the name):
- `SessionActionsMenu` (idle render): make the outer `<span>` a full-width flex row —
  `flex w-full min-w-0 items-center gap-1` — with the name `truncate` (shrinks) on the left and the `⋯`
  button's container gaining `ml-auto` (pinned right). No `onClick` on the outer span (preserves the
  name-click-to-open bubbling from the kill-session regression fix). Rename mode (the `autoEdit`
  `SessionNameEditor`) is unchanged — it fills the block width as before.
- `Sidebar.tsx`: give the row's content `<div className="min-w-0">` a `flex-1` so it fills the row width
  (letting the `⋯`'s `ml-auto` reach the right edge). Result: `● name … ⋯` over `/cwd`.

The mobile inbox (`SessionList`) and grid tile header are NOT affected (they don't use the ⋯ menu).

## 6. Components & interfaces

| Unit | What it does | Test |
|------|--------------|------|
| `lib/grid-layout.ts` `gridLayout(n, maxCols)` | pure `{cols, rows}` balanced-grid math | vitest: maxCols 3/2/1/4 mappings + edges (n=1, maxCols clamp) |
| `store/prefs.ts` `gridMaxColumns` | persisted pref (default 3, clamp 1–4) | folded into the store; light test optional |
| `SettingsPanel.tsx` "Grid columns" select | binds the pref | light render/update test |
| `GridView.tsx` | apply `gridLayout` cols/rows (grid mode) + `focus-within` ring | pure logic tested via `gridLayout`; ring/CSS verified on-device |
| `SessionActionsMenu.tsx` + `Sidebar.tsx` | ⋯ pinned right, name-click preserved | existing SessionActionsMenu tests must stay green |

## 7. Testing & acceptance

- **TDD** the pure `gridLayout` (the only real logic) — the mapping table above + maxCols=2/1/4 + edges.
- Extend/keep the `SessionActionsMenu` tests green (the ⋯-right restructure must not break the name-click
  bubbling or the menu/kill flow). Optional light `SettingsPanel` test for the new select.
- Full web suite (`npx vitest run`) + `npm run build` (tsc + vite) green.
- `/multi-review --codex` on the batch.
- **Visual/on-device acceptance (owner):** the CSS bits (focus ring, the balanced grid filling the
  viewport, the ⋯ at the right edge) can't be machine-verified here (no headless browser) — the owner
  confirms them on-device after deploy.
- **Deploy:** web-only → **hub rebuild only** (`docker compose up -d --build`; the SPA is `//go:embed`'d);
  **no agent change, no DB, no config**. Rides with the other pending web deploys. After the rebuild, hit
  "Check for updates" in ⚙ (PWA cache).

## 8. Non-goals

- Drag-to-rearrange / drag-to-resize tiles.
- Per-tile size or manual tile positions.
- Persisting the highlight after focus leaves (the ring tracks live focus only).
- Any change to mobile, the grid's expand behavior, or the tile WS/scrollback lifecycle.
