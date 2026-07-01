# Mobile Keep-Alive Terminals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make switching between session tabs in the mobile terminal view instant and flash-free by keeping opened terminals mounted + connected instead of remounting on every switch.

**Architecture:** A route-local pane pool (dies on ‹ Back → Level-1 scope). The mobile terminal view renders one persistent `TerminalView` per pooled pane, single-visible (focused shown, rest `display:none`) — the keep-mounted technique `GridView` already uses. Switching tabs changes the focused pane **in-state** (no navigation), so the target terminal is already live → instant reveal, no reconnect, no cross-session bleed. On entry, eagerly warm up to `MOBILE_POOL_CAP = 4` sessions.

**Tech Stack:** React 18 + TypeScript, TanStack Router, TanStack Query, Zustand (existing stores), xterm.js, Vitest + @testing-library/react.

**Design spec:** `docs/superpowers/specs/2026-07-01-agentmon-mobile-keepalive-terminals-design.md`

## Global Constraints

- All web code under `web/`; run all commands from `web/` (`cd web`).
- Gate before every commit: `npm run typecheck` (tsc --noEmit), `npx vitest run` (full suite), `npm run build`. All must pass.
- TDD: write the failing test first, watch it fail, implement, watch it pass, commit.
- Pane identity is `${serverId}:${target}:${paneId}` — name-independent (a rename must NOT change identity). This exact format already exists as a private `tabIdentity` in `MobileSessionTabs.tsx`; Task 1 promotes it to a shared helper.
- Do NOT change desktop `GridView` behavior or the desktop `usePanes` store. The new `active` prop on `TerminalView` defaults to `undefined` so existing callers are unaffected.
- Commit style: this repo ends commit messages with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Work on a feature branch off `main`; merge `--no-ff` at the end.

---

### Task 1: Shared `paneIdentity` helper

**Files:**
- Create: `web/src/lib/pane-identity.ts`
- Create: `web/src/lib/pane-identity.test.ts`
- Modify: `web/src/components/MobileSessionTabs.tsx` (replace the private `tabIdentity` with the shared helper)

**Interfaces:**
- Produces: `export const paneIdentity = (serverId: string, target: string, paneId: string): string`

- [ ] **Step 1: Write the failing test**

`web/src/lib/pane-identity.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { paneIdentity } from "@/lib/pane-identity";

describe("paneIdentity", () => {
  it("joins server:target:pane, independent of session name", () => {
    expect(paneIdentity("s1", "default", "%0")).toBe("s1:default:%0");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/lib/pane-identity.test.ts`
Expected: FAIL — cannot resolve `@/lib/pane-identity`.

- [ ] **Step 3: Create the helper**

`web/src/lib/pane-identity.ts`:
```ts
// A terminal pane's stable identity: server + tmux target + pane id. Deliberately
// NOT the session name (which is mutable via rename). Used to key the mobile tab
// strip and the mobile pane pool so a rename never re-identifies a pane.
export const paneIdentity = (serverId: string, target: string, paneId: string): string =>
  `${serverId}:${target}:${paneId}`;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/lib/pane-identity.test.ts`
Expected: PASS.

- [ ] **Step 5: Refactor `MobileSessionTabs.tsx` to use the shared helper**

In `web/src/components/MobileSessionTabs.tsx`, delete the local:
```ts
const tabIdentity = (serverId: string, target: string, paneId: string) =>
  `${serverId}:${target}:${paneId}`;
```
Add the import at the top:
```ts
import { paneIdentity } from "@/lib/pane-identity";
```
Replace the two `tabIdentity(...)` call sites in `buildTabs` with `paneIdentity(...)`.

- [ ] **Step 6: Run tests + typecheck**

Run: `npx vitest run src/components/MobileSessionTabs.test.tsx src/lib/pane-identity.test.ts && npm run typecheck`
Expected: all PASS (MobileSessionTabs behavior unchanged).

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/pane-identity.ts web/src/lib/pane-identity.test.ts web/src/components/MobileSessionTabs.tsx
git commit -m "$(printf 'refactor(web): shared paneIdentity helper\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: `useMobilePanePool` hook (pure reducer + hook)

**Files:**
- Create: `web/src/hooks/useMobilePanePool.ts`
- Create: `web/src/hooks/useMobilePanePool.test.ts`

