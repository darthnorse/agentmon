# Mobile Close-Tab (Explicit Open Set) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the mobile session tab bar a per-tab close (`×`) that removes a tab without killing the tmux session, backed by an explicit, per-device-persisted open set.

**Architecture:** A new persisted zustand store (`useMobileOpenTabs`) holds the ordered set of open tab identities. `buildTabs` is re-sourced from that set (∩ live sessions). The route (`terminal.tsx`) adds the entered pane to the set on entry, warms the set into the existing route-local pane pool, and handles close by removing from the set + evicting from the pool (via a new `close` pool action), focusing a neighbor when the active tab is closed and navigating home when the last tab is closed.

**Tech Stack:** React 18, TypeScript, zustand (+ `persist` middleware), TanStack Router/Query, Vitest + Testing Library. Package manager: **npm**. Web-only (`web/`), no Go/agent/API/DB changes.

## Global Constraints

- **Web-only.** Touch only files under `web/src/`. No Go, hub, agent, API, or DB changes.
- **Close ≠ kill.** Never send or reuse any tmux `kill-session` path. Close is purely client-side state + socket teardown; the tmux session must keep running.
- **Identity key:** always `paneIdentity(serverId, target, paneId)` from `@/lib/pane-identity` — never key on the mutable session name.
- **Persistence:** per-device via `localStorage` (single-user v1), mirroring `usePrefs` (`persist` + `partialize` persists data fields only).
- **Desktop untouched:** do not modify `usePanes`, `GridView`, `DesktopShell`, or the sidebar.
- **Commands:** `npm run test:run -- <path>` (targeted tests), `npm run typecheck`, `npm run build`. Run all from `web/`.

## File Structure

- **Create** `web/src/store/mobile-open-tabs.ts` — persisted open-set store (state + `add`/`remove`/`has`).
- **Create** `web/src/store/mobile-open-tabs.test.ts` — store unit tests.
- **Modify** `web/src/hooks/useMobilePanePool.ts` — add a `close` action to `poolReducer` + `close` to the hook.
- **Modify** `web/src/hooks/useMobilePanePool.test.ts` — tests for `close`.
- **Modify** `web/src/components/MobileSessionTabs.tsx` — `buildTabs` sourced from the open set; new pure `nextFocusAfterClose`; component renders `×` on every tab, drops the active-tab inline rename editor, replaces `onRenamed` with `onClose`.
- **Modify** `web/src/components/MobileSessionTabs.test.tsx` — updated `buildTabs` / component tests + `nextFocusAfterClose` tests.
- **Modify** `web/src/routes/terminal.tsx` — open-set add on entry, warm from the open set, `onClose` handler (non-active / active-neighbor / last-tab-navigate), drop `onRenamed`.

---

### Task 1: Persisted open-set store

**Files:**
- Create: `web/src/store/mobile-open-tabs.ts`
- Test: `web/src/store/mobile-open-tabs.test.ts`

**Interfaces:**
- Consumes: `paneIdentity` from `@/lib/pane-identity`.
- Produces:
  - `interface OpenTab { serverId: string; target: string; paneId: string; }`
  - `useMobileOpenTabs` — zustand store with `open: OpenTab[]`, `add(t: OpenTab): void`, `remove(id: string): void`, `has(id: string): boolean`.
  - `const OPEN_TABS_STORAGE_KEY = "agentmon-mobile-open-tabs"`.

- [ ] **Step 1: Write the failing test**

Create `web/src/store/mobile-open-tabs.test.ts`:

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { useMobileOpenTabs, OPEN_TABS_STORAGE_KEY, type OpenTab } from "@/store/mobile-open-tabs";
import { paneIdentity } from "@/lib/pane-identity";

const t = (paneId: string): OpenTab => ({ serverId: "s1", target: "default", paneId });
const id = (paneId: string) => paneIdentity("s1", "default", paneId);

beforeEach(() => {
  localStorage.clear();
  useMobileOpenTabs.setState({ open: [] });
});

