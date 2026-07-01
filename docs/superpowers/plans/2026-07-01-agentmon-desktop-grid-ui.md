# Desktop Grid + Sidebar UI Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Four small desktop web-only UI improvements: active-tile focus ring, a balanced count-based grid layout, a max-columns preference, and moving the sidebar ⋯ menu to the right edge.

**Architecture:** One pure function (`gridLayout`) drives the grid's columns/rows; a persisted `gridMaxColumns` pref (⚙ select) feeds it; `GridView` applies the result + a `focus-within` ring; `SessionActionsMenu`/`Sidebar` restructure to pin the ⋯ right. No Go/API/DB.

**Tech Stack:** Vite + React + TS, zustand (+persist), Tailwind, vitest + @testing-library/react. Run from `web/`: `npx vitest run …`, `npm run build`.

## Global Constraints

- **Web-only.** No Go, no API, no DB, no config. Deploy = hub rebuild only (SPA is `//go:embed`'d).
- **Layout formula:** `gridLayout(n, maxCols)` = `rows = ceil(n/maxCols)`, `cols = ceil(n/rows)`, with `n` and `maxCols` each clamped to `>= 1`. At maxCols=3: 1→1×1, 2→2×1, 3→3×1, 4→2×2, 5→3×2, 6→3×2.
- **`gridMaxColumns`** pref: default **3**, clamped to **[1, 4]**, persisted per-device (localStorage, like the other prefs).
- **Active highlight:** `focus-within:ring-2 focus-within:ring-primary focus-within:ring-inset` on each grid tile — pure CSS, no new state.
- **Sidebar ⋯:** pinned to the row's right edge; the **name-click-to-open bubbling must stay intact** (no `onClick` on the SessionActionsMenu outer span).
- Expand-to-full-screen, mobile, and the tile WS/scrollback lifecycle are **unchanged**.
- Commit after each task. Do NOT push or deploy without owner confirmation.

---

## File Structure

- **Create** `web/src/lib/grid-layout.ts` — pure `gridLayout(n, maxCols)`.
- **Create** `web/src/lib/grid-layout.test.ts` — its unit tests.
- **Modify** `web/src/store/prefs.ts` — add `gridMaxColumns` + `setGridMaxColumns` (clamped, persisted).
- **Create** `web/src/store/prefs.test.ts` — the clamp/default test (or append if it exists).
- **Modify** `web/src/components/SettingsPanel.tsx` — a "Grid columns" 1–4 select.
- **Modify** `web/src/components/GridView.tsx` — apply `gridLayout` cols/rows + the focus-within ring.
- **Modify** `web/src/components/SessionActionsMenu.tsx` — ⋯ pinned right (+ menu opens `right-0`).
- **Modify** `web/src/components/Sidebar.tsx` — row content `flex-1` so ⋯ reaches the right edge.

---

## Task 1: `gridLayout` pure function

**Files:**
- Create: `web/src/lib/grid-layout.ts`
- Test: `web/src/lib/grid-layout.test.ts`

**Interfaces:**
- Produces: `gridLayout(n: number, maxCols: number): { cols: number; rows: number }`.

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/grid-layout.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { gridLayout } from "./grid-layout";

describe("gridLayout", () => {
  it("maxCols=3 gives the balanced mapping (1..6)", () => {
    expect(gridLayout(1, 3)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(2, 3)).toEqual({ cols: 2, rows: 1 });
    expect(gridLayout(3, 3)).toEqual({ cols: 3, rows: 1 });
    expect(gridLayout(4, 3)).toEqual({ cols: 2, rows: 2 }); // 2×2 square, not 3+1
    expect(gridLayout(5, 3)).toEqual({ cols: 3, rows: 2 });
    expect(gridLayout(6, 3)).toEqual({ cols: 3, rows: 2 });
  });

  it("maxCols=2 makes 2-wide stacks", () => {
    expect(gridLayout(3, 2)).toEqual({ cols: 2, rows: 2 });
    expect(gridLayout(4, 2)).toEqual({ cols: 2, rows: 2 });
    expect(gridLayout(5, 2)).toEqual({ cols: 2, rows: 3 });
    expect(gridLayout(6, 2)).toEqual({ cols: 2, rows: 3 });
  });

  it("maxCols=1 stacks vertically", () => {
    expect(gridLayout(1, 1)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(3, 1)).toEqual({ cols: 1, rows: 3 });
  });

  it("maxCols=4 lets 4 sit in one row; 6 stays 3×2", () => {
    expect(gridLayout(4, 4)).toEqual({ cols: 4, rows: 1 });
    expect(gridLayout(6, 4)).toEqual({ cols: 3, rows: 2 });
  });

  it("clamps n and maxCols to at least 1", () => {
    expect(gridLayout(0, 3)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(3, 0)).toEqual({ cols: 1, rows: 3 });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/lib/grid-layout.test.ts`
Expected: FAIL — cannot resolve `./grid-layout`.

- [ ] **Step 3: Implement**

Create `web/src/lib/grid-layout.ts`:

```ts
// Balanced grid geometry for the desktop tile view. Given n tiles and a max
// column cap, fill rows up to maxCols, then even the columns out across the
// required rows so the grid stays near-square. At maxCols=3:
//   1→1×1  2→2×1  3→3×1  4→2×2  5→3×2  6→3×2
export function gridLayout(n: number, maxCols: number): { cols: number; rows: number } {
  const cap = Math.max(1, Math.floor(maxCols));
  const count = Math.max(1, Math.floor(n));
  const rows = Math.ceil(count / cap);
  const cols = Math.ceil(count / rows);
  return { cols, rows };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentmon/web && npx vitest run src/lib/grid-layout.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon
git add web/src/lib/grid-layout.ts web/src/lib/grid-layout.test.ts
git commit -m "feat(web): gridLayout — balanced count-based tile geometry"
```

---

## Task 2: `gridMaxColumns` preference + ⚙ select

**Files:**
- Modify: `web/src/store/prefs.ts`
- Test: `web/src/store/prefs.test.ts` (create)
- Modify: `web/src/components/SettingsPanel.tsx`

**Interfaces:**
- Produces: `usePrefs` state gains `gridMaxColumns: number` (default 3) + `setGridMaxColumns(n: number): void` (clamps [1,4]).

- [ ] **Step 1: Write the failing prefs test**

Create `web/src/store/prefs.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { usePrefs } from "./prefs";

describe("prefs gridMaxColumns", () => {
  it("defaults to 3", () => {
    expect(usePrefs.getState().gridMaxColumns).toBe(3);
  });
  it("setGridMaxColumns clamps to [1,4]", () => {
    usePrefs.getState().setGridMaxColumns(9);
    expect(usePrefs.getState().gridMaxColumns).toBe(4);
    usePrefs.getState().setGridMaxColumns(0);
    expect(usePrefs.getState().gridMaxColumns).toBe(1);
    usePrefs.getState().setGridMaxColumns(2);
    expect(usePrefs.getState().gridMaxColumns).toBe(2);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/store/prefs.test.ts`
Expected: FAIL — `gridMaxColumns` is `undefined` / `setGridMaxColumns is not a function`.

- [ ] **Step 3: Implement the pref**

In `web/src/store/prefs.ts`:

1. Add to the `PrefsState` interface (after `alertOnDone`):
```ts
  gridMaxColumns: number;
```
   and (after `setAlertOnDone`):
```ts
  setGridMaxColumns(n: number): void;
```

2. Add to the store object (after `alertOnDone: false,`):
```ts
      gridMaxColumns: 3,
```
   and (after the `setAlertOnDone` setter):
```ts
      setGridMaxColumns: (n) => set({ gridMaxColumns: Math.max(1, Math.min(4, Math.floor(n))) }),
```

3. Add to `partialize` (after `alertOnDone: s.alertOnDone,`):
```ts
        gridMaxColumns: s.gridMaxColumns,
```

- [ ] **Step 4: Run the prefs test**

Run: `cd /root/agentmon/web && npx vitest run src/store/prefs.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 5: Add the ⚙ "Grid columns" select**

In `web/src/components/SettingsPanel.tsx`:

1. Add the selectors alongside the others (after `const setTerminalTheme = usePrefs((s) => s.setTerminalTheme);` etc., near line 33):
```tsx
  const gridMaxColumns = usePrefs((s) => s.gridMaxColumns);
  const setGridMaxColumns = usePrefs((s) => s.setGridMaxColumns);
```

2. Insert this block immediately AFTER the "Terminal theme" `<div className="mb-3"> … </div>` (i.e. after the theme `<select>`'s closing `</div>`, before the `alertOnDone` checkbox label):
```tsx
          <div className="mb-3">
            <label htmlFor="settings-grid-cols" className="mb-1 block text-xs font-medium text-muted-foreground">
              Grid columns
            </label>
            <select
              id="settings-grid-cols"
              value={gridMaxColumns}
              onChange={(e) => setGridMaxColumns(Number(e.target.value))}
              className="h-8 w-full rounded-md border border-input bg-background px-2 text-sm"
            >
              {[1, 2, 3, 4].map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>
          </div>
```

- [ ] **Step 6: Full web suite + build**

Run: `cd /root/agentmon/web && npx vitest run && npm run build`
Expected: all tests pass; `tsc --noEmit && vite build` clean. (The select is UI wiring — its behavior is verified by tsc + on-device; the pref logic is covered by Step 1's test.)

- [ ] **Step 7: Commit**

```bash
cd /root/agentmon
git add web/src/store/prefs.ts web/src/store/prefs.test.ts web/src/components/SettingsPanel.tsx
git commit -m "feat(web): gridMaxColumns pref (default 3) + ⚙ Grid columns select"
```

---

## Task 3: `GridView` — balanced layout + active-tile ring

**Files:**
- Modify: `web/src/components/GridView.tsx`

**Interfaces:**
- Consumes: `gridLayout` (Task 1), `usePrefs().gridMaxColumns` (Task 2).

No new unit test: `GridView` renders live terminals (xterm + WS) that aren't machine-testable here; the layout math is already covered by Task 1's `gridLayout` tests, and the CSS (grid fill, focus ring) is verified by the build + owner on-device. Correctness is: the wiring compiles and applies the right template strings.

- [ ] **Step 1: Add the imports + pref selector**

In `web/src/components/GridView.tsx`:
1. Add import (with the other `@/` imports):
```tsx
import { gridLayout } from "@/lib/grid-layout";
```
2. Alongside the existing `fontSize`/`theme` selectors (BEFORE the `if (panes.length === 0)` early return, so hook order is stable):
```tsx
  const gridMaxColumns = usePrefs((s) => s.gridMaxColumns);
```

- [ ] **Step 2: Compute the layout + apply columns AND rows**

After the `if (panes.length === 0) { … }` early return (so `panes.length >= 1`), add:
```tsx
  const layout = gridLayout(panes.length, gridMaxColumns);
```

Replace the grid `<div>`'s `style` prop (currently):
```tsx
        style={{
          gridTemplateColumns: activeId ? "1fr" : "repeat(auto-fit, minmax(360px, 1fr))",
          // when expanded, the grid collapses to one cell; hidden tiles take no space
        }}
```
with:
```tsx
        style={
          activeId
            ? { gridTemplateColumns: "1fr" } // expanded: one cell full-screen (unchanged)
            : {
                gridTemplateColumns: `repeat(${layout.cols}, minmax(0, 1fr))`,
                gridTemplateRows: `repeat(${layout.rows}, minmax(0, 1fr))`,
              }
        }
```

- [ ] **Step 3: Add the active-tile focus ring**

On the per-tile wrapper `<div>` (the one with `key={...}` and
`className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border"`), append the ring
classes:
```tsx
              className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border focus-within:ring-2 focus-within:ring-primary focus-within:ring-inset"
```

- [ ] **Step 4: Build + full suite**

Run: `cd /root/agentmon/web && npm run build && npx vitest run`
Expected: `tsc --noEmit && vite build` clean; all tests pass (no regressions).
(If `ring-primary` does not resolve to a color in this Tailwind config, the build/PostCSS still succeeds but the ring would be invisible — verify the app already uses `bg-primary` (it does, in `ui/button.tsx`), so `primary` is a real color token and `ring-primary` resolves. If ever not, fall back to `focus-within:ring-ring`.)

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon
git add web/src/components/GridView.tsx
git commit -m "feat(web): balanced grid layout + active-tile focus ring"
```

---

## Task 4: Sidebar ⋯ menu to the right edge

**Files:**
- Modify: `web/src/components/SessionActionsMenu.tsx`
- Modify: `web/src/components/Sidebar.tsx`

**Interfaces:** none new — a layout-only restructure. The existing `SessionActionsMenu.test.tsx` must stay green (it covers the name-click-bubbles / ⋯-opens-menu / kill-flow behavior that this must not break).

- [ ] **Step 1: Pin the ⋯ right in `SessionActionsMenu`**

In `web/src/components/SessionActionsMenu.tsx`, in the idle `return (` block:

1. Change the outer span so it fills the row and right-aligns the ⋯:
```tsx
    <span className="flex w-full min-w-0 items-center gap-1">
```
   (was `className="inline-flex min-w-0 items-center gap-1"`).

2. Change the ⋯ container to push right:
```tsx
      <div className="relative flex-none ml-auto" ref={ref}>
```
   (was `className="relative flex-none"`).

3. Change the dropdown menu to open from the right edge (so it doesn't overflow the sidebar now that the ⋯ is at the right):
```tsx
          <div role="menu" className="absolute right-0 top-full z-20 mt-1 min-w-32 rounded-md border border-border bg-popover py-1 shadow-md">
```
   (was `className="absolute left-0 top-full …"`).

Leave everything else — including the outer span having NO `onClick` (name-click bubbling) and each button's `stop(e)` — unchanged.

- [ ] **Step 2: Let the row content fill the width in `Sidebar`**

In `web/src/components/Sidebar.tsx`, the session row's content wrapper is `<div className="min-w-0">`
(it holds `<SessionActionsMenu … />` and the cwd subtitle). Change it to:
```tsx
                <div className="min-w-0 flex-1">
```
so the `ml-auto` on the ⋯ reaches the row's right edge.

- [ ] **Step 3: Run the affected tests + full suite + build**

Run: `cd /root/agentmon/web && npx vitest run src/components/SessionActionsMenu.test.tsx && npx vitest run && npm run build`
Expected: the SessionActionsMenu tests still pass (name-click bubbles to `onOpen`, ⋯ opens the menu, kill flow) — proving the restructure didn't break behavior — and the full suite + build are green. (The ⋯'s new right position is CSS — verified on-device.)

- [ ] **Step 4: Commit**

```bash
cd /root/agentmon
git add web/src/components/SessionActionsMenu.tsx web/src/components/Sidebar.tsx
git commit -m "fix(web): move the sidebar session ⋯ menu to the row's right edge"
```

---

## Task 5: Verification, review, and finish

- [ ] **Step 1: Full web suite + build**

Run: `cd /root/agentmon/web && npx vitest run && npm run build`
Expected: all tests pass; `tsc --noEmit && vite build` clean.

- [ ] **Step 2: `/multi-review --codex` on the branch diff**

Invoke `multi-review` with `--codex` on the `feat-grid-ui-polish` branch diff. Apply real findings (regression test first for any logic bug), defer the rest with rationale.

- [ ] **Step 3: Carryover + finish**

Write `docs/superpowers/grid-ui-carryover.md` (what shipped, review outcome, the on-device visual checks the owner should do: the focus ring on the active tile, the balanced grid at maxCols 1–4, the ⋯ at the right edge). Then invoke `superpowers:finishing-a-development-branch`. **Deploy is web-only** → a **hub rebuild** on the dedicated box (no agent change); after it, hit "Check for updates" in ⚙. Confirm with the owner before deploying.

---

## Self-Review (against the spec)

- **Spec coverage:** §1 ring → Task 3 Step 3; §2 layout → Tasks 1 + 3; §3 pref/select → Task 2; §4 ⋯-right → Task 4; testing/deploy → Task 5. No gaps.
- **Placeholder scan:** every code step has complete code + exact commands; the one adapt-if-needed note (`ring-primary` fallback) names the exact condition + fallback, not a vague TODO.
- **Type consistency:** `gridLayout(n, maxCols) → {cols, rows}` and `gridMaxColumns`/`setGridMaxColumns` are used identically across Tasks 1–3. The ⋯ restructure is class-only; the `SessionActionsMenu` public props are unchanged.
- **Scope:** four small web-only items, five tasks (four build + one verification) — single plan.