**Interfaces:**
- Consumes: `paneIdentity` (Task 1).
- Produces:
  - `export const MOBILE_POOL_CAP = 4`
  - `export interface PoolPane { serverId: string; target: string; paneId: string }`
  - `export interface PoolState { panes: PoolPane[]; focusedId: string | null; lru: string[] }`
  - `export type PoolAction = { type: "open"; pane: PoolPane; focus: boolean } | { type: "focus"; id: string }`
  - `export const initialPool: PoolState`
  - `export function poolReducer(state: PoolState, action: PoolAction): PoolState`
  - `export function useMobilePanePool(): { panes: PoolPane[]; focusedId: string | null; open(p: PoolPane): void; openAndFocus(p: PoolPane): void; focus(id: string): void }`

- [ ] **Step 1: Write the failing tests (pure reducer)**

`web/src/hooks/useMobilePanePool.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { poolReducer, initialPool, MOBILE_POOL_CAP, type PoolPane } from "@/hooks/useMobilePanePool";
import { paneIdentity } from "@/lib/pane-identity";

const pane = (n: string): PoolPane => ({ serverId: "s1", target: "default", paneId: n });
const id = (n: string) => paneIdentity("s1", "default", n);

describe("poolReducer", () => {
  it("open adds a pane without focusing it", () => {
    const s = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: false });
    expect(s.panes.map((p) => p.paneId)).toEqual(["%0"]);
    expect(s.focusedId).toBeNull();
  });

  it("openAndFocus (open+focus) focuses the pane", () => {
    const s = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    expect(s.focusedId).toBe(id("%0"));
  });

  it("open dedupes by identity", () => {
    let s = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    s = poolReducer(s, { type: "open", pane: pane("%0"), focus: false });
    expect(s.panes).toHaveLength(1);
    expect(s.focusedId).toBe(id("%0")); // still focused
  });

  it("focus on an absent id is a no-op", () => {
    const s0 = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    const s1 = poolReducer(s0, { type: "focus", id: id("%9") });
    expect(s1).toBe(s0);
  });

  it("evicts the least-recently-focused non-focused pane past the cap", () => {
    let s = initialPool;
    // Open + focus cap panes in order %0.. so lru order is %0 (oldest) .. last (newest)
    for (let i = 0; i < MOBILE_POOL_CAP; i++) s = poolReducer(s, { type: "open", pane: pane(`%${i}`), focus: true });
    // Re-focus %0 so it is no longer the oldest; %1 becomes the eviction victim.
    s = poolReducer(s, { type: "focus", id: id("%0") });
    // Open+focus one more → over cap → evict %1 (least-recently-focused, not focused)
    s = poolReducer(s, { type: "open", pane: pane("%new"), focus: true });
    expect(s.panes).toHaveLength(MOBILE_POOL_CAP);
    expect(s.panes.some((p) => p.paneId === "%1")).toBe(false);
    expect(s.panes.some((p) => p.paneId === "%new")).toBe(true);
    expect(s.focusedId).toBe(id("%new"));
  });

  it("never evicts the focused pane", () => {
    let s = initialPool;
    for (let i = 0; i <= MOBILE_POOL_CAP; i++) s = poolReducer(s, { type: "open", pane: pane(`%${i}`), focus: true });
    expect(s.panes.some((p) => paneIdentity(p.serverId, p.target, p.paneId) === s.focusedId)).toBe(true);
    expect(s.panes).toHaveLength(MOBILE_POOL_CAP);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx vitest run src/hooks/useMobilePanePool.test.ts`
Expected: FAIL — cannot resolve `@/hooks/useMobilePanePool`.

- [ ] **Step 3: Implement the reducer + hook**