describe("useMobileOpenTabs", () => {
  it("add appends in insertion order", () => {
    useMobileOpenTabs.getState().add(t("%0"));
    useMobileOpenTabs.getState().add(t("%1"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%0", "%1"]);
  });

  it("add is idempotent (no duplicate, no reorder)", () => {
    const s = useMobileOpenTabs.getState();
    s.add(t("%0"));
    s.add(t("%1"));
    s.add(t("%0"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%0", "%1"]);
  });

  it("remove drops by identity", () => {
    const s = useMobileOpenTabs.getState();
    s.add(t("%0"));
    s.add(t("%1"));
    s.remove(id("%0"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%1"]);
  });

  it("has reflects membership", () => {
    useMobileOpenTabs.getState().add(t("%0"));
    expect(useMobileOpenTabs.getState().has(id("%0"))).toBe(true);
    expect(useMobileOpenTabs.getState().has(id("%9"))).toBe(false);
  });

  it("persists the open set to localStorage", () => {
    useMobileOpenTabs.getState().add(t("%0"));
    expect(localStorage.getItem(OPEN_TABS_STORAGE_KEY)).toContain("%0");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run test:run -- src/store/mobile-open-tabs.test.ts`
Expected: FAIL — cannot resolve `@/store/mobile-open-tabs`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/store/mobile-open-tabs.ts`:

```ts
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { paneIdentity } from "@/lib/pane-identity";

export const OPEN_TABS_STORAGE_KEY = "agentmon-mobile-open-tabs";

// A session the user has explicitly opened on mobile. Keyed by pane identity
// (serverId:target:paneId) — stable across rename (target, not the mutable name) and
// across reload (tmux paneId persists). The session NAME is never stored; it is always
// resolved from the live session list so a rename reflects on refetch.
export interface OpenTab {
  serverId: string;
  target: string;
  paneId: string;
}

interface MobileOpenTabsState {
  open: OpenTab[]; // ordered = tab render order (insertion order)
  add(t: OpenTab): void; // append if absent (idempotent)
  remove(id: string): void; // remove by paneIdentity
  has(id: string): boolean;
}

const idOf = (t: OpenTab) => paneIdentity(t.serverId, t.target, t.paneId);

// Per-device open set persisted to localStorage (v1 is single-user, one device — a hub
// table is a later add). Drives the mobile tab bar and pane warming.
export const useMobileOpenTabs = create<MobileOpenTabsState>()(
  persist(
    (set, get) => ({
      open: [],
      add: (t) =>
        set((s) => (s.open.some((x) => idOf(x) === idOf(t)) ? s : { open: [...s.open, t] })),
      remove: (id) => set((s) => ({ open: s.open.filter((x) => idOf(x) !== id) })),
      has: (id) => get().open.some((x) => idOf(x) === id),
    }),
    {
      name: OPEN_TABS_STORAGE_KEY,
      partialize: (s) => ({ open: s.open }), // persist data only, never the actions
    },
  ),
);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm run test:run -- src/store/mobile-open-tabs.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/store/mobile-open-tabs.ts web/src/store/mobile-open-tabs.test.ts
git commit -m "feat(web): persisted mobile open-tabs store"
```

---

### Task 2: `close` action on the mobile pane pool

**Files:**
- Modify: `web/src/hooks/useMobilePanePool.ts`
- Test: `web/src/hooks/useMobilePanePool.test.ts`

**Interfaces:**
- Consumes: existing `poolReducer`, `PoolState`, `PoolAction`, `idOf`, `initialPool`, `MOBILE_POOL_CAP`, `PoolPane`.
- Produces:
  - New action variant `{ type: "close"; id: string }` handled by `poolReducer`.
  - New hook method `close(id: string): void`.

- [ ] **Step 1: Write the failing test**

Append to `web/src/hooks/useMobilePanePool.test.ts` inside the existing `describe("poolReducer", …)` block (before its closing `});`):

```ts
  it("close evicts a non-focused pane and cleans lru, leaving focus intact", () => {
    let s = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    s = poolReducer(s, { type: "open", pane: pane("%1"), focus: false });
    s = poolReducer(s, { type: "close", id: id("%1") });
    expect(s.panes.map((p) => p.paneId)).toEqual(["%0"]);
    expect(s.lru).not.toContain(id("%1"));
    expect(s.focusedId).toBe(id("%0"));
  });

  it("close on the focused pane clears focusedId", () => {
    let s = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    s = poolReducer(s, { type: "close", id: id("%0") });
    expect(s.panes).toHaveLength(0);
    expect(s.lru).toHaveLength(0);
    expect(s.focusedId).toBeNull();
  });

  it("close on an absent id is a no-op (same reference)", () => {
    const s0 = poolReducer(initialPool, { type: "open", pane: pane("%0"), focus: true });
    const s1 = poolReducer(s0, { type: "close", id: id("%9") });
    expect(s1).toBe(s0);
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run test:run -- src/hooks/useMobilePanePool.test.ts`
Expected: FAIL — TypeScript rejects `{ type: "close" }` (not assignable to `PoolAction`) / reducer has no `close` case.

- [ ] **Step 3: Write minimal implementation**

In `web/src/hooks/useMobilePanePool.ts`:

Add the variant to the `PoolAction` union:

```ts
export type PoolAction =
  | { type: "open"; pane: PoolPane; focus: boolean }
  | { type: "focus"; id: string }
  | { type: "close"; id: string };
```

Add a `case "close"` to `poolReducer` (immediately before the closing `}` of the `switch`, after the `focus` case):

```ts
    case "close": {
      if (!state.panes.some((p) => idOf(p) === action.id)) return state; // absent → no-op
      return {
        panes: state.panes.filter((p) => idOf(p) !== action.id),
        lru: state.lru.filter((x) => x !== action.id),
        // The route re-focuses a neighbor explicitly; a null focus for one render is safe.
        focusedId: state.focusedId === action.id ? null : state.focusedId,
      };
    }
```

Expose `close` from the hook — add to the `React.useMemo` object returned by `useMobilePanePool`:

```ts
      close: (id: string) => dispatch({ type: "close", id }),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm run test:run -- src/hooks/useMobilePanePool.test.ts`
Expected: PASS (existing tests + 3 new).

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useMobilePanePool.ts web/src/hooks/useMobilePanePool.test.ts
git commit -m "feat(web): add close action to mobile pane pool"
```

---

### Task 3: `buildTabs` from the open set + close UI in `MobileSessionTabs`

**Files:**
- Modify: `web/src/components/MobileSessionTabs.tsx`
- Test: `web/src/components/MobileSessionTabs.test.tsx`

**Interfaces:**
- Consumes: `OpenTab` from `@/store/mobile-open-tabs`; existing `SessionTab`, `CurrentSession`, `SessionRow`, `paneIdentity`, `StateDot`.
- Produces:
  - `buildTabs(openTabs: OpenTab[], rows: SessionRow[], current: CurrentSession, stateOf: (row: SessionRow) => SessionState): SessionTab[]` (new **first** parameter `openTabs`).
  - `nextFocusAfterClose(tabs: SessionTab[], closingKey: string): SessionTab | null` (pure).
  - `MobileSessionTabs({ tabs, onSwitch, onClose })` — `onClose(tab: SessionTab): void` replaces `onRenamed`.

> NOTE: this task changes the `buildTabs` signature and `MobileSessionTabs` props, so `web/src/routes/terminal.tsx` (the only non-test caller) will not typecheck until Task 4. That is expected — verify this task via the **targeted test run** below, not full typecheck.

- [ ] **Step 1: Write the failing test**

Replace the entire contents of `web/src/components/MobileSessionTabs.test.tsx` with:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileSessionTabs, buildTabs, nextFocusAfterClose } from "@/components/MobileSessionTabs";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { OpenTab } from "@/store/mobile-open-tabs";
import type { SessionState } from "@/lib/contracts";

const servers = [{ id: "s1", name: "host-1", labels: [], enabled: true }];
function mkSession(name: string, winId: string, paneId: string) {
  return {
    name, server: "s1", target: "default", cwd: `/home/${name}`, command: "claude",
    windows: [{ id: winId, index: "0", name: "m", panes: [{ id: paneId, command: "c", cwd: `/home/${name}` }] }],
  };
}
const byServer = { s1: [mkSession("alpha", "@0", "%0"), mkSession("beta", "@1", "%1"), mkSession("gamma", "@2", "%2")] };
const rows = flattenSessions(servers, byServer);
const idle = (): SessionState => "idle";
const current = { serverId: "s1", target: "default", session: "beta", paneId: "%1" };
const open = (paneId: string): OpenTab => ({ serverId: "s1", target: "default", paneId });
const openAll: OpenTab[] = [open("%0"), open("%1"), open("%2")];

describe("buildTabs", () => {
  it("renders open-set members in order and marks the current active", () => {
    const tabs = buildTabs(openAll, rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["alpha", "beta", "gamma"]);
    expect(tabs.filter((t) => t.active).map((t) => t.name)).toEqual(["beta"]);
  });

  it("honors open-set order (not row order)", () => {
    const tabs = buildTabs([open("%2"), open("%1"), open("%0")], rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["gamma", "beta", "alpha"]);
    expect(tabs.filter((t) => t.active).map((t) => t.name)).toEqual(["beta"]);
  });

  it("skips an open tab absent from the live rows (no dead tab)", () => {
    const tabs = buildTabs([open("%1"), open("%ghost")], rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["beta"]);
  });

  it("synthesizes an active tab when the current session isn't in the rows yet", () => {
    const tabs = buildTabs([open("%9")], [], { serverId: "s1", target: "default", session: "solo", paneId: "%9" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ name: "solo", active: true, state: "unknown" });
  });

  it("carries each row's state through from stateOf", () => {
    const stateOf = (r: SessionRow): SessionState => (r.session.name === "gamma" ? "blocked" : "idle");
    const tabs = buildTabs(openAll, rows, current, stateOf);
    expect(tabs.find((t) => t.name === "gamma")?.state).toBe("blocked");
  });

  it("keeps a single active tab (no phantom) when the URL name leads the cached list mid-rename", () => {
    const stale = flattenSessions(servers, { s1: [mkSession("old-name", "@1", "%1")] });
    const tabs = buildTabs([open("%1")], stale, { serverId: "s1", target: "default", session: "new-name", paneId: "%1" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ active: true, name: "new-name", paneId: "%1" });
  });

  it("identifies the current session by pane, not by its (mutable) name", () => {
    const tabs = buildTabs(openAll, rows, { serverId: "s1", target: "default", session: "renamed", paneId: "%2" }, idle);
    expect(tabs).toHaveLength(3);
    expect(tabs.filter((t) => t.active).map((t) => t.paneId)).toEqual(["%2"]);
    expect(tabs.find((t) => t.active)?.name).toBe("renamed");
  });
});

describe("nextFocusAfterClose", () => {
  const tabs = buildTabs(openAll, rows, current, idle); // alpha(%0), beta(%1,active), gamma(%2)
  it("returns the right neighbor", () => {
    expect(nextFocusAfterClose(tabs, tabs[0].key)?.name).toBe("beta");
  });
  it("falls back to the left neighbor when closing the last tab", () => {
    expect(nextFocusAfterClose(tabs, tabs[2].key)?.name).toBe("beta");
  });
  it("returns null when closing the only visible tab", () => {
    const one = buildTabs([open("%0")], rows, { serverId: "s1", target: "default", session: "alpha", paneId: "%0" }, idle);
    expect(nextFocusAfterClose(one, one[0].key)).toBeNull();
  });
  it("returns null for an unknown key", () => {
    expect(nextFocusAfterClose(tabs, "nope")).toBeNull();
  });
});

describe("MobileSessionTabs", () => {
  const tabs = buildTabs(openAll, rows, current, idle);

  it("switches on tapping an inactive tab", async () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={() => {}} />);
    await userEvent.click(screen.getByText("alpha"));
    expect(onSwitch).toHaveBeenCalledTimes(1);
    expect(onSwitch.mock.calls[0][0]).toMatchObject({ name: "alpha", paneId: "%0" });
  });

  it("renders the active tab as a plain label (no rename editor, no switch button)", () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={() => {}} />);
    expect(screen.queryByRole("button", { name: "Rename session" })).not.toBeInTheDocument();
    expect(onSwitch).not.toHaveBeenCalled();
  });

  it("shows a close button on every tab, including the active one", () => {
    render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
    expect(screen.getByRole("button", { name: "Close alpha" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close beta" })).toBeInTheDocument(); // active
    expect(screen.getByRole("button", { name: "Close gamma" })).toBeInTheDocument();
  });

  it("calls onClose (not onSwitch) when the close button is tapped", async () => {
    const onSwitch = vi.fn();
    const onClose = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: "Close alpha" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClose.mock.calls[0][0]).toMatchObject({ name: "alpha", paneId: "%0" });
    expect(onSwitch).not.toHaveBeenCalled();
  });

  it("marks the active tab with aria-current", () => {
    render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
    expect(document.querySelector('[aria-current="page"]')?.textContent).toContain("beta");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run test:run -- src/components/MobileSessionTabs.test.tsx`
Expected: FAIL — `nextFocusAfterClose` is not exported / `buildTabs` arity mismatch / `onClose` prop unknown.

- [ ] **Step 3: Write minimal implementation**

In `web/src/components/MobileSessionTabs.tsx`:

Update imports (remove `SessionNameEditor`, add `OpenTab`):

```tsx
import type { SessionState } from "@/lib/contracts";
import type { SessionRow } from "@/components/SessionList";
import type { OpenTab } from "@/store/mobile-open-tabs";
import { StateDot } from "@/components/StateDot";
import { paneIdentity } from "@/lib/pane-identity";
```

Keep the `SessionTab` and `CurrentSession` interfaces and the identity-comment block as-is. Replace the `buildTabs` function with the open-set version:

```tsx
// Build the tab list from the explicit open set, resolved against the live session rows.
// Order follows the open set (insertion order) — deliberately NOT blocked-first, so a state
// change doesn't reshuffle tabs under the user's thumb; the state dot flags attention instead.
// An open-set entry with no matching live row (list still loading, or session gone) is skipped
// — no dead tabs. The current/entered session is guaranteed present: if it isn't in the rows
// yet, a synthetic active tab is prepended (first-paint fallback).
export function buildTabs(
  openTabs: OpenTab[],
  rows: SessionRow[],
  current: CurrentSession,
  stateOf: (row: SessionRow) => SessionState,
): SessionTab[] {
  const currentId = paneIdentity(current.serverId, current.target, current.paneId);
  const rowByIdent = new Map(
    rows.map((row) => [paneIdentity(row.server.id, row.session.target, row.pane.id), row] as const),
  );
  let matched = false;
  const tabs: SessionTab[] = [];
  for (const t of openTabs) {
    const key = paneIdentity(t.serverId, t.target, t.paneId);
    const row = rowByIdent.get(key);
    if (!row) continue; // open but not live (loading or gone) → no dead tab
    const active = key === currentId;
    if (active) matched = true;
    tabs.push({
      key,
      serverId: row.server.id,
      target: row.session.target,
      // The active tab follows the URL name (source of truth mid-rename); the rest show
      // their name from the cached list.
      name: active ? current.session : row.session.name,
      paneId: row.pane.id,
      state: stateOf(row),
      active,
    });
  }
  if (!matched) {
    tabs.unshift({
      key: currentId,
      serverId: current.serverId,
      target: current.target,
      name: current.session,
      paneId: current.paneId,
      state: "unknown",
      active: true,
    });
  }
  return tabs;
}

// Which tab should take focus when `closingKey` is closed: the right neighbor, else the left,
// else null (closing the only visible tab). Pure so the route can unit-test the active-close
// focus jump without a router.
export function nextFocusAfterClose(tabs: SessionTab[], closingKey: string): SessionTab | null {
  const i = tabs.findIndex((t) => t.key === closingKey);
  if (i === -1) return null;
  return tabs[i + 1] ?? tabs[i - 1] ?? null;
}
```

Replace the `MobileSessionTabs` component:

```tsx
// Mobile terminal header tab row: state dot + session name + close (×) per tab. Tap an
// inactive tab's body to switch; tap × to remove the tab from the bar (frees its socket,
// does NOT kill the tmux session). Rename lives on the home list now, not here.
export function MobileSessionTabs({
  tabs, onSwitch, onClose,
}: {
  tabs: SessionTab[];
  onSwitch(tab: SessionTab): void;
  onClose(tab: SessionTab): void;
}) {
  return (
    <nav aria-label="Sessions" className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
      {tabs.map((tab) => (
        <span
          key={tab.key}
          aria-current={tab.active ? "page" : undefined}
          className={
            "flex flex-none items-center gap-1 rounded-md px-2 py-1 text-xs " +
            (tab.active ? "bg-accent font-semibold" : "text-muted-foreground")
          }
        >
          {tab.active ? (
            <span className="flex min-w-0 items-center gap-1">
              <StateDot state={tab.state} />
              <span className="max-w-[8rem] truncate">{tab.name}</span>
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onSwitch(tab)}
              className="flex min-w-0 items-center gap-1 hover:opacity-80"
            >
              <StateDot state={tab.state} />
              <span className="max-w-[8rem] truncate">{tab.name}</span>
            </button>
          )}
          <button
            type="button"
            aria-label={`Close ${tab.name}`}
            onClick={() => onClose(tab)}
            className="ml-0.5 flex-none rounded px-1 leading-none text-muted-foreground hover:text-foreground"
          >
            ×
          </button>
        </span>
      ))}
    </nav>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm run test:run -- src/components/MobileSessionTabs.test.tsx`
Expected: PASS (all buildTabs, nextFocusAfterClose, and MobileSessionTabs tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/MobileSessionTabs.tsx web/src/components/MobileSessionTabs.test.tsx
git commit -m "feat(web): source mobile tabs from open set + per-tab close button"
```

---

### Task 4: Wire the open set into the terminal route

**Files:**
- Modify: `web/src/routes/terminal.tsx`

**Interfaces:**
- Consumes: `useMobileOpenTabs` (Task 1); `pool.close` (Task 2); `buildTabs`, `nextFocusAfterClose`, `SessionTab` (Task 3); existing `useMobilePanePool`, `MOBILE_POOL_CAP`, `paneIdentity`, `useNavigate`.
- Produces: no new exports (integration only).

- [ ] **Step 1: Update imports and store hooks**

In `web/src/routes/terminal.tsx`, change the `MobileSessionTabs` import to also pull the helper and type, and add the store import:

```tsx
import { MobileSessionTabs, buildTabs, nextFocusAfterClose, type SessionTab } from "@/components/MobileSessionTabs";
```

Add alongside the other store imports (e.g. after the `usePrefs` import):

```tsx
import { useMobileOpenTabs } from "@/store/mobile-open-tabs";
```

Inside `MobileTerminalRoute`, after `const pool = useMobilePanePool();`, add:

```tsx
  const openTabs = useMobileOpenTabs((s) => s.open);
  const addOpenTab = useMobileOpenTabs((s) => s.add);
  const removeOpenTab = useMobileOpenTabs((s) => s.remove);
```

- [ ] **Step 2: Add the entered pane to the open set on mount**

Replace the mount `useLayoutEffect` body so entry both opens the tab and focuses it:

```tsx
  React.useLayoutEffect(() => {
    addOpenTab({ serverId, target, paneId }); // entering = open/reopen (idempotent)
    pool.openAndFocus({ serverId, target, paneId });
    // Mount-only: the entered pane is fixed for this route mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
```

- [ ] **Step 3: Warm from the open set (not all rows)**

Replace the eager-warm `useEffect` (the `warmedRef` block) with:

```tsx
  // Eager-warm the open set into the pool (up to the cap) — open-set entries carry the pane
  // parts directly, so warming does NOT wait for the live session list. Focused pane counts first.
  const warmedRef = React.useRef(false);
  React.useEffect(() => {
    if (warmedRef.current || openTabs.length === 0) return;
    warmedRef.current = true;
    const focusedIdent = paneIdentity(serverId, target, paneId);
    let warmed = 1; // the seeded/focused pane counts toward the cap
    for (const t of openTabs) {
      if (warmed >= MOBILE_POOL_CAP) break;
      const tid = paneIdentity(t.serverId, t.target, t.paneId);
      if (tid === focusedIdent) continue;
      pool.open({ serverId: t.serverId, target: t.target, paneId: t.paneId });
      warmed++;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openTabs.length]);
```

- [ ] **Step 4: Pass the open set to `buildTabs` and add the close handler**

Update the `buildTabs` call to pass `openTabs` first:

```tsx
  const tabs = buildTabs(openTabs, rows, { serverId: focused.serverId, target: focused.target, session: focusedName, paneId: focused.paneId }, stateOf);
```

Add the close handler just before the `return (` (after the `tabs` line / viewport hook):

```tsx
  // Close = remove from the open set + drop the pane from the pool (frees the socket). The
  // tmux session keeps running. Closing the active tab focuses a neighbor first; closing the
  // last open tab returns to the session list.
  const handleClose = (tab: SessionTab) => {
    const closingId = paneIdentity(tab.serverId, tab.target, tab.paneId);
    if (tab.active) {
      const neighbor = nextFocusAfterClose(tabs, closingId);
      if (neighbor) {
        pool.openAndFocus({ serverId: neighbor.serverId, target: neighbor.target, paneId: neighbor.paneId });
        removeOpenTab(closingId);
        pool.close(closingId);
      } else {
        removeOpenTab(closingId);
        pool.close(closingId);
        navigate({ to: "/" }); // closed the last tab → back to the session list
      }
    } else {
      removeOpenTab(closingId);
      pool.close(closingId);
    }
  };
```

Update the `<MobileSessionTabs …>` render — replace `onRenamed` with `onClose`:

```tsx
        <MobileSessionTabs
          tabs={tabs}
          onSwitch={(tab) => pool.openAndFocus({ serverId: tab.serverId, target: tab.target, paneId: tab.paneId })}
          onClose={handleClose}
        />
```

- [ ] **Step 5: Typecheck, run the full suite, and build**

Run: `npm run typecheck`
Expected: PASS (no errors — the Task 3 boundary is now resolved).

Run: `npm run test:run`
Expected: PASS (entire suite green).

Run: `npm run build`
Expected: PASS (`tsc --noEmit && vite build` completes).

- [ ] **Step 6: Manual smoke (mobile viewport)**

In `npm run dev`, open the app in a mobile-width viewport (or device emulation) and confirm:
- Entering a session from the home list shows it as the active tab with a `×`.
- Opening a second session from home adds a second tab; switching between them is instant.
- Tapping `×` on an inactive tab removes it from the bar; the session still appears on the home list (not killed) and re-opens from there.
- Tapping `×` on the active tab hides it and focuses a neighbor.
- Tapping `×` on the last remaining tab returns to the home list.
- Reload the page: the open set persists (same tabs return).

- [ ] **Step 7: Commit**

```bash
git add web/src/routes/terminal.tsx
git commit -m "feat(web): wire mobile open set + close-tab into terminal route"
```

---

## Self-Review

**Spec coverage:**
- §3/§4 persisted open set → Task 1.
- §7 pool `close` action → Task 2.
- §5 `buildTabs` from open set + `×` on every tab + drop inline editor + `onClose` → Task 3.
- §5 `nextFocusAfterClose` neighbor helper → Task 3.
- §6 entry adds to open set + focus; warm from open set; close handler (non-active / active-neighbor / last-tab navigate); switch unchanged → Task 4.
- §8 edge cases: reopen-from-home (Task 4 mount add), rename resolves from rows (Task 3 buildTabs), absent-row skip (Task 3 test), idempotent add (Task 1 test), first-run empty set (no migration — inherent) → covered.
- §9 testing: store, pool.close, buildTabs, component render, neighbor selection → Tasks 1–3 tests; route integration verified via typecheck+build+manual smoke → Task 4.
- Rename stays on the home list (`SessionList` unchanged) — no task needed; explicitly not modified.

**Placeholder scan:** none — every code step shows complete content.

**Type consistency:** `OpenTab` shape identical across store/tests/buildTabs; `buildTabs(openTabs, rows, current, stateOf)` arity matches its Task 4 caller and all Task 3 tests; `nextFocusAfterClose(tabs, closingKey)` matches its route call; `close(id)`/`{ type: "close"; id }` consistent across pool reducer, hook, and tests; `onClose(tab: SessionTab)` consistent between component, tests, and route. `SessionTab.key` is a `paneIdentity`, which is exactly what `nextFocusAfterClose` matches against and what `handleClose` computes as `closingId`.
