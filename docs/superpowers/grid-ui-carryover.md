# Desktop Grid + Sidebar UI Polish → carry-over

Four small **web-only** desktop UI improvements, complete + reviewed + ready to merge.
Spec: `docs/superpowers/specs/2026-07-01-agentmon-desktop-grid-ui-design.md`.
Plan: `docs/superpowers/plans/2026-07-01-agentmon-desktop-grid-ui.md`.

## What shipped

1. **`lib/grid-layout.ts` `gridLayout(n, maxCols)`** — balanced grid geometry: `rows = ceil(n/maxCols)`,
   `cols = ceil(n/rows)`, both clamped `>= 1`. At maxCols=3: 1→1×1, 2→2×1, 3→3×1, **4→2×2**, 5→3×2, 6→3×2.
2. **`store/prefs.ts` `gridMaxColumns`** — persisted per-device pref, default **3**, clamped `[1,4]` (in
   `partialize`, so it survives reload) + a **"Grid columns" 1–4 select** in the ⚙ panel.
3. **`GridView.tsx`** — grid mode now sets `gridTemplateColumns/Rows` from `gridLayout(panes.length,
   gridMaxColumns)` (tiles fill the viewport as an even grid instead of a single width-based row) + a
   **`focus-within:ring-2 ring-primary ring-inset`** ring on the active tile (the terminal you're typing
   into). Expand-to-full-screen, mobile, and the tile WS/scrollback lifecycle are **unchanged**.
4. **Sidebar ⋯ to the right** — `SessionActionsMenu` outer span `flex w-full` + ⋯ `ml-auto` + menu opens
   `right-0`; `Sidebar` row content `flex-1`. The **name-click-to-open** invariant is preserved (outer span
   stays `onClick`-free — the previously-fixed regression is intact).

## Reviews

- **4 per-task reviews** — all Spec ✅ / Approved, no Critical/Important, no fix rounds (Tasks 3 & 4 zero
  findings).
- **Whole-branch (opus): Ready-to-merge YES.** Verified the full maxCols=3 mapping, GridView hook order
  (the `usePrefs` selector sits before the early return), complete persistence (`partialize`), the
  name-click invariant preserved, expand/mobile untouched, and that `ring-primary` resolves to a real blue
  (`--primary: 217 84% 55%` in `index.css` + `tailwind.config.js`).
- **Codex cross-model pass: clean** (ran grid-layout + prefs + the existing GridView/SettingsPanel tests +
  typecheck itself).
- **1 Minor fixed** (`98e51ad`): the new `prefs gridMaxColumns` test block got a `beforeEach(resetPrefs)`
  so its "defaults to 3" assertion is order-independent (flagged by both the task review and the
  whole-branch review). Test-only.

Full web suite **252** green, `tsc --noEmit && vite build` clean.

## Acceptance (owner, on-device — the only unverifiable surface)

All the visual bits are pure CSS and can't be machine-verified here (no headless browser). Owner to
confirm on-device after deploy:
- the blue focus ring appears on the tile you're typing in (and clears when focus leaves);
- the grid fills the viewport as a balanced grid at ⚙ Grid columns = 1 / 2 / 3 / 4 (e.g. 4 tiles = 2×2 at
  cap 3);
- the sidebar ⋯ menu sits at the right edge of the row (name-click still opens the session).
The math (`gridLayout`) + the pref (default/clamp/persist) are unit-tested; the wiring compiles + the full
suite passes.

## Deploy notes

**Web-only → hub rebuild only** (`docker compose up -d --build` on the dedicated box; the SPA is
`//go:embed`'d). **No agent change, no DB, no config.** After the rebuild hit "Check for updates" in ⚙ (PWA
cache). Rides with the other pending web deploys (keybar, kill-session web). Confirm with the owner before
deploying.