`web/src/hooks/useMobilePanePool.ts`:
```ts
import * as React from "react";
import { paneIdentity } from "@/lib/pane-identity";

// Max terminals kept live at once in the mobile terminal view (eager-warm ceiling +
// LRU-eviction bound). Well within the hub's per-principal relay cap; mirrors the
// desktop grid's bounded pool.
export const MOBILE_POOL_CAP = 4;

export interface PoolPane {
  serverId: string;
  target: string;
  paneId: string;
}

export interface PoolState {
  panes: PoolPane[];       // insertion order (stable render order)
  focusedId: string | null; // paneIdentity of the focused pane
  lru: string[];           // identities, least-recently-focused first
}

export type PoolAction =
  | { type: "open"; pane: PoolPane; focus: boolean }
  | { type: "focus"; id: string };

const idOf = (p: PoolPane) => paneIdentity(p.serverId, p.target, p.paneId);

export const initialPool: PoolState = { panes: [], focusedId: null, lru: [] };

export function poolReducer(state: PoolState, action: PoolAction): PoolState {
  switch (action.type) {
    case "open": {
      const id = idOf(action.pane);
      const exists = state.panes.some((p) => idOf(p) === id);
      let panes = exists ? state.panes : [...state.panes, action.pane];
      // LRU: focusing → most-recent (end). New-but-not-focused → least-recent (front),
      // so it is the first to be evicted if never focused. Existing-not-focused → unchanged.
      let lru: string[];
      if (action.focus) lru = [...state.lru.filter((x) => x !== id), id];
      else if (!exists) lru = [id, ...state.lru];
      else lru = state.lru;
      const focusedId = action.focus ? id : state.focusedId;
      // Evict the least-recently-focused pane that is neither focused nor just-added.
      while (panes.length > MOBILE_POOL_CAP) {
        const victim = lru.find((x) => x !== focusedId && x !== id);
        if (!victim) break;
        panes = panes.filter((p) => idOf(p) !== victim);
        lru = lru.filter((x) => x !== victim);
      }
      return { panes, focusedId, lru };
    }
    case "focus": {
      if (!state.panes.some((p) => idOf(p) === action.id)) return state; // absent → no-op
      return { ...state, focusedId: action.id, lru: [...state.lru.filter((x) => x !== action.id), action.id] };
    }
  }
}

export function useMobilePanePool() {
  const [state, dispatch] = React.useReducer(poolReducer, initialPool);
  return React.useMemo(
    () => ({
      panes: state.panes,
      focusedId: state.focusedId,
      open: (p: PoolPane) => dispatch({ type: "open", pane: p, focus: false }),
      openAndFocus: (p: PoolPane) => dispatch({ type: "open", pane: p, focus: true }),
      focus: (id: string) => dispatch({ type: "focus", id }),
    }),
    [state],
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/hooks/useMobilePanePool.test.ts`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useMobilePanePool.ts web/src/hooks/useMobilePanePool.test.ts
git commit -m "$(printf 'feat(web): mobile pane pool (keep-alive state + LRU cap)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: `TerminalView` gains an `active` prop (focus handoff)

**Files:**
- Modify: `web/src/components/TerminalView.tsx`
- Modify: `web/src/components/TerminalView.test.tsx`

**Interfaces:**
- Produces: `TerminalView` accepts an optional `active?: boolean`. When it becomes `true`, the xterm is focused. `undefined` (existing callers) = no new behavior.

Current `TerminalView` props: `{ serverId, paneId, target, showKeyBar?, fontSize?, theme? }`. It calls `useTerminalSession(targetObj)` which returns `{ xtermRef, controller, connected, everConnected, handleData, handleResize }`. `xtermRef.current?.focus()` focuses the terminal.

- [ ] **Step 1: Read the existing test to match its mocking style**

Read `web/src/components/TerminalView.test.tsx` to see how `useTerminalSession` / `XTerm` are mocked. Mirror that style in the new test.

- [ ] **Step 2: Write the failing test**

Add to `web/src/components/TerminalView.test.tsx` a test asserting that when `active` transitions to `true`, the xterm handle's `focus()` is called, and when rendered without `active`, `focus()` is NOT called on mount. Mock `useTerminalSession` so `xtermRef` points at an object with a `focus` spy. Concretely (adapt imports/mocks to the file's existing setup):
```tsx
it("focuses the terminal when it becomes active", () => {
  const focus = vi.fn();
  // Arrange the useTerminalSession mock so xtermRef.current = { focus, ... }
  const { rerender } = render(<TerminalView serverId="s1" paneId="%0" target="default" active={false} />);
  expect(focus).not.toHaveBeenCalled();
  rerender(<TerminalView serverId="s1" paneId="%0" target="default" active={true} />);
  expect(focus).toHaveBeenCalledTimes(1);
});

it("does not focus on mount when active is undefined (grid path unchanged)", () => {
  const focus = vi.fn();
  render(<TerminalView serverId="s1" paneId="%0" target="default" />);
  expect(focus).not.toHaveBeenCalled();
});
```
NOTE: the existing `TerminalView.test.tsx` mocks `useTerminalSession`; extend that mock to expose a `focus` spy on `xtermRef.current`. If the current mock returns a bare `xtermRef`, set `xtermRef.current = { focus, write: vi.fn(), fit: vi.fn(), reset: vi.fn(), blur: vi.fn(), appCursor: () => false, getSelection: () => "", paste: vi.fn(), scrollLines: vi.fn() }`.

