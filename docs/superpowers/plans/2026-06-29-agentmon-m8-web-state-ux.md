# M8 — Web Supervision UX Implementation Plan

> **For agentic workers:** This plan is executed via the **Workflow tool (ultracode)** per the owner directive — parallel implement + pipelined adversarial verify, risk-tiered (see "Execution" at the end). The same per-task TDD discipline applies. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Turn the M5 React SPA into a blocked-first attention queue — state dots on every surface, blocked-first sort, live SSE updates into a dedicated store, and per-principal seen-on-focus.

**Architecture:** SSE (`/api/v1/events`) is the single source of live truth, fed into a pure zustand store keyed by `(server,target,session)`. REST (`/servers`, `/sessions`) is first-paint + structure only; the live store wins so a refetch can't clobber a delta. Server dots roll up client-side from live session states. Seen is an optimistic local mask (`done→idle`) plus a `POST /seen`, with the actively-viewed session continuously suppressed. Components stay pure (take a `stateOf` prop / read the store), unit-tested with fakes — the M5 discipline.

**Tech Stack:** TypeScript, React 18, TanStack Query + Router, zustand, Tailwind/shadcn, Vitest + Testing Library. EventSource for SSE.

**Spec:** `docs/superpowers/specs/2026-06-29-agentmon-m8-web-state-ux-design.md`.

## Global Constraints

- Frontend-only. No Go/hub changes. All work under `web/`.
- `SessionState = "blocked" | "done" | "working" | "idle" | "unknown"`. Priority `blocked(5) > done(4) > working(3) > idle(2) > unknown(1)`; empty/unrecognized → `unknown`.
- Session join key = the triple `(server, target, session-name)`; the `server` component is always `ServerSummary.id` (= SSE `server` field = seen `serverId`). Use a control-char delimiter (`\u001f` unit separator).
- SSE wire: `event: snapshot` (array of `{server,target,session,state}`), then `event: state` (one frame), `: ping` heartbeats. Seen-projected at connect-time. EventSource self-reconnects; the hub sends no `id:`, so each reconnect replays the snapshot (= resync).
- `POST /api/v1/seen` body `{serverId,target,sessionName}` (+ `X-CSRF-Token`; `204`). `target` may be `""`.
- Only `done` is maskable. `blocked`/`working`/`idle`/`unknown` are never masked.
- Run web commands from `web/`. Per-task verify runs that task's own test file; the full suite (`npm run test:run`) is the phase gate. Keep `tsc --noEmit` clean.
- All new/changed code is TDD (test first, watch it fail, implement, watch it pass).

## File Structure