- [ ] **Step 3: Run test to verify it fails**

Run: `npx vitest run src/components/TerminalView.test.tsx`
Expected: FAIL — `active` prop not handled / `focus` not called.

- [ ] **Step 4: Implement the `active` prop**

In `web/src/components/TerminalView.tsx`:
1. Add `active` to the props type: `active?: boolean;` and to the destructure: `{ serverId, paneId, target, showKeyBar = false, active, fontSize, theme }`.
2. After the `useTerminalSession(...)` call, add:
```tsx
// When this pane becomes the visible/focused one in the mobile pool, hand keyboard
// focus to its xterm. Guarded on active===true so existing callers (grid) that pass
// no `active` are unaffected.
React.useEffect(() => {
  if (active) xtermRef.current?.focus();
}, [active]);
```
(`React` is already imported; `xtermRef` is already in scope.)

- [ ] **Step 5: Run test to verify it passes**

Run: `npx vitest run src/components/TerminalView.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/TerminalView.tsx web/src/components/TerminalView.test.tsx
git commit -m "$(printf 'feat(web): TerminalView active prop focuses xterm on reveal\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: `MobileTerminalStack` (single-visible mounted pool)

**Files:**
- Create: `web/src/components/MobileTerminalStack.tsx`
- Create: `web/src/components/MobileTerminalStack.test.tsx`

**Interfaces:**
- Consumes: `PoolPane` (Task 2), `paneIdentity` (Task 1), `TerminalView` with `active`/`showKeyBar` (Task 3).
- Produces: `export function MobileTerminalStack({ panes, focusedId, fontSize, theme }: { panes: PoolPane[]; focusedId: string | null; fontSize: number; theme: ITheme }): JSX.Element`

- [ ] **Step 1: Write the failing test**

`web/src/components/MobileTerminalStack.test.tsx` (mock `TerminalView` to a marker, as `terminal.test.tsx` does):
```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => (
    <div data-testid={`tv-${p.paneId}`} data-active={String(!!p.active)} data-keybar={String(!!p.showKeyBar)} />
  ),
}));

import { MobileTerminalStack } from "@/components/MobileTerminalStack";
import { paneIdentity } from "@/lib/pane-identity";
import { TERMINAL_THEMES } from "@/lib/terminal-themes";

const panes = [
  { serverId: "s1", target: "default", paneId: "%0" },
  { serverId: "s1", target: "default", paneId: "%1" },
];