**Create:**
- `web/src/lib/state.ts` — pure state logic: `STATE_PRIORITY`, `rollUp`, `STATE_META`, `stateKey`, `present`, `effectiveSessionState`, `sortBlockedFirst`, `StateSnapshot`.
- `web/src/lib/state.test.ts`
- `web/src/lib/sse-state.ts` — `StateStream` (EventSource transport, DI'd).
- `web/src/lib/sse-state.test.ts`
- `web/src/store/session-state.ts` — zustand live-state store.
- `web/src/store/session-state.test.ts`
- `web/src/components/StateDot.tsx` — the dot.
- `web/src/components/StateDot.test.tsx`
- `web/src/components/Sidebar.test.tsx` — Sidebar has no test today.
- `web/src/components/AuthLayout.tsx` — mounts the SSE stream around the auth Outlet.
- `web/src/hooks/useStateStream.ts` + `web/src/hooks/useStateStream.test.tsx`
- `web/src/hooks/useFocusedSeen.ts` + `web/src/hooks/useFocusedSeen.test.tsx`

**Modify:**
- `web/src/lib/contracts.ts` — add `SessionState`, `Session.state?`, `ServerSummary.state?`, `StateEventFrame`, `SeenRequest`.
- `web/src/lib/api-client.ts` (+ `.test.ts`) — add `postSeen`.
- `web/src/components/SessionList.tsx` (+ `.test.tsx`) — `stateOf` prop, dots, blocked-first sort.
- `web/src/components/Sidebar.tsx` — `stateOf` prop, session dots, server rollup dot, blocked-first sort of sessions + servers.
- `web/src/components/GridView.tsx` (+ `.test.tsx`) — dot in tile header (reads the store).
- `web/src/routes/index.tsx` (`ShellRoute`) — build `stateOf` from the store; pass to `SessionList` + `DesktopShell`.
- `web/src/components/DesktopShell.tsx` — `stateOf` prop → `Sidebar`; wire `useFocusedSeen` from `panes.focusedId`.
- `web/src/routes/terminal.tsx` (+ `.test.tsx`) — `useFocusedSeen` on the mobile terminal.
- `web/src/router.tsx` — auth route `component` → `AuthLayout`.
- `web/src/store/auth.ts` (+ `.test.ts`) — reset the session-state store on sign-out.

## Parallelization map (for the workflow)

- **Phase A (serial):** T1 (contracts + `lib/state`) — everything imports it.
- **Phase B (parallel, disjoint files):** T2 `sse-state`, T3 `session-state` store, T4 `StateDot`, T5 `api-client.postSeen`.
- **Phase C (parallel):** T6 `SessionList`, T8 `GridView` (neither imports the other). Then **T7 `Sidebar`** (imports `SessionList`'s stable exports — runs after T6 to avoid a read-during-write race on `SessionList.tsx`).
- **Phase D (integration):** T9 `useStateStream`+`AuthLayout`+router, T10 `useFocusedSeen`, T13 `auth` reset (parallel; disjoint). Then T11 `ShellRoute`+`DesktopShell` (needs T6/T7/T10) and T12 `terminal` (needs T10) in parallel.
- No worktree isolation needed: tasks are phased so no two concurrent tasks mutate or import-under-write the same file. Implementation agents write files + run their own test file (not the full suite, not git commit); the main loop runs the full suite and commits per phase.

---

### Task 1: Contracts + pure state logic (Phase A)

**Files:**
- Modify: `web/src/lib/contracts.ts`
- Create: `web/src/lib/state.ts`, `web/src/lib/state.test.ts`

**Interfaces:**
- Produces: `SessionState`; `StateEventFrame {server,target,session,state}`; `SeenRequest {serverId,target,sessionName}`; `STATE_PRIORITY`, `rollUp(...states): SessionState`, `STATE_META: Record<SessionState,{label,dotClass}>`, `stateKey(server,target,session): string`, `present(state,{seen,focused}): SessionState`, `StateSnapshot {live:Map<string,SessionState>; seen:Set<string>; focusedKey:string|null}`, `effectiveSessionState(snap,server,target,session,fallback?): SessionState`, `sortBlockedFirst<T>(items,stateOf): T[]`.

- [ ] **Step 1: Write the failing test** — `web/src/lib/state.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import {
  rollUp, STATE_PRIORITY, STATE_META, stateKey, present,
  effectiveSessionState, sortBlockedFirst, type StateSnapshot,
} from "@/lib/state";
import type { SessionState } from "@/lib/contracts";

describe("rollUp", () => {
  it("picks the highest-priority state", () => {
    expect(rollUp("idle", "blocked", "working")).toBe("blocked");
    expect(rollUp("idle", "done")).toBe("done");
    expect(rollUp("idle", "working")).toBe("working");
  });
  it("returns unknown for empty or unrecognized input", () => {
    expect(rollUp()).toBe("unknown");
    expect(rollUp("nope" as SessionState)).toBe("unknown");
  });
});

describe("STATE_META", () => {
  it("has a label + dotClass for every state", () => {
    (["blocked","done","working","idle","unknown"] as SessionState[]).forEach((s) => {
      expect(STATE_META[s].label).toBe(s);
      expect(STATE_META[s].dotClass).toMatch(/^bg-/);
    });
  });
});

describe("stateKey", () => {
  it("is collision-safe across triples (control-char delimiter)", () => {
    // a session name containing a space must not collide with a different triple
    expect(stateKey("s", "a", "b c")).not.toBe(stateKey("s", "a b", "c"));
    expect(stateKey("s", "default", "x")).toBe(stateKey("s", "default", "x"));
  });
});

describe("present (mask)", () => {
  it("masks done→idle only when seen or focused", () => {
    expect(present("done", { seen: false, focused: false })).toBe("done");
    expect(present("done", { seen: true, focused: false })).toBe("idle");
    expect(present("done", { seen: false, focused: true })).toBe("idle");
  });
  it("never masks blocked/working/idle/unknown", () => {
    expect(present("blocked", { seen: true, focused: true })).toBe("blocked");
    expect(present("working", { seen: true, focused: true })).toBe("working");
    expect(present("idle", { seen: true, focused: true })).toBe("idle");
    expect(present("unknown", { seen: true, focused: true })).toBe("unknown");
  });
});

describe("effectiveSessionState", () => {
  const snap = (over: Partial<StateSnapshot> = {}): StateSnapshot => ({
    live: new Map(), seen: new Set(), focusedKey: null, ...over,
  });
  it("uses live state, then the REST fallback, then unknown", () => {
    const k = stateKey("s", "t", "x");
    expect(effectiveSessionState(snap({ live: new Map([[k, "blocked"]]) }), "s", "t", "x")).toBe("blocked");
    expect(effectiveSessionState(snap(), "s", "t", "x", "working")).toBe("working");
    expect(effectiveSessionState(snap(), "s", "t", "x")).toBe("unknown");
  });
  it("applies the seen mask and the focused mask", () => {
    const k = stateKey("s", "t", "x");
    expect(effectiveSessionState(snap({ live: new Map([[k, "done"]]), seen: new Set([k]) }), "s", "t", "x")).toBe("idle");
    expect(effectiveSessionState(snap({ live: new Map([[k, "done"]]), focusedKey: k }), "s", "t", "x")).toBe("idle");
  });
});

describe("sortBlockedFirst", () => {
  it("orders by priority desc and is stable within a group", () => {
    const items = [
      { id: "a", st: "idle" }, { id: "b", st: "blocked" },
      { id: "c", st: "done" }, { id: "d", st: "blocked" },
    ] as const;
    const out = sortBlockedFirst([...items], (i) => i.st as SessionState).map((i) => i.id);
    expect(out).toEqual(["b", "d", "c", "a"]); // b,d (blocked, input order) → c (done) → a (idle)
  });
});
```

- [ ] **Step 2: Run it, expect FAIL** — `cd web && npx vitest run src/lib/state.test.ts` → fails ("Cannot find module @/lib/state").

- [ ] **Step 3: Implement** — add to `web/src/lib/contracts.ts` (top, after the header comment):

```ts
export type SessionState = "blocked" | "done" | "working" | "idle" | "unknown";
```
Add `state?: SessionState;` to `interface Session` and to `interface ServerSummary`. Append:
```ts
// SSE snapshot-entry + delta shape (mirrors hubd api.stateEvent).
export interface StateEventFrame { server: string; target: string; session: string; state: SessionState; }
// POST /api/v1/seen body (mirrors hubd api.seenRequest).
export interface SeenRequest { serverId: string; target: string; sessionName: string; }
```
Create `web/src/lib/state.ts`:
```ts
import type { SessionState } from "@/lib/contracts";

export const STATE_PRIORITY: Record<SessionState, number> = {
  blocked: 5, done: 4, working: 3, idle: 2, unknown: 1,
};

// Roll up many states to the highest-priority one. Empty/unrecognized → "unknown".
export function rollUp(...states: SessionState[]): SessionState {
  let best: SessionState = "unknown";
  let bestP = STATE_PRIORITY.unknown;
  for (const s of states) {
    const p = STATE_PRIORITY[s];
    if (p === undefined) continue;
    if (p > bestP) { best = s; bestP = p; }
  }
  return best;
}

export interface StateMeta { label: string; dotClass: string; }
export const STATE_META: Record<SessionState, StateMeta> = {
  blocked: { label: "blocked", dotClass: "bg-red-500" },
  done:    { label: "done",    dotClass: "bg-blue-500" },
  working: { label: "working", dotClass: "bg-amber-500" },
  idle:    { label: "idle",    dotClass: "bg-green-500" },
  unknown: { label: "unknown", dotClass: "bg-zinc-400" },
};

const SEP = "\u001f"; // unit separator — never appears in a server id / target / session name
export function stateKey(server: string, target: string, session: string): string {
  return `${server}${SEP}${target}${SEP}${session}`;
}

// Only `done` is maskable: a focused or already-seen session reads idle.
export function present(state: SessionState, opts: { seen: boolean; focused: boolean }): SessionState {
  if (state === "done" && (opts.seen || opts.focused)) return "idle";
  return state;
}

export interface StateSnapshot {
  live: Map<string, SessionState>;
  seen: Set<string>;
  focusedKey: string | null;
}

export function effectiveSessionState(
  snap: StateSnapshot, server: string, target: string, session: string, fallback?: SessionState,
): SessionState {
  const key = stateKey(server, target, session);
  const raw = snap.live.get(key) ?? fallback ?? "unknown";
  return present(raw, { seen: snap.seen.has(key), focused: snap.focusedKey === key });
}

// Stable sort by state priority (blocked first). Ties keep input order.
export function sortBlockedFirst<T>(items: T[], stateOf: (t: T) => SessionState): T[] {
  return items
    .map((item, i) => ({ item, i }))
    .sort((a, b) => (STATE_PRIORITY[stateOf(b.item)] - STATE_PRIORITY[stateOf(a.item)]) || (a.i - b.i))
    .map((x) => x.item);
}
```

- [ ] **Step 4: Run it, expect PASS** — `npx vitest run src/lib/state.test.ts`.
- [ ] **Step 5 (phase gate / main loop):** full suite + `tsc --noEmit`; commit `feat(m8): SessionState contracts + pure state logic (rollup, dots, mask, sort)`.

---

### Task 2: SSE transport — `StateStream` (Phase B)

**Files:** Create `web/src/lib/sse-state.ts`, `web/src/lib/sse-state.test.ts`.

**Interfaces:**
- Consumes: `StateEventFrame` (T1).
- Produces: `StateStream` class with `open()` / `dispose()`; ctor `(handlers, deps?)`; `StateStreamHandlers {onSnapshot(frames),onDelta(frame),onOpen?,onError?}`; `StateStreamDeps {EventSourceCtor?, url?}`.

- [ ] **Step 1: Failing test** — `web/src/lib/sse-state.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { StateStream } from "@/lib/sse-state";

class FakeES {
  static instances: FakeES[] = [];
  url: string; opts: unknown;
  listeners: Record<string, ((ev: { data: string }) => void)[]> = {};
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(url: string, opts?: unknown) { this.url = url; this.opts = opts; FakeES.instances.push(this); }
  addEventListener(type: string, fn: (ev: { data: string }) => void) { (this.listeners[type] ??= []).push(fn); }
  close() { this.closed = true; }
  emit(type: string, data: string) { (this.listeners[type] ?? []).forEach((fn) => fn({ data })); }
}

function mk() {
  FakeES.instances = [];
  const onSnapshot = vi.fn(); const onDelta = vi.fn();
  const s = new StateStream({ onSnapshot, onDelta }, { EventSourceCtor: FakeES as unknown as typeof EventSource });
  s.open();
  return { s, es: FakeES.instances[0], onSnapshot, onDelta };
}

describe("StateStream", () => {
  it("connects to /api/v1/events", () => {
    const { es } = mk();
    expect(es.url).toBe("/api/v1/events");
  });
  it("parses a snapshot array → onSnapshot", () => {
    const { es, onSnapshot } = mk();
    es.emit("snapshot", JSON.stringify([{ server: "s", target: "t", session: "x", state: "blocked" }]));
    expect(onSnapshot).toHaveBeenCalledWith([{ server: "s", target: "t", session: "x", state: "blocked" }]);
  });
  it("parses a state delta → onDelta", () => {
    const { es, onDelta } = mk();
    es.emit("state", JSON.stringify({ server: "s", target: "t", session: "x", state: "done" }));
    expect(onDelta).toHaveBeenCalledWith({ server: "s", target: "t", session: "x", state: "done" });
  });
  it("re-fires onSnapshot on reconnect (server replays the snapshot)", () => {
    const { es, onSnapshot } = mk();
    es.emit("snapshot", JSON.stringify([]));
    es.emit("snapshot", JSON.stringify([{ server: "s", target: "t", session: "x", state: "idle" }]));
    expect(onSnapshot).toHaveBeenCalledTimes(2);
  });
  it("drops malformed JSON without throwing", () => {
    const { es, onSnapshot, onDelta } = mk();
    es.emit("snapshot", "{not json");
    es.emit("state", "{not json");
    expect(onSnapshot).not.toHaveBeenCalled();
    expect(onDelta).not.toHaveBeenCalled();
  });
  it("dispose() closes the EventSource and blocks re-open", () => {
    const { s, es } = mk();
    s.dispose();
    expect(es.closed).toBe(true);
    s.open();
    expect(FakeES.instances.length).toBe(1);
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — `web/src/lib/sse-state.ts`:

```ts
import type { StateEventFrame } from "@/lib/contracts";

const EVENTS_URL = "/api/v1/events";

export interface StateStreamHandlers {
  onSnapshot(frames: StateEventFrame[]): void;
  onDelta(frame: StateEventFrame): void;
  onOpen?(): void;
  onError?(): void; // EventSource self-reconnects; this is a connection indicator only
}
export interface StateStreamDeps { EventSourceCtor?: typeof EventSource; url?: string; }

function parseJSON<T>(data: unknown): T | null {
  try { return JSON.parse(String(data)) as T; } catch { return null; }
}

// Thin EventSource transport. EventSource reconnects natively; the hub sends no
// `id:`, so each reconnect replays `event: snapshot` (= resync). DI'd for tests.
export class StateStream {
  private es: EventSource | null = null;
  private disposed = false;
  private readonly ES: typeof EventSource | undefined;
  private readonly url: string;

  constructor(private readonly handlers: StateStreamHandlers, deps: StateStreamDeps = {}) {
    this.ES = deps.EventSourceCtor ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
    this.url = deps.url ?? EVENTS_URL;
  }

  open(): void {
    if (this.disposed || this.es || !this.ES) return;
    const es = new this.ES(this.url, { withCredentials: true });
    this.es = es;
    es.addEventListener("snapshot", (ev: MessageEvent) => {
      const frames = parseJSON<StateEventFrame[]>(ev.data);
      if (Array.isArray(frames)) this.handlers.onSnapshot(frames);
    });
    es.addEventListener("state", (ev: MessageEvent) => {
      const frame = parseJSON<StateEventFrame>(ev.data);
      if (frame && typeof frame === "object") this.handlers.onDelta(frame);
    });
    es.onopen = () => this.handlers.onOpen?.();
    es.onerror = () => this.handlers.onError?.();
  }

  dispose(): void {
    this.disposed = true;
    if (this.es) { this.es.close(); this.es = null; }
  }
}
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** (phase gate) commit with T3/T4/T5.

---

### Task 3: Live-state store — `session-state` (Phase B) — HARD VERIFY

**Files:** Create `web/src/store/session-state.ts`, `web/src/store/session-state.test.ts`.

**Interfaces:**
- Consumes: `SessionState`, `StateEventFrame` (T1); `stateKey`, `StateSnapshot` (T1).
- Produces: `useSessionState` zustand store with state `{live,seen,focusedKey,connected}` and actions `applySnapshot(frames)`, `applyDelta(frame)`, `markSeen(key)`, `setFocusedKey(key|null)`, `setConnected(b)`, `reset()`.

- [ ] **Step 1: Failing test** — `web/src/store/session-state.test.ts`:

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { useSessionState } from "@/store/session-state";
import { stateKey, effectiveSessionState } from "@/lib/state";

const k = (s: string, t: string, n: string) => stateKey(s, t, n);

describe("session-state store", () => {
  beforeEach(() => useSessionState.getState().reset());

  it("applySnapshot replaces live, clears seen, sets connected", () => {
    useSessionState.getState().markSeen(k("s", "t", "old"));
    useSessionState.getState().applySnapshot([
      { server: "s", target: "t", session: "a", state: "blocked" },
      { server: "s", target: "t", session: "b", state: "done" },
    ]);
    const st = useSessionState.getState();
    expect(st.live.get(k("s", "t", "a"))).toBe("blocked");
    expect(st.live.get(k("s", "t", "b"))).toBe("done");
    expect(st.seen.size).toBe(0);          // cleared
    expect(st.connected).toBe(true);
  });

  it("applyDelta patches one key and clears that key's seen (re-alert un-suppresses)", () => {
    const key = k("s", "t", "a");
    useSessionState.getState().applySnapshot([{ server: "s", target: "t", session: "a", state: "done" }]);
    useSessionState.getState().markSeen(key);
    expect(effectiveSessionState(useSessionState.getState(), "s", "t", "a")).toBe("idle"); // masked
    useSessionState.getState().applyDelta({ server: "s", target: "t", session: "a", state: "done" }); // re-alert
    expect(useSessionState.getState().seen.has(key)).toBe(false); // un-suppressed
    expect(effectiveSessionState(useSessionState.getState(), "s", "t", "a")).toBe("done");
  });

  it("focusedKey continuously suppresses done even across a delta", () => {
    const key = k("s", "t", "a");
    useSessionState.getState().setFocusedKey(key);
    useSessionState.getState().applyDelta({ server: "s", target: "t", session: "a", state: "done" });
    expect(effectiveSessionState(useSessionState.getState(), "s", "t", "a")).toBe("idle"); // focused → masked
  });

  it("immutable updates: live identity changes on delta (so subscribers re-render)", () => {
    useSessionState.getState().applySnapshot([{ server: "s", target: "t", session: "a", state: "idle" }]);
    const before = useSessionState.getState().live;
    useSessionState.getState().applyDelta({ server: "s", target: "t", session: "a", state: "working" });
    expect(useSessionState.getState().live).not.toBe(before);
  });

  it("reset clears everything", () => {
    useSessionState.getState().applySnapshot([{ server: "s", target: "t", session: "a", state: "blocked" }]);
    useSessionState.getState().setFocusedKey(k("s", "t", "a"));
    useSessionState.getState().reset();
    const st = useSessionState.getState();
    expect(st.live.size).toBe(0); expect(st.seen.size).toBe(0);
    expect(st.focusedKey).toBeNull(); expect(st.connected).toBe(false);
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — `web/src/store/session-state.ts`:

```ts
import { create } from "zustand";
import type { SessionState, StateEventFrame } from "@/lib/contracts";
import { stateKey, type StateSnapshot } from "@/lib/state";

interface SessionStateStore extends StateSnapshot {
  connected: boolean;
  applySnapshot(frames: StateEventFrame[]): void;
  applyDelta(frame: StateEventFrame): void;
  markSeen(key: string): void;
  setFocusedKey(key: string | null): void;
  setConnected(b: boolean): void;
  reset(): void;
}

export const useSessionState = create<SessionStateStore>((set) => ({
  live: new Map<string, SessionState>(),
  seen: new Set<string>(),
  focusedKey: null,
  connected: false,
  applySnapshot(frames) {
    const live = new Map<string, SessionState>();
    for (const f of frames) live.set(stateKey(f.server, f.target, f.session), f.state);
    set({ live, seen: new Set(), connected: true }); // fresh snapshot is already server-seen-projected
  },
  applyDelta(frame) {
    set((s) => {
      const key = stateKey(frame.server, frame.target, frame.session);
      const live = new Map(s.live); live.set(key, frame.state);
      let seen = s.seen;
      if (seen.has(key)) { seen = new Set(seen); seen.delete(key); } // a delta = changed/re-alert → un-suppress
      return { live, seen };
    });
  },
  markSeen(key) {
    set((s) => {
      if (s.seen.has(key)) return {} as Partial<SessionStateStore>;
      const seen = new Set(s.seen); seen.add(key);
      return { seen };
    });
  },
  setFocusedKey(focusedKey) { set({ focusedKey }); },
  setConnected(connected) { set({ connected }); },
  reset() { set({ live: new Map(), seen: new Set(), focusedKey: null, connected: false }); },
}));
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** (phase gate) commit with T2/T4/T5.

---

### Task 4: `StateDot` component (Phase B)

**Files:** Create `web/src/components/StateDot.tsx`, `web/src/components/StateDot.test.tsx`.

**Interfaces:** Consumes `STATE_META` (T1), `SessionState` (T1), `cn` (`@/lib/utils`). Produces `StateDot({state, className?})`.

- [ ] **Step 1: Failing test** — `web/src/components/StateDot.test.tsx`:

```ts
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StateDot } from "@/components/StateDot";
import type { SessionState } from "@/lib/contracts";

describe("StateDot", () => {
  it("labels and colors each state", () => {
    const cases: [SessionState, string][] = [
      ["blocked", "bg-red-500"], ["done", "bg-blue-500"], ["working", "bg-amber-500"],
      ["idle", "bg-green-500"], ["unknown", "bg-zinc-400"],
    ];
    for (const [state, cls] of cases) {
      const { unmount } = render(<StateDot state={state} />);
      const dot = screen.getByRole("img", { name: state });
      expect(dot.className).toContain(cls);
      unmount();
    }
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — `web/src/components/StateDot.tsx`:

```tsx
import { STATE_META } from "@/lib/state";
import type { SessionState } from "@/lib/contracts";
import { cn } from "@/lib/utils";

// A small themeable status dot. Not raw emoji (consistent render, themeable,
// assertable by aria-label). Color per §9.1.
export function StateDot({ state, className }: { state: SessionState; className?: string }) {
  const meta = STATE_META[state];
  return (
    <span
      role="img"
      aria-label={meta.label}
      title={meta.label}
      className={cn("inline-block size-2.5 flex-none rounded-full", meta.dotClass, className)}
    />
  );
}
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** (phase gate) commit with T2/T3/T5.

---

### Task 5: `api-client.postSeen` (Phase B)

**Files:** Modify `web/src/lib/api-client.ts`, `web/src/lib/api-client.test.ts`.

**Interfaces:** Consumes `SeenRequest` (T1) + the existing `request`. Produces `postSeen(req: SeenRequest): Promise<void>`.

- [ ] **Step 1: Failing test** — append to `web/src/lib/api-client.test.ts`:

```ts
it("postSeen POSTs the body with X-CSRF-Token", async () => {
  const f = mockFetch(204, undefined);
  vi.stubGlobal("fetch", f);
  setCsrfToken("tok");
  const { postSeen } = await import("@/lib/api-client");
  await postSeen({ serverId: "s", target: "default", sessionName: "x" });
  const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe("/api/v1/seen");
  expect(init.method).toBe("POST");
  expect(init.body).toBe(JSON.stringify({ serverId: "s", target: "default", sessionName: "x" }));
  expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
});
```
(Add `postSeen` to the top `import { ... } from "@/lib/api-client"` line instead of the dynamic import if preferred; either works.)

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — in `web/src/lib/api-client.ts`, extend the contracts import with `SeenRequest` and append:
```ts
export const postSeen = (req: SeenRequest) => request<void>("POST", "/seen", req);
```

- [ ] **Step 4: Run `npx vitest run src/lib/api-client.test.ts`, expect PASS.**  — [ ] **Step 5:** (phase gate) commit with T2/T3/T4: `feat(m8): SSE StateStream, live-state store, StateDot, postSeen`.

---

### Task 6: `SessionList` — dots + blocked-first (Phase C)

**Files:** Modify `web/src/components/SessionList.tsx`, `web/src/components/SessionList.test.tsx`.

**Interfaces:** Consumes `sortBlockedFirst`, `SessionState` (T1), `StateDot` (T4). Produces an updated `SessionList` whose props gain `stateOf(row: SessionRow): SessionState`.

- [ ] **Step 1: Failing test** — extend `web/src/components/SessionList.test.tsx`. Update the existing render test to pass `stateOf={() => "idle"}`, and add:

```ts
describe("SessionList state", () => {
  it("sorts blocked first and renders a dot per row", () => {
    const two = {
      s1: [
        { name: "calm", server: "s1", target: "default", cwd: "/a", command: "claude",
          windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] },
        { name: "needshelp", server: "s1", target: "default", cwd: "/b", command: "claude",
          windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "c", cwd: "/b" }] }] },
      ],
    };
    const rows = flattenSessions(servers, two);
    const stateOf = (r: SessionRow) => (r.session.name === "needshelp" ? "blocked" : "idle") as const;
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    const labels = screen.getAllByText(/calm|needshelp/).map((n) => n.textContent);
    expect(labels[0]).toBe("needshelp"); // blocked floats up
    expect(screen.getAllByRole("img", { name: "blocked" })).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — in `SessionList.tsx`: import `sortBlockedFirst` from `@/lib/state`, `StateDot` from `@/components/StateDot`, `SessionState` from `@/lib/contracts`. Add `stateOf(row: SessionRow): SessionState` to the props type. Replace the `filtered` line with:
```tsx
const filtered = sortBlockedFirst(rows.filter((r) => matchesQuery(r, query)), stateOf);
```
Replace the row `<button>` inner markup with a dot + text row:
```tsx
<button
  className="flex w-full items-center gap-2 border-b border-border px-4 py-3 text-left hover:bg-accent"
  onClick={() => onOpen(row)}
>
  <StateDot state={stateOf(row)} />
  <div className="min-w-0">
    <div className="font-medium">{row.session.name}</div>
    <div className="text-xs text-muted-foreground">{row.server.name} · {row.session.cwd || "—"}</div>
  </div>
</button>
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** (phase gate) commit with T8: `feat(m8): SessionList + GridView state dots + blocked-first`.

---

### Task 7: `Sidebar` — session dots, server rollup dot, blocked-first (Phase C, after T6)

**Files:** Modify `web/src/components/Sidebar.tsx`. Create `web/src/components/Sidebar.test.tsx`.

**Interfaces:** Consumes `sortBlockedFirst`, `rollUp`, `SessionState` (T1), `StateDot` (T4), `matchesQuery`/`SessionRow` (existing). Produces updated `Sidebar` whose props gain `stateOf(row: SessionRow): SessionState`.

- [ ] **Step 1: Failing test** — `web/src/components/Sidebar.test.tsx`:

```ts
import { describe, it, expect } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { Sidebar } from "@/components/Sidebar";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { ServerSummary } from "@/lib/contracts";

const servers: ServerSummary[] = [
  { id: "s1", name: "alpha", labels: [], enabled: true },
  { id: "s2", name: "bravo", labels: [], enabled: true },
];
const byServer = {
  s1: [{ name: "calm", server: "s1", target: "default", cwd: "/a", command: "c",
    windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] }],
  s2: [{ name: "hot", server: "s2", target: "default", cwd: "/b", command: "c",
    windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "c", cwd: "/b" }] }] }],
};

describe("Sidebar state", () => {
  it("rolls up server dots and sorts the blocked server first", () => {
    const rows = flattenSessions(servers, byServer);
    const stateOf = (r: SessionRow) => (r.server.id === "s2" ? "blocked" : "idle") as const;
    render(<Sidebar rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    const headers = screen.getAllByText(/alpha|bravo/).map((n) => n.textContent);
    expect(headers[0]).toBe("bravo"); // server holding the blocked session floats up
    // bravo's rollup dot reads blocked
    expect(screen.getAllByRole("img", { name: "blocked" }).length).toBeGreaterThanOrEqual(1);
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — rewrite `Sidebar.tsx`:

```tsx
import type { SessionRow } from "@/components/SessionList";
import { Input } from "@/components/ui/input";
import { matchesQuery } from "@/components/SessionList";
import { sortBlockedFirst, rollUp } from "@/lib/state";
import { StateDot } from "@/components/StateDot";
import type { SessionState } from "@/lib/contracts";

// Desktop servers→sessions tree. Dots roll up; blocked sorts first.
export function Sidebar({
  rows, query, onQueryChange, onOpen, stateOf,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
  stateOf(row: SessionRow): SessionState;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const byServer = new Map<string, SessionRow[]>();
  for (const r of filtered) {
    const list = byServer.get(r.server.name) ?? [];
    list.push(r);
    byServer.set(r.server.name, list);
  }
  const groups = sortBlockedFirst(
    [...byServer.entries()].map(([serverName, list]) => ({
      serverName,
      list: sortBlockedFirst(list, stateOf),
      serverState: rollUp(...list.map(stateOf)),
    })),
    (g) => g.serverState,
  );

  return (
    <aside className="flex h-full w-72 flex-none flex-col border-r border-border">
      <div className="p-3">
        <Input placeholder="Search…" value={query} onChange={(e) => onQueryChange(e.target.value)}
          aria-label="Search sessions" />
      </div>
      <div className="flex-1 overflow-y-auto">
        {groups.map(({ serverName, list, serverState }) => (
          <div key={serverName}>
            <div className="flex items-center gap-2 px-3 py-1">
              <StateDot state={serverState} />
              <span className="text-xs font-semibold uppercase text-muted-foreground">{serverName}</span>
            </div>
            {list.map((row) => (
              <button
                key={`${row.session.target}:${row.session.name}:${row.pane.id}`}
                className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-accent"
                onClick={() => onOpen(row)}
              >
                <StateDot state={stateOf(row)} />
                <div className="min-w-0">
                  <div className="truncate">{row.session.name}</div>
                  <div className="truncate text-xs text-muted-foreground">{row.session.cwd || "—"}</div>
                </div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit `feat(m8): Sidebar session+server rollup dots, blocked-first sort`.

---

### Task 8: `GridView` — tile-header dot (Phase C)

**Files:** Modify `web/src/components/GridView.tsx`, `web/src/components/GridView.test.tsx`.

**Interfaces:** Consumes `useSessionState` (T3), `effectiveSessionState` (T1), `StateDot` (T4). Reads the store directly (GridView already reads `usePanes`).

- [ ] **Step 1: Failing test** — append to `web/src/components/GridView.test.tsx`:

```ts
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";

it("shows a state dot per tile from the live store", () => {
  useSessionState.getState().reset();
  useSessionState.getState().applySnapshot([{ server: "s", target: "default", session: "a", state: "blocked" }]);
  usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
  usePanes.getState().collapse();
  render(<GridView />);
  expect(screen.getByRole("img", { name: "blocked" })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — in `GridView.tsx` add imports and read the store snapshot, then render a `StateDot` in the tile header next to the title button:
```tsx
import { useSessionState } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import { StateDot } from "@/components/StateDot";
// inside GridView(), after usePanes destructuring:
const live = useSessionState((s) => s.live);
const seen = useSessionState((s) => s.seen);
const focusedKey = useSessionState((s) => s.focusedKey);
const snap = { live, seen, focusedKey };
// in the tile header, wrap the title button so the dot precedes it:
<span className="flex min-w-0 items-center gap-1.5">
  <StateDot state={effectiveSessionState(snap, p.serverId, p.target, p.session)} />
  <button className="min-w-0 truncate text-left hover:underline"
    onClick={() => (expanded ? collapse() : focus(p.id))}
    title={expanded ? "Back to grid" : "Expand"}>
    {p.serverName} · {p.session} · {p.paneId}
  </button>
</span>
```
(Replace the existing title `<button>` with this wrapped form; keep the right-side controls `<span>` unchanged.)

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit with T6.

---

### Task 9: `useStateStream` + `AuthLayout` + router (Phase D)

**Files:** Create `web/src/hooks/useStateStream.ts`, `web/src/components/AuthLayout.tsx`, `web/src/hooks/useStateStream.test.tsx`. Modify `web/src/router.tsx`.

**Interfaces:** Consumes `StateStream` (T2), `useSessionState` (T3). Produces `useStateStream(deps?)` and `AuthLayout`.

- [ ] **Step 1: Failing test** — `web/src/hooks/useStateStream.test.tsx`:

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { useStateStream } from "@/hooks/useStateStream";
import { useSessionState } from "@/store/session-state";

class FakeES {
  static instances: FakeES[] = [];
  listeners: Record<string, ((ev: { data: string }) => void)[]> = {};
  onopen: (() => void) | null = null; onerror: (() => void) | null = null; closed = false;
  constructor(public url: string, public opts?: unknown) { FakeES.instances.push(this); }
  addEventListener(t: string, fn: (ev: { data: string }) => void) { (this.listeners[t] ??= []).push(fn); }
  close() { this.closed = true; }
  emit(t: string, data: string) { (this.listeners[t] ?? []).forEach((fn) => fn({ data })); }
}
function Harness() { useStateStream({ EventSourceCtor: FakeES as unknown as typeof EventSource }); return null; }

describe("useStateStream", () => {
  beforeEach(() => { FakeES.instances = []; useSessionState.getState().reset(); });
  it("pumps snapshot frames into the store and closes on unmount", () => {
    const { unmount } = render(<Harness />);
    FakeES.instances[0].emit("snapshot", JSON.stringify([{ server: "s", target: "t", session: "a", state: "blocked" }]));
    expect(useSessionState.getState().live.size).toBe(1);
    unmount();
    expect(FakeES.instances[0].closed).toBe(true);
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — `web/src/hooks/useStateStream.ts`:
```ts
import * as React from "react";
import { StateStream, type StateStreamDeps } from "@/lib/sse-state";
import { useSessionState } from "@/store/session-state";

// Mounts one SSE stream for the authed session and pumps it into the store.
export function useStateStream(deps?: StateStreamDeps): void {
  React.useEffect(() => {
    const s = useSessionState.getState();
    const stream = new StateStream(
      {
        onSnapshot: s.applySnapshot,
        onDelta: s.applyDelta,
        onOpen: () => useSessionState.getState().setConnected(true),
        onError: () => useSessionState.getState().setConnected(false),
      },
      deps,
    );
    stream.open();
    return () => stream.dispose();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
```
`web/src/components/AuthLayout.tsx`:
```tsx
import { Outlet } from "@tanstack/react-router";
import { useStateStream } from "@/hooks/useStateStream";

// Auth layout: one live SSE stream for the whole authed session, around the Outlet.
export function AuthLayout() {
  useStateStream();
  return <Outlet />;
}
```
`web/src/router.tsx`: import `AuthLayout` and set the auth route `component: AuthLayout` (replacing `component: () => <Outlet />`). Remove the now-unused `Outlet` import only if nothing else uses it (the root route still uses it — keep the import).

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit `feat(m8): mount SSE stream in the auth layout (useStateStream)`.

---

### Task 10: `useFocusedSeen` (Phase D) — HARD VERIFY

**Files:** Create `web/src/hooks/useFocusedSeen.ts`, `web/src/hooks/useFocusedSeen.test.tsx`.

**Interfaces:** Consumes `useSessionState` (T3), `stateKey` (T1), `postSeen` (T5), `SeenRequest` (T1). Produces `useFocusedSeen(req: SeenRequest | null)`.

- [ ] **Step 1: Failing test** — `web/src/hooks/useFocusedSeen.test.tsx`:

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render } from "@testing-library/react";

// vi.hoisted: vi.mock is hoisted above plain consts, so the mock fn must be too.
const { postSeen } = vi.hoisted(() => ({ postSeen: vi.fn(async () => {}) }));
vi.mock("@/lib/api-client", () => ({ postSeen }));

import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
import type { SeenRequest } from "@/lib/contracts";

function Harness({ req }: { req: SeenRequest | null }) { useFocusedSeen(req); return null; }

describe("useFocusedSeen", () => {
  beforeEach(() => { postSeen.mockClear(); useSessionState.getState().reset(); });

  it("sets focusedKey, optimistically marks seen, and POSTs", () => {
    const req = { serverId: "s", target: "default", sessionName: "a" };
    render(<Harness req={req} />);
    const key = stateKey("s", "default", "a");
    expect(useSessionState.getState().focusedKey).toBe(key);
    expect(useSessionState.getState().seen.has(key)).toBe(true);
    expect(postSeen).toHaveBeenCalledWith(req);
  });

  it("clears focusedKey on unmount and when req is null", () => {
    const { rerender, unmount } = render(<Harness req={{ serverId: "s", target: "t", sessionName: "a" }} />);
    expect(useSessionState.getState().focusedKey).not.toBeNull();
    rerender(<Harness req={null} />);
    expect(useSessionState.getState().focusedKey).toBeNull();
    unmount();
    expect(useSessionState.getState().focusedKey).toBeNull();
  });

  it("swallows a POST failure", async () => {
    postSeen.mockRejectedValueOnce(new Error("boom"));
    expect(() => render(<Harness req={{ serverId: "s", target: "t", sessionName: "a" }} />)).not.toThrow();
  });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — `web/src/hooks/useFocusedSeen.ts`:
```ts
import * as React from "react";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
import { postSeen } from "@/lib/api-client";
import type { SeenRequest } from "@/lib/contracts";

// Marks the actively-viewed session focused (continuous done-suppression) + seen,
// and persists via POST /seen (best-effort). Passing null clears the focus.
export function useFocusedSeen(req: SeenRequest | null): void {
  const key = req ? stateKey(req.serverId, req.target, req.sessionName) : null;
  const reqRef = React.useRef(req);
  reqRef.current = req;
  React.useEffect(() => {
    const store = useSessionState.getState();
    if (!key) { store.setFocusedKey(null); return; }
    store.setFocusedKey(key);
    store.markSeen(key);
    void postSeen(reqRef.current!).catch(() => {});
    return () => { useSessionState.getState().setFocusedKey(null); };
  }, [key]);
}
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit `feat(m8): useFocusedSeen — mark seen + suppress the active view`.

---

### Task 11: `ShellRoute` + `DesktopShell` wiring (Phase D, after T6/T7/T10) — HARD VERIFY

**Files:** Modify `web/src/routes/index.tsx`, `web/src/components/DesktopShell.tsx`.

**Interfaces:** Consumes `useSessionState` (T3), `effectiveSessionState` (T1), `SessionList`/`Sidebar` `stateOf` props (T6/T7), `useFocusedSeen` (T10).

- [ ] **Step 1:** No new unit test file is strictly required (covered by component + hook tests); the gate is `tsc --noEmit` + the full suite staying green + the existing route tests. Optionally add an integration assertion later.
- [ ] **Step 2: Implement `ShellRoute`** — in `web/src/routes/index.tsx`:
  - import `useSessionState` from `@/store/session-state`, `effectiveSessionState` from `@/lib/state`, `SessionRow` from `@/components/SessionList`, `SessionState` from `@/lib/contracts`.
  - build `stateOf`:
```tsx
const live = useSessionState((s) => s.live);
const seen = useSessionState((s) => s.seen);
const focusedKey = useSessionState((s) => s.focusedKey);
const stateOf = React.useCallback(
  (row: SessionRow): SessionState =>
    effectiveSessionState({ live, seen, focusedKey }, row.server.id, row.session.target, row.session.name, row.session.state),
  [live, seen, focusedKey],
);
```
  - pass `stateOf={stateOf}` to both `<DesktopShell …>` and `<SessionList …>`.
- [ ] **Step 3: Implement `DesktopShell`** — add the `stateOf` prop (pass through to `<Sidebar … stateOf={stateOf} />`) and wire desktop focus-seen:
```tsx
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import type { SeenRequest } from "@/lib/contracts";
// inside DesktopShell, with the existing usePanes usage:
const panes = usePanes((s) => s.panes);
const focusedId = usePanes((s) => s.focusedId);
const focusedReq = React.useMemo<SeenRequest | null>(() => {
  const p = panes.find((x) => x.id === focusedId);
  return p ? { serverId: p.serverId, target: p.target, sessionName: p.session } : null;
}, [panes, focusedId]);
useFocusedSeen(focusedReq);
```
- [ ] **Step 4:** `npx vitest run` (full suite) + `npx tsc --noEmit`, expect PASS/clean.
- [ ] **Step 5:** commit `feat(m8): wire live stateOf into desktop+mobile and desktop seen-on-expand`.

---

### Task 12: Mobile terminal seen-on-open (Phase D, after T10)

**Files:** Modify `web/src/routes/terminal.tsx`, `web/src/routes/terminal.test.tsx`.

**Interfaces:** Consumes `useFocusedSeen` (T10).

- [ ] **Step 1: Failing test** — extend `web/src/routes/terminal.test.tsx`. The existing file already `vi.mock`s `@tanstack/react-router` (`useParams → {serverId:"s1",paneId:"%0"}`, `useSearch → {target:"default",session:"demo-web"}`) and `@/components/TerminalView`. Add a `postSeen` mock (via `vi.hoisted`) and a second test. At the top, alongside the existing mocks:
```ts
const { postSeen } = vi.hoisted(() => ({ postSeen: vi.fn(async () => {}) }));
vi.mock("@/lib/api-client", () => ({ postSeen }));
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
```
Add the test inside the existing `describe`:
```ts
it("marks the opened session seen/focused on mount", () => {
  useSessionState.getState().reset();
  postSeen.mockClear();
  render(<MobileTerminalRoute />);
  expect(useSessionState.getState().focusedKey).toBe(stateKey("s1", "default", "demo-web"));
  expect(postSeen).toHaveBeenCalledWith({ serverId: "s1", target: "default", sessionName: "demo-web" });
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — in `web/src/routes/terminal.tsx`, after reading `serverId`/`paneId`/`target`/`session`:
```tsx
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
// inside MobileTerminalRoute:
useFocusedSeen({ serverId, target, sessionName: session });
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit `feat(m8): mobile terminal marks its session seen on open`.

---

### Task 13: Reset live-state store on sign-out (Phase D)

**Files:** Modify `web/src/store/auth.ts`, `web/src/store/auth.test.ts`.

**Interfaces:** Consumes `useSessionState.reset` (T3).

- [ ] **Step 1: Failing test** — add `import { useSessionState } from "@/store/session-state";` to the top of `web/src/store/auth.test.ts`, then append:
```ts
it("clears the live-state store on sign-out", () => {
  useSessionState.getState().applySnapshot([{ server: "s", target: "t", session: "a", state: "blocked" }]);
  useAuth.getState().clear();
  expect(useSessionState.getState().live.size).toBe(0);
});
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** — in `web/src/store/auth.ts`, import `useSessionState` and call `useSessionState.getState().reset()` inside `resetGridAndCache()`:
```ts
import { useSessionState } from "@/store/session-state";
// in resetGridAndCache():
usePanes.setState({ panes: [], focusedId: null });
useSessionState.getState().reset();
void import("@/lib/query-client").then((m) => m.queryClient.clear());
```

- [ ] **Step 4: Run, expect PASS.**  — [ ] **Step 5:** commit `feat(m8): reset live-state store on sign-out`.

---

## Self-Review (plan vs spec)

**Spec coverage:** dots on every surface → T4 (StateDot), T6/T7/T8 (SessionList/Sidebar/GridView). Blocked-first → `sortBlockedFirst` (T1) used in T6/T7. Live SSE → T2 (StateStream), T3 (store), T9 (mount). Reconnect resync → T2 re-fires snapshot + T3 `applySnapshot` replace. Per-principal seen → T5 (`postSeen`), T10 (`useFocusedSeen`), T11/T12 (desktop/mobile triggers); optimistic mask + re-alert clear + continuous suppression → T3 + T1 `present`. Server rollup client-side → T7 via `rollUp`. Sign-out reset → T13. Contracts mirror → T1. All §17 deliverables map to a task.

**Placeholder scan:** code is complete for every module; the only "match the existing harness" notes are in T12's test (the existing `terminal.test.tsx` router mock must be mirrored) — flagged, not a code placeholder.

**Type consistency:** `stateKey`/`present`/`effectiveSessionState`/`StateSnapshot`/`sortBlockedFirst` (T1) are used with identical signatures in T3/T6/T7/T8/T11; `StateEventFrame` shape `{server,target,session,state}` is identical in T2/T3; `SeenRequest {serverId,target,sessionName}` identical in T5/T10/T11/T12; `useSessionState` action names identical across T3/T9/T10/T13. `stateOf(row): SessionState` prop identical in T6/T7/T11.

**Scope:** single milestone, frontend-only; Phase-4 items excluded per spec §10.

## Execution (ultracode / Workflow)

Implementation runs as Workflow(s): **Phase A serial → Phase B (4 parallel) → Phase C (T6+T8 parallel, then T7) → Phase D (T9/T10/T13 parallel, then T11/T12)**, each implement agent pipelined into an adversarial-verify agent. **Hard verify** (independent skeptic, refute-by-default) on T3, T10, T11 (and T2's reconnect/parse). **Light verify** (vitest + diff read) on T1, T4, T5, T6, T7, T8, T9, T12, T13. Implementation agents write files + run their own test file; the main loop runs the full suite + `tsc` and commits per phase. Then: opus whole-branch review → `/multi-review --codex` (fix all but nitpicks) → safe acceptance (§9 of the spec) → merge + `m8-carryover.md`.