describe("MobileTerminalStack", () => {
  it("mounts a terminal per pane; only the focused one is active + shows the key bar + is visible", () => {
    const focusedId = paneIdentity("s1", "default", "%1");
    const { container } = render(
      <MobileTerminalStack panes={panes} focusedId={focusedId} fontSize={13} theme={TERMINAL_THEMES.dark} />,
    );
    // both mounted (keep-alive)
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
    // only focused is active + has the key bar
    expect(screen.getByTestId("tv-%1").getAttribute("data-active")).toBe("true");
    expect(screen.getByTestId("tv-%0").getAttribute("data-active")).toBe("false");
    expect(screen.getByTestId("tv-%1").getAttribute("data-keybar")).toBe("true");
    expect(screen.getByTestId("tv-%0").getAttribute("data-keybar")).toBe("false");
    // only focused wrapper is visible
    const wrappers = Array.from(container.querySelectorAll("[data-pane-wrapper]")) as HTMLElement[];
    const visible = wrappers.filter((w) => w.style.display !== "none");
    expect(visible).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/components/MobileTerminalStack.test.tsx`
Expected: FAIL — cannot resolve `@/components/MobileTerminalStack`.

- [ ] **Step 3: Implement the component**

`web/src/components/MobileTerminalStack.tsx`:
```tsx
import type { ITheme } from "@xterm/xterm";
import { TerminalView } from "@/components/TerminalView";
import { paneIdentity } from "@/lib/pane-identity";
import type { PoolPane } from "@/hooks/useMobilePanePool";

// Renders the mobile pane pool single-visible: every pooled pane stays mounted (its own
// socket + scrollback survive), only the focused one is shown. Mirrors GridView's
// keep-mounted trick so switching is instant with no reconnect and no cross-session bleed.
export function MobileTerminalStack({
  panes, focusedId, fontSize, theme,
}: {
  panes: PoolPane[];
  focusedId: string | null;
  fontSize: number;
  theme: ITheme;
}) {
  return (
    <div className="relative h-full w-full">
      {panes.map((p) => {
        const id = paneIdentity(p.serverId, p.target, p.paneId);
        const active = id === focusedId;
        return (
          <div
            key={id}
            data-pane-wrapper
            className="absolute inset-0"
            style={{ display: active ? "flex" : "none" }}
          >
            <TerminalView
              serverId={p.serverId}
              paneId={p.paneId}
              target={p.target}
              showKeyBar={active}
              active={active}
              fontSize={fontSize}
              theme={theme}
            />
          </div>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/components/MobileTerminalStack.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/MobileTerminalStack.tsx web/src/components/MobileTerminalStack.test.tsx
git commit -m "$(printf 'feat(web): MobileTerminalStack renders the pane pool single-visible\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: Wire the pool into `MobileTerminalRoute` (integration)

**Files:**
- Modify: `web/src/routes/terminal.tsx`
- Modify: `web/src/routes/terminal.test.tsx`

**Interfaces:**
- Consumes: `useMobilePanePool`, `MOBILE_POOL_CAP` (Task 2); `MobileTerminalStack` (Task 4); `paneIdentity` (Task 1); existing `MobileSessionTabs`/`buildTabs`, `useFocusedSeen`, `flattenSessions`, `effectiveSessionState`, `serversKey`/`sessionsKey`.

This task replaces the single URL-driven `TerminalView` with the pool + stack, switches tabs in-state, and drives `useFocusedSeen` off the focused pane.

- [ ] **Step 1: Rewrite `MobileTerminalRoute`**

Replace the body of `web/src/routes/terminal.tsx`'s `MobileTerminalRoute` with the following (keep the file's existing imports and ADD the new ones shown):

New imports to add:
```ts
import * as React from "react";
import { MobileTerminalStack } from "@/components/MobileTerminalStack";
import { useMobilePanePool, MOBILE_POOL_CAP } from "@/hooks/useMobilePanePool";
import { paneIdentity } from "@/lib/pane-identity";
```
(Keep existing imports: `useNavigate/useParams/useSearch`, `useQuery/useQueries`, `Button`, `MobileSessionTabs`/`buildTabs`, `flattenSessions`/`SessionRow`, `listServers`/`listSessions`/`serversKey`/`sessionsKey`, `useStateSnapshot`, `effectiveSessionState`, `useFocusedSeen`, `useVisualViewport`, `usePrefs`, `themeOf`, `Session`/`SessionState`. REMOVE the direct single `TerminalView` import if it is no longer referenced — it is now used only inside `MobileTerminalStack`.)

Component body:
```tsx
export function MobileTerminalRoute() {
  const { serverId, paneId } = useParams({ strict: false }) as { serverId: string; paneId: string };
  const { target, session } = useSearch({ strict: false }) as TerminalSearch;
  const navigate = useNavigate();
  const fontSize = usePrefs((s) => s.fontSizeMobile);
  const theme = themeOf(usePrefs((s) => s.terminalTheme));

  // Session list for the tab strip + eager warming (same cached keys as the inbox).
  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers, staleTime: 15_000 });
  const servers = serversQ.data ?? [];
  const sessionQs = useQueries({
    queries: servers.map((s) => ({ queryKey: sessionsKey(s.id), queryFn: () => listSessions(s.id), staleTime: 15_000 })),
  });
  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);
  const snap = useStateSnapshot();
  const stateOf = (row: SessionRow): SessionState =>
    effectiveSessionState(snap, row.server.id, row.session.target, row.session.name, row.session.state);

  // Keep-alive pool (route-local → dies on Back). Seed the entered pane, focused.
  const pool = useMobilePanePool();
  React.useEffect(() => {
    pool.openAndFocus({ serverId, target, paneId });
    // Mount-only: the entered pane is fixed for this route mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Eager-warm up to the cap once the session list first arrives (focused always included).
  const warmedRef = React.useRef(false);
  React.useEffect(() => {
    if (warmedRef.current || rows.length === 0) return;
    warmedRef.current = true;
    const focusedIdent = paneIdentity(serverId, target, paneId);
    let warmed = 1; // the seeded/focused pane counts toward the cap
    for (const r of rows) {
      if (warmed >= MOBILE_POOL_CAP) break;
      const rid = paneIdentity(r.server.id, r.session.target, r.pane.id);
      if (rid === focusedIdent) continue;
      pool.open({ serverId: r.server.id, target: r.session.target, paneId: r.pane.id });
      warmed++;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows.length]);

  // The focused pane drives the header/tabs + seen tracking. Fall back to the URL pane
  // until the seed effect lands. The focused session NAME comes from the list (so a
  // rename reflects on refetch); fall back to the URL session name before the list loads.
  const focused = pool.panes.find(
    (p) => paneIdentity(p.serverId, p.target, p.paneId) === pool.focusedId,
  ) ?? { serverId, target, paneId };
  const focusedRow = rows.find(
    (r) => paneIdentity(r.server.id, r.session.target, r.pane.id) === paneIdentity(focused.serverId, focused.target, focused.paneId),
  );
  const focusedName = focusedRow?.session.name ?? session;

  useFocusedSeen({ serverId: focused.serverId, target: focused.target, sessionName: focusedName });

  const tabs = buildTabs(rows, { serverId: focused.serverId, target: focused.target, session: focusedName, paneId: focused.paneId }, stateOf);

  const { height: vvHeight } = useVisualViewport();

  return (
    <div className="flex h-full flex-col" style={{ height: vvHeight ? `${vvHeight}px` : undefined }}>
      <header
        className="flex items-center gap-2 border-b border-border bg-background px-2 py-2"
        style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}
      >
        <Button variant="ghost" size="sm" className="flex-none" onClick={() => navigate({ to: "/" })}>‹ Back</Button>
        <MobileSessionTabs
          tabs={tabs}
          onSwitch={(tab) => pool.openAndFocus({ serverId: tab.serverId, target: tab.target, paneId: tab.paneId })}
          onRenamed={() => { /* rename reflects via the sessions refetch; URL is only the entry point */ }}
        />
      </header>
      <div className="min-h-0 flex-1">
        <MobileTerminalStack panes={pool.panes} focusedId={pool.focusedId} fontSize={fontSize} theme={theme} />
      </div>
    </div>
  );
}
```

Key removals vs. the current file:
- The single `<TerminalView key={`${serverId}:${target}:${paneId}`} … />` block is gone (replaced by `<MobileTerminalStack …>`). The `key` remount hack is intentionally removed — the pool prevents both the flash and the bleed.
- The per-switch `navigate({ to: "/t/$serverId/$paneId", …, replace: true })` in `onSwitch` is gone (switching is now in-state via `pool.openAndFocus`).
- `onRenamed` no longer navigates.

- [ ] **Step 2: Typecheck**

Run: `npm run typecheck`
Expected: PASS. (If `TerminalView` is reported unused in `terminal.tsx`, remove its import.)

- [ ] **Step 3: Update `terminal.test.tsx` and add a switch-in-state assertion**

The existing two tests should still pass (mount seeds the pool → one mocked `TerminalView` with `showKeyBar` true → `"s1:%0:default:true"`; `useFocusedSeen` fires with `demo-web` via the URL fallback). Verify by running them first:

Run: `npx vitest run src/routes/terminal.test.tsx`
Expected: the two existing tests PASS.

If they fail because `MobileTerminalStack`/pool changes the DOM (e.g. the marker text), adjust the assertions minimally to match (the mocked `TerminalView` still receives `serverId="s1" paneId="%0" target="default" showKeyBar={true}`).

Then add a test proving a tab switch does NOT navigate. Extend the react-query mock so the list has two sessions, and assert tapping the other tab calls neither `navigate` nor a route change. Because `useNavigate` is mocked to a shared `vi.fn()`, capture it:
```tsx
const navigateSpy = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ serverId: "s1", paneId: "%0" }),
  useSearch: () => ({ target: "default", session: "alpha" }),
  useNavigate: () => navigateSpy,
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [{ id: "s1", name: "host-1", labels: [], enabled: true }] }),
  useQueries: () => [{ data: [
    { name: "alpha", server: "s1", target: "default", cwd: "/a", command: "c", windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] },
    { name: "beta",  server: "s1", target: "default", cwd: "/b", command: "c", windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "c", cwd: "/b" }] }] },
  ] }],
}));
```
```tsx
it("switches tabs in-state without navigating", async () => {
  navigateSpy.mockClear();
  render(<MobileTerminalRoute />);
  await userEvent.click(screen.getByText("beta")); // the inactive tab
  expect(navigateSpy).not.toHaveBeenCalled();       // in-state focus, not a route change
  // both panes are now mounted (keep-alive): two mocked terminals present
  expect(screen.getAllByTestId(/^tv/).length).toBeGreaterThanOrEqual(2);
});
```
NOTE: keep the mocked `TerminalView` marker (`data-testid={`tv-${p.paneId}`}` style, or the existing `tv` testid) consistent so `getAllByTestId(/^tv/)` matches. The existing mock uses a single `data-testid="tv"`; update it to include the paneId so multiple panes are distinguishable (`data-testid={`tv-${p.paneId}`}`) and adjust the first test's `getByTestId` accordingly. Import `userEvent` from `@testing-library/user-event`.

- [ ] **Step 4: Run the route tests**

Run: `npx vitest run src/routes/terminal.test.tsx`
Expected: PASS (existing two, adapted, + the new switch test).

- [ ] **Step 5: Full gate**

Run: `npm run typecheck && npx vitest run && npm run build`
Expected: typecheck clean, ALL tests pass, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/src/routes/terminal.tsx web/src/routes/terminal.test.tsx
git commit -m "$(printf 'feat(web): mobile terminal keep-alive pool (instant tab switching)\n\nSwitch tabs in-state over a mounted pane pool instead of remounting the\nterminal per switch. Removes the connect flash and the cross-session\nscrollback bleed. Level-1: pool dies on Back.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: Manual device verification + finish

**Files:** none (verification), then branch merge.

- [ ] **Step 1: Build and run locally**, exercise the mobile terminal view (mobile viewport / device):
  - Switching between tabs is **instant, no "connecting…"** and shows the correct session's content (no bleed).
  - The **soft keyboard stays up** across a switch and typing lands in the newly-focused terminal.
  - **Rename** the focused session → the tab label updates (after the brief refetch), pool undisturbed.
  - **‹ Back** returns to the list; re-opening a session reconnects (expected Level-1).
  - With >4 sessions, switching still works (older panes reconnect on revisit).
- [ ] **Step 2: If refit is stale after a reveal** (wrong cols/rows), add a resize nudge: in `TerminalView`, when `active` flips true, also call `handleResize` after `fit()` — but only add this if observed; do not pre-optimize.
- [ ] **Step 3: Merge the feature branch** `--no-ff` into `main`, push, delete the branch. (Optionally run `/multi-review` first — this feature touches lifecycle/focus, so it is a good candidate.)

---

## Self-Review

**Spec coverage:**
- Instant switching / keep-mounted pool → Tasks 2, 4, 5. ✅
- `TerminalView` `active` focus handoff → Task 3. ✅
- Eager warm (cap 4) + LRU → Task 2 (reducer/cap) + Task 5 (warm effect). ✅
- `useFocusedSeen` off focused pane → Task 5. ✅
- Key bar only on focused pane → Task 4. ✅
- Remove per-switch navigate + `TerminalView` key → Task 5. ✅
- Shared `paneIdentity` (dedup with tabs) → Task 1. ✅
- Rename undisturbed (identity name-independent; label from list) → Task 5. ✅
- Level-1 (pool dies on Back) → route-local pool, Task 5. ✅
- Testing (pool, stack, TerminalView active, route) → Tasks 2–5. ✅

**Placeholder scan:** none — every code step contains full code.

**Type consistency:** `PoolPane { serverId, target, paneId }` used consistently in Tasks 2/4/5; `paneIdentity(serverId, target, paneId)` signature consistent; `useMobilePanePool` returns `{ panes, focusedId, open, openAndFocus, focus }` consumed exactly in Task 5; `TerminalView` `active?: boolean` defined in Task 3, consumed in Task 4.

**Note carried to execution:** `PoolPane` intentionally omits `session`/`serverName` — the stack needs only identity fields, and the focused session name is derived from the live session list (so a rename reflects on refetch). This is why Task 5 has no pool `rename` action.
