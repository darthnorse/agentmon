# Terminal Reconnect UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kill the "blank terminal / stuck on disconnected — close it and reopen it" ritual: reconnect immediately when a pane is (re)opened or focused, show "session ended" instead of retrying forever when the pane is truly gone, and close the agent-side snapshot/attach race that can silently drop pane output.

**Architecture:** Three independent streams. (A) Web reconnect-kick: a new `TerminalSocket.retryNow()` plus a tiny module-level kick bus keyed by pane identity; the panes-store dedupe path and the tile-activation effect emit kicks, the mounted socket consumes them. (B) Web "session ended": containers compute pane liveness from the already-cached sessions queries (`paneIdentity`-based, rename-safe) and pass an `ended` flag + close callback down to `TerminalView`, which renders a distinct banner; the socket keeps its slow retry loop (self-healing if the pane id is recycled). (C) Agent attach-gate: `ControlClient` signals when the tmux control-mode attach handshake completes (first `%end`/`%error`), and the WS handler waits for that signal (bounded) before taking the `capture-pane` snapshot, so every pane byte lands in the snapshot or the live `%output` stream.

**Tech Stack:** Go (agent, stdlib + gorilla/websocket, `go test`), React + TypeScript (web, zustand + TanStack Query, vitest + testing-library). No new dependencies.

## Global Constraints

- Work on branch `fix/terminal-reconnect-ux` cut from `main` (worktree per superpowers:using-git-worktrees at execution time; repo root `/root/agentmon`).
- No new npm or Go dependencies.
- Match the repo's comment style: comments state rationale/constraints, not narration.
- Web commands run in `/root/agentmon/web`: `npm run test:run` (all tests), `npx vitest run <file>` (one file), `npm run typecheck`.
- Agent commands run in `/root/agentmon`: `go test ./agent/...`. tmux 3.5a is installed; integration tests exec it directly (existing pattern).
- NEVER touch the default tmux socket with rw commands; test servers must use dedicated `-L <name>` sockets and `kill-server` only their own socket.
- Session-name mutability rule (existing invariant): pane identity is `paneIdentity(serverId, target, paneId)` — never key new logic on the session name.

---

### Task 1: `TerminalSocket.retryNow()`

**Files:**
- Modify: `web/src/lib/ws-terminal.ts` (class `TerminalSocket`, after `dispose()`)
- Test: `web/src/lib/ws-terminal.test.ts`

**Interfaces:**
- Consumes: existing private fields `disposed`, `ws`, `attempt`, `reconnectTimer`, `open()`.
- Produces: `retryNow(): void` on `TerminalSocket` — cancels a pending backoff timer, resets `attempt` to 0, and dials immediately; no-op when disposed or when a socket already exists (connected/connecting). Task 2 and the visibility handler rely on exactly this signature.

- [ ] **Step 1: Write the failing tests** (append to the `describe("TerminalSocket", …)` block; it already has `FakeWS`, `target`, `loc`, and fake timers in `beforeEach`)

```ts
  it("retryNow cancels the backoff timer, reopens immediately, and resets attempt", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    FakeWS.instances[0].fireOpen();
    FakeWS.instances[0].close(); // unexpected close → schedules reconnect (1200ms)
    expect(FakeWS.instances).toHaveLength(1);
    sock.retryNow();
    expect(FakeWS.instances).toHaveLength(2); // no timer wait
    // attempt was reset: the NEXT failure schedules the base delay again, not 2400ms.
    FakeWS.instances[1].close();
    vi.advanceTimersByTime(1199);
    expect(FakeWS.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeWS.instances).toHaveLength(3);
    sock.dispose();
  });

  it("retryNow is a no-op while a socket exists (connected or connecting)", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open(); // connecting (never fired open)
    sock.retryNow();
    expect(FakeWS.instances).toHaveLength(1);
    FakeWS.instances[0].fireOpen(); // connected
    sock.retryNow();
    expect(FakeWS.instances).toHaveLength(1);
    sock.dispose();
  });

  it("retryNow is a no-op after dispose", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    sock.dispose();
    sock.retryNow();
    expect(FakeWS.instances).toHaveLength(1);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/lib/ws-terminal.test.ts`
Expected: FAIL — `sock.retryNow is not a function`

- [ ] **Step 3: Implement `retryNow` and reuse it from the visibility handler**

Add after `dispose()` in `web/src/lib/ws-terminal.ts`:

```ts
  // Reconnect NOW instead of waiting out the backoff. Called when someone re-opens
  // or focuses this pane (kick bus / tile activation) and on tab-visibility wake:
  // user intent means the pane is probably alive again (e.g. a recreated session
  // reusing a recycled pane id), so a stale multi-second timer must not make the
  // terminal look dead. No-op when a socket already exists — never drop a live or
  // in-flight connection.
  retryNow(): void {
    if (this.disposed || this.ws !== null) return;
    this.attempt = 0;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.open();
  }
```

Replace the body of `onVisibility` so the two paths cannot drift:

```ts
  private onVisibility(): void {
    if (document.visibilityState === "visible") this.retryNow(); // wake → reconnect immediately
  }
```

(The old body's `disposed`/`ws === null` guards are inside `retryNow`; resetting `attempt` on wake is intentional — a fresh user gesture restarts the backoff curve.)

- [ ] **Step 4: Run the file's full test suite (old visibility tests must still pass)**

Run: `cd /root/agentmon/web && npx vitest run src/lib/ws-terminal.test.ts`
Expected: PASS (all)

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/ws-terminal.ts web/src/lib/ws-terminal.test.ts
git commit -m "feat(web): TerminalSocket.retryNow — cancel backoff and dial immediately"
```

---

### Task 2: Reconnect-kick bus + socket subscription + focus kick

**Files:**
- Create: `web/src/lib/reconnect-kick.ts`
- Create: `web/src/lib/reconnect-kick.test.ts`
- Modify: `web/src/hooks/useTerminalSession.ts`
- Modify: `web/src/components/TerminalView.tsx` (the `active`/`focusNonce` effect)
- Test: `web/src/hooks/useTerminalSession.test.tsx`, `web/src/components/TerminalView.test.tsx`

**Interfaces:**
- Consumes: `TerminalSocket.retryNow()` (Task 1), `paneIdentity(serverId, target, paneId)` from `@/lib/pane-identity`.
- Produces: `onReconnectKick(id: string, fn: () => void): () => void` (subscribe, returns unsubscribe) and `kickReconnect(id: string): void`, both keyed by `paneIdentity` strings. `useTerminalSession` additionally returns `retryNow: () => void`. Task 3 calls `kickReconnect`.

- [ ] **Step 1: Write the bus test** — `web/src/lib/reconnect-kick.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { onReconnectKick, kickReconnect } from "@/lib/reconnect-kick";

describe("reconnect-kick bus", () => {
  it("delivers kicks only to same-id listeners, and unsubscribe stops delivery", () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = onReconnectKick("s:default:%0", a);
    const offB = onReconnectKick("s:default:%1", b);
    kickReconnect("s:default:%0");
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).not.toHaveBeenCalled();
    offA();
    kickReconnect("s:default:%0");
    expect(a).toHaveBeenCalledTimes(1);
    kickReconnect("s:default:%2"); // no listeners → no throw
    offB();
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/lib/reconnect-kick.test.ts`
Expected: FAIL — cannot resolve `@/lib/reconnect-kick`

- [ ] **Step 3: Implement the bus** — `web/src/lib/reconnect-kick.ts`:

```ts
// A tiny pane-scoped signal: "someone just (re)opened or focused this pane —
// reconnect NOW instead of waiting out the backoff". Emitters (the panes-store
// dedupe path, tile activation) know only the pane identity; the mounted
// TerminalView's socket subscribes. Module-level rather than React context
// because emitters live in plain zustand stores. Keys are paneIdentity strings.
type Kick = () => void;

const listeners = new Map<string, Set<Kick>>();

export function onReconnectKick(id: string, fn: Kick): () => void {
  let set = listeners.get(id);
  if (!set) {
    set = new Set();
    listeners.set(id, set);
  }
  set.add(fn);
  return () => {
    set.delete(fn);
    if (set.size === 0) listeners.delete(id);
  };
}

export function kickReconnect(id: string): void {
  listeners.get(id)?.forEach((fn) => fn());
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd /root/agentmon/web && npx vitest run src/lib/reconnect-kick.test.ts`
Expected: PASS

- [ ] **Step 5: Write the failing hook test** (append to `web/src/hooks/useTerminalSession.test.tsx` — note its module mock of `@/lib/ws-terminal`: extend the mock class with a module-level `retryNow` spy)

Extend the existing `vi.mock("@/lib/ws-terminal", …)` factory's class with a spy method. Add at the top of the file, next to `let captured`:

```ts
const retryNowSpy = vi.fn();
```

and inside the mocked class add:

```ts
      retryNow = retryNowSpy;
```

Then append the test:

```ts
import { onReconnectKick as _unused } from "@/lib/reconnect-kick"; // real module (not mocked)
import { kickReconnect } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";

describe("useTerminalSession reconnect kick", () => {
  it("a kick for this pane's identity calls retryNow; unmount unsubscribes", () => {
    retryNowSpy.mockClear();
    const { unmount } = renderHook(() => useTerminalSession(target));
    kickReconnect(paneIdentity(target.serverId, target.target, target.paneId));
    expect(retryNowSpy).toHaveBeenCalledTimes(1);
    kickReconnect(paneIdentity("other", "default", "%9"));
    expect(retryNowSpy).toHaveBeenCalledTimes(1); // different pane → not ours
    unmount();
    kickReconnect(paneIdentity(target.serverId, target.target, target.paneId));
    expect(retryNowSpy).toHaveBeenCalledTimes(1); // unsubscribed
  });
});
```

(`target` is the file's existing `{ serverId: "s", target: "default", paneId: "%1" }`. Merge the imports with the file's existing import lines; drop the `_unused` line — it is shown only to flag that `@/lib/reconnect-kick` must NOT be mocked.)

- [ ] **Step 6: Run it to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/hooks/useTerminalSession.test.tsx`
Expected: FAIL — `retryNowSpy` not called (no subscription exists yet)

- [ ] **Step 7: Wire the hook** — in `web/src/hooks/useTerminalSession.ts`:

Add imports:

```ts
import { onReconnectKick } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";
```

In the main `React.useEffect`, after `sock.open();` and before the cleanup return, subscribe; and unsubscribe in the cleanup:

```ts
    sockRef.current = sock;
    sock.open();
    // Re-open/focus of this pane elsewhere in the UI (panes-store dedupe, tile
    // activation) must not leave this socket asleep in reconnect backoff.
    const offKick = onReconnectKick(
      paneIdentity(target.serverId, target.target, target.paneId),
      () => sock.retryNow(),
    );
    return () => { offKick(); sock.dispose(); sockRef.current = null; };
```

Add a stable `retryNow` callback next to `handleResize` and return it:

```ts
  const retryNow = React.useCallback(() => sockRef.current?.retryNow(), []);
```

```ts
  return { xtermRef, controller, connected, everConnected, handleData, handleResize, retryNow };
```

- [ ] **Step 8: Write the failing TerminalView test** (append to `web/src/components/TerminalView.test.tsx`; extend its `vi.mock("@/lib/ws-terminal", …)` class with `retryNow = retryNow;` next to the existing `open`/`dispose` spies, declaring `const retryNow = vi.fn();` beside them)

```ts
  it("kicks the socket to reconnect when the tile becomes active", () => {
    retryNow.mockClear();
    const { rerender } = render(
      <TerminalView serverId="s" paneId="%1" target="default" active={false} />,
    );
    expect(retryNow).not.toHaveBeenCalled();
    rerender(<TerminalView serverId="s" paneId="%1" target="default" active={true} />);
    expect(retryNow).toHaveBeenCalled();
  });
```

- [ ] **Step 9: Run it to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/components/TerminalView.test.tsx`
Expected: FAIL — `retryNow` not called

- [ ] **Step 10: Wire TerminalView's activation effect** — in `web/src/components/TerminalView.tsx`, destructure `retryNow` from the hook and extend the effect:

```ts
  const { xtermRef, controller, connected, everConnected, handleData, handleResize, retryNow } =
    useTerminalSession(targetObj);
```

```ts
  React.useEffect(() => {
    if (active) {
      xtermRef.current?.focus();
      // A tile brought to the front must not sit out a reconnect backoff window.
      retryNow();
    }
  }, [active, focusNonce, retryNow]);
```

- [ ] **Step 11: Run both test files + typecheck**

Run: `cd /root/agentmon/web && npx vitest run src/hooks/useTerminalSession.test.tsx src/components/TerminalView.test.tsx && npm run typecheck`
Expected: PASS, no type errors

- [ ] **Step 12: Commit**

```bash
git add web/src/lib/reconnect-kick.ts web/src/lib/reconnect-kick.test.ts \
        web/src/hooks/useTerminalSession.ts web/src/hooks/useTerminalSession.test.tsx \
        web/src/components/TerminalView.tsx web/src/components/TerminalView.test.tsx
git commit -m "feat(web): reconnect-kick bus — focus/activation reconnects a backed-off terminal"
```

---

### Task 3: `openPane` dedupe emits a kick

**Files:**
- Modify: `web/src/store/panes.ts` (`openPane`, the `existing` branch)
- Test: `web/src/store/panes.test.ts`

**Interfaces:**
- Consumes: `kickReconnect(id)` + `paneIdentity(...)` (Task 2).
- Produces: behavioral only — re-opening an already-open tile kicks its socket. No API change.

- [ ] **Step 1: Write the failing test** (append inside `describe("panes store", …)` in `web/src/store/panes.test.ts`; add imports `import { onReconnectKick } from "@/lib/reconnect-kick";`, `import { paneIdentity } from "@/lib/pane-identity";`, and `vi` to the vitest import)

```ts
  it("re-opening an already-open pane kicks its reconnect (no new tile)", () => {
    usePanes.getState().openPane(mk(0));
    const kicked = vi.fn();
    const off = onReconnectKick(paneIdentity("s", "default", "%0"), kicked);
    const r = usePanes.getState().openPane(mk(0)); // dedupe path
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(kicked).toHaveBeenCalledTimes(1);
    off();
  });

  it("first open does NOT kick (a fresh socket dials by itself)", () => {
    const kicked = vi.fn();
    const off = onReconnectKick(paneIdentity("s", "default", "%0"), kicked);
    usePanes.getState().openPane(mk(0));
    expect(kicked).not.toHaveBeenCalled();
    off();
  });
```

- [ ] **Step 2: Run to verify the first test fails**

Run: `cd /root/agentmon/web && npx vitest run src/store/panes.test.ts`
Expected: FAIL — `kicked` not called

- [ ] **Step 3: Implement** — in `web/src/store/panes.ts` add imports:

```ts
import { kickReconnect } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";
```

and change the dedupe branch of `openPane`:

```ts
    if (existing) {
      // Already open → no-op on the grid, but the tile's socket may be asleep in
      // reconnect backoff (e.g. a recreated same-named session reuses pane %0 after
      // a tmux server restart) — kick it so the tile comes alive immediately.
      kickReconnect(paneIdentity(p.serverId, p.target, p.paneId));
      return { ok: true }; // do NOT change focusedId
    }
```

- [ ] **Step 4: Run the store's full test file**

Run: `cd /root/agentmon/web && npx vitest run src/store/panes.test.ts`
Expected: PASS (all, including pre-existing tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/store/panes.ts web/src/store/panes.test.ts
git commit -m "feat(web): openPane dedupe kicks the existing tile's reconnect"
```

---

### Task 4: TerminalView "session ended" banner

**Files:**
- Modify: `web/src/components/TerminalView.tsx`
- Test: `web/src/components/TerminalView.test.tsx`

**Interfaces:**
- Consumes: existing `connected`/`everConnected` from `useTerminalSession`.
- Produces: two new optional props on `TerminalView`: `ended?: boolean` (the pane is confirmed absent from a fresh sessions list) and `onClose?: () => void` (close this tile/tab). When `ended && !connected`, the banner reads "session ended" with a close button instead of "disconnected — reconnecting…". When the socket IS connected the `ended` flag is ignored (fresh data beats a stale list; also covers pane-id recycling). Tasks 5–6 pass these props.

- [ ] **Step 1: Write the failing tests** (append to `web/src/components/TerminalView.test.tsx`; it already imports `render`; add `screen, fireEvent` to the testing-library import)

```ts
  it("shows 'session ended' + close instead of the reconnect banner when ended", () => {
    const onClose = vi.fn();
    render(
      <TerminalView serverId="s" paneId="%1" target="default" ended onClose={onClose} />,
    );
    expect(screen.getByText("session ended")).toBeInTheDocument();
    expect(screen.queryByText(/connecting|reconnecting/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows the normal connecting banner when not ended", () => {
    render(<TerminalView serverId="s" paneId="%1" target="default" />);
    expect(screen.getByText("connecting…")).toBeInTheDocument();
    expect(screen.queryByText("session ended")).toBeNull();
  });

  it("omits the close button when no onClose is provided", () => {
    render(<TerminalView serverId="s" paneId="%1" target="default" ended />);
    expect(screen.getByText("session ended")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "close" })).toBeNull();
  });
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd /root/agentmon/web && npx vitest run src/components/TerminalView.test.tsx`
Expected: FAIL — "session ended" not found

- [ ] **Step 3: Implement** — in `web/src/components/TerminalView.tsx` add the props:

```ts
export function TerminalView({
  serverId, paneId, target, showKeyBar = false, active, focusNonce, fontSize, theme,
  ended, onClose,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
  active?: boolean;
  focusNonce?: number;
  fontSize?: number;
  theme?: ITheme;
  // The pane is confirmed gone (absent from a successful sessions fetch). Display-
  // only: the socket keeps its slow retry so a recycled pane id self-heals.
  ended?: boolean;
  onClose?: () => void;
}) {
```

and replace the banner block:

```tsx
      {!connected && (
        ended ? (
          <div className="absolute left-0 right-0 top-0 z-10 flex items-center justify-center gap-3 bg-muted px-2 py-1 text-center text-xs font-semibold text-muted-foreground">
            <span>session ended</span>
            {onClose && (
              <button type="button" className="underline underline-offset-2" onClick={onClose}>
                close
              </button>
            )}
          </div>
        ) : (
          <div className="absolute left-0 right-0 top-0 z-10 bg-destructive px-2 py-1 text-center text-xs font-semibold text-destructive-foreground">
            {everConnected ? "disconnected — reconnecting…" : "connecting…"}
          </div>
        )
      )}
```

- [ ] **Step 4: Run the component's full test file**

Run: `cd /root/agentmon/web && npx vitest run src/components/TerminalView.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/components/TerminalView.tsx web/src/components/TerminalView.test.tsx
git commit -m "feat(web): 'session ended' banner with close affordance on TerminalView"
```

---

### Task 5: Pane-liveness helper + desktop wiring (index → DesktopShell → GridView)

**Files:**
- Modify: `web/src/lib/pane-identity.ts` (add `liveIdentSet`)
- Modify: `web/src/routes/index.tsx` (compute `readyServers` + `livePaneIds`, pass to DesktopShell)
- Modify: `web/src/components/DesktopShell.tsx` (thread the two props to GridView)
- Modify: `web/src/components/GridView.tsx` (compute `ended` per tile; pass `ended` + `onClose` to TerminalView)
- Test: `web/src/lib/pane-identity.test.ts`

**Interfaces:**
- Consumes: `TerminalView`'s `ended`/`onClose` props (Task 4); `SessionRow`-shaped rows (`{ server: { id }, session: { target }, pane: { id } }`); the existing `sessionQs` `useQueries` results and `closePane` in GridView.
- Produces: `liveIdentSet(rows): Set<string>` in `@/lib/pane-identity` (paneIdentity of every live row — structural row type, no component import). `GridView` accepts optional props `livePaneIds?: Set<string>` and `readyServers?: Set<string>`; `DesktopShell` accepts and forwards the same two props. Ended rule everywhere: `readyServers.has(serverId) && !livePaneIds.has(paneIdentity(serverId, target, paneId))`. Task 6 reuses `liveIdentSet`.

- [ ] **Step 1: Write the failing helper test** (append to `web/src/lib/pane-identity.test.ts`)

```ts
import { liveIdentSet } from "@/lib/pane-identity";

describe("liveIdentSet", () => {
  it("collects the pane identity of every row", () => {
    const rows = [
      { server: { id: "s1" }, session: { target: "default" }, pane: { id: "%0" } },
      { server: { id: "s2" }, session: { target: "alt" }, pane: { id: "%3" } },
    ];
    const set = liveIdentSet(rows);
    expect(set.has("s1:default:%0")).toBe(true);
    expect(set.has("s2:alt:%3")).toBe(true);
    expect(set.size).toBe(2);
  });
});
```

(Merge the import with the file's existing `paneIdentity` import.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/lib/pane-identity.test.ts`
Expected: FAIL — `liveIdentSet` is not exported

- [ ] **Step 3: Implement the helper** — append to `web/src/lib/pane-identity.ts`:

```ts
// The set of live pane identities in a flattened session-row list. Used to decide
// whether an open tile/tab's pane still exists. Rename-safe by construction (the
// identity ignores the mutable session name). Structural row type so lib code does
// not import component modules.
export const liveIdentSet = (
  rows: ReadonlyArray<{ server: { id: string }; session: { target: string }; pane: { id: string } }>,
): Set<string> => new Set(rows.map((r) => paneIdentity(r.server.id, r.session.target, r.pane.id)));
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /root/agentmon/web && npx vitest run src/lib/pane-identity.test.ts`
Expected: PASS

- [ ] **Step 5: Wire the desktop containers** (no new unit tests — pure prop threading; the logic is covered by Steps 1–4 and Task 4's banner tests, and `npm run typecheck` verifies the wiring)

`web/src/routes/index.tsx` — add imports `paneIdentity, liveIdentSet` from `@/lib/pane-identity`, then after the `rows` line (`const rows = flattenSessions(servers, byServer);`):

```ts
  // Pane-liveness for the "session ended" banner: a pane counts as gone ONLY when
  // its server's sessions query has succeeded (readyServers) and the fresh list
  // does not contain the pane. Query errors / not-yet-loaded → unknown → keep the
  // ordinary reconnecting banner.
  const readyServers = React.useMemo(
    () => new Set(servers.filter((_, i) => sessionQs[i]?.isSuccess).map((s) => s.id)),
    [servers, sessionQs],
  );
  const livePaneIds = React.useMemo(() => liveIdentSet(rows), [rows]);
```

and extend the DesktopShell call:

```tsx
          <DesktopShell servers={servers} rows={rows} query={query} onQueryChange={setQuery}
            stateOf={stateOf} livePaneIds={livePaneIds} readyServers={readyServers} />
```

`web/src/components/DesktopShell.tsx` — add the two props to the signature and type:

```ts
export function DesktopShell({
  servers, rows, query, onQueryChange, stateOf, livePaneIds, readyServers,
}: {
  // …existing prop types unchanged…
  livePaneIds?: Set<string>;
  readyServers?: Set<string>;
}) {
```

and forward them:

```tsx
        <GridView livePaneIds={livePaneIds} readyServers={readyServers} />
```

`web/src/components/GridView.tsx` — accept the props, import `paneIdentity`:

```ts
import { paneIdentity } from "@/lib/pane-identity";

export function GridView({ livePaneIds, readyServers }: {
  livePaneIds?: Set<string>;
  readyServers?: Set<string>;
} = {}) {
```

and inside the tile `.map`, before the `<TerminalView …>`:

```ts
          // Confirmed-gone only on fresh data; TerminalView further requires the
          // socket to be disconnected, so a stale list can never mask a live pane.
          const ended = !!readyServers?.has(p.serverId) &&
            !!livePaneIds && !livePaneIds.has(paneIdentity(p.serverId, p.target, p.paneId));
```

```tsx
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target}
                  active={activeWindowId === p.id} focusNonce={focusNonce}
                  fontSize={fontSize} theme={theme}
                  ended={ended} onClose={() => closePane(p.id)} />
```

- [ ] **Step 6: Typecheck + run the web suite (GridView/DesktopShell/index have existing tests that must stay green)**

Run: `cd /root/agentmon/web && npm run typecheck && npm run test:run`
Expected: no type errors; all tests PASS

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/pane-identity.ts web/src/lib/pane-identity.test.ts \
        web/src/routes/index.tsx web/src/components/DesktopShell.tsx web/src/components/GridView.tsx
git commit -m "feat(web): desktop grid marks confirmed-gone panes as 'session ended'"
```

---

### Task 6: Mobile wiring (terminal route + MobileTerminalStack)

**Files:**
- Modify: `web/src/routes/terminal.tsx`
- Modify: `web/src/components/MobileTerminalStack.tsx`
- Test: `web/src/components/MobileTerminalStack.test.tsx`

**Interfaces:**
- Consumes: `liveIdentSet` (Task 5), `TerminalView` `ended`/`onClose` (Task 4), existing `handleClose(tab)` / `tabs` / `pool` / `removeOpenTab` in the route.
- Produces: `MobileTerminalStack` gains optional props `endedIds?: Set<string>` and `onClosePane?: (id: string) => void` (id = paneIdentity). The route computes `endedIds` for pooled panes and closes via the existing tab-close flow.

- [ ] **Step 1: Write the failing stack test** (append to `web/src/components/MobileTerminalStack.test.tsx`; if the file already mocks `@/components/TerminalView`, extend that factory to record props instead of adding a second mock — one `vi.mock` per module per file)

```tsx
  it("threads ended + onClose(paneIdentity) through to the pane's TerminalView", () => {
    const onClosePane = vi.fn();
    render(
      <MobileTerminalStack
        panes={[{ serverId: "s", target: "default", paneId: "%0" }] as any}
        focusedId="s:default:%0"
        fontSize={14}
        theme={{} as any}
        endedIds={new Set(["s:default:%0"])}
        onClosePane={onClosePane}
      />,
    );
    // With the real TerminalView (or a props-recording mock) the ended banner is on:
    expect(screen.getByText("session ended")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(onClosePane).toHaveBeenCalledWith("s:default:%0");
  });
```

(If the file's existing setup mocks TerminalView with a bare stub, change the stub to render `props.ended ? <div>session ended <button onClick={props.onClose}>close</button></div> : null` so the assertion exercises the prop threading — the banner itself is already covered by Task 4.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/components/MobileTerminalStack.test.tsx`
Expected: FAIL — unknown props / banner absent

- [ ] **Step 3: Implement the stack pass-through** — `web/src/components/MobileTerminalStack.tsx`:

```tsx
export function MobileTerminalStack({
  panes, focusedId, fontSize, theme, endedIds, onClosePane,
}: {
  panes: PoolPane[];
  focusedId: string | null;
  fontSize: number;
  theme: ITheme;
  // paneIdentity ids confirmed gone (fresh sessions fetch without the pane).
  endedIds?: Set<string>;
  onClosePane?: (id: string) => void;
}) {
```

and in the `.map`, extend the `TerminalView`:

```tsx
            <TerminalView
              serverId={p.serverId}
              paneId={p.paneId}
              target={p.target}
              showKeyBar={active}
              active={active}
              fontSize={fontSize}
              theme={theme}
              ended={endedIds?.has(id)}
              onClose={onClosePane ? () => onClosePane(id) : undefined}
            />
```

- [ ] **Step 4: Wire the route** — `web/src/routes/terminal.tsx`: add `liveIdentSet` to the `@/lib/pane-identity` import, hoist the live set (replacing the inline one in the warm effect), compute `endedIds`, and pass both new props where `<MobileTerminalStack panes={pool.panes} focusedId={pool.focusedId} …/>` is rendered:

```ts
  // Hoisted pane-liveness (shared by warming and the "session ended" banner).
  const liveIdents = React.useMemo(() => liveIdentSet(rows), [rows]);
  const readyServers = React.useMemo(
    () => new Set(servers.filter((_, i) => sessionQs[i]?.isSuccess).map((s) => s.id)),
    [servers, sessionQs],
  );
  const endedIds = React.useMemo(() => {
    const out = new Set<string>();
    for (const p of pool.panes) {
      const id = paneIdentity(p.serverId, p.target, p.paneId);
      if (readyServers.has(p.serverId) && !liveIdents.has(id)) out.add(id);
    }
    return out;
  }, [pool.panes, readyServers, liveIdents]);
```

In the warm effect, delete the local `const liveIdents = new Set(rows.map(…));` line and use the hoisted `liveIdents` (add it to the effect's dependency comment if the lint disable list names deps).

Close handler — the ended banner's close goes through the SAME flow as a tab ×, with a fallback for a pooled pane that has no tab:

```ts
  // Ended-banner close: identical to closing the pane's tab; fall back to a direct
  // pool/open-set removal for the (edge) pooled pane that has no tab row.
  const handleClosePane = (id: string) => {
    const tab = tabs.find((t) => t.key === id);
    if (tab) {
      handleClose(tab);
      return;
    }
    removeOpenTab(id);
    pool.close(id);
    if (pool.focusedId === id) navigate({ to: "/" });
  };
```

```tsx
        <MobileTerminalStack panes={pool.panes} focusedId={pool.focusedId}
          fontSize={fontSize} theme={theme}
          endedIds={endedIds} onClosePane={handleClosePane} />
```

(Place `handleClosePane` after `tabs` and `handleClose` are defined.)

- [ ] **Step 5: Typecheck + run the touched suites**

Run: `cd /root/agentmon/web && npm run typecheck && npx vitest run src/components/MobileTerminalStack.test.tsx src/routes`
Expected: no type errors; PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/routes/terminal.tsx web/src/components/MobileTerminalStack.tsx \
        web/src/components/MobileTerminalStack.test.tsx
git commit -m "feat(web): mobile terminals mark confirmed-gone panes as 'session ended'"
```

---

### Task 7: Agent — `ControlClient` attach-handshake signal

**Files:**
- Modify: `agent/internal/tmux/control.go`
- Test: `agent/internal/tmux/control_lifecycle_test.go` (unit), `agent/internal/tmux/control_attach_integration_test.go` (create, real tmux)

**Interfaces:**
- Consumes: existing `readLoop` line dispatch (`%begin` / `%end` / `%error` cases).
- Produces: `func (c *ControlClient) AttachedChan() <-chan struct{}` — closes exactly once, when the first `%end` or `%error` line arrives (the reply terminator of the implicit `attach-session` command; from that point every pane write is guaranteed to surface as `%output`). Task 8's handler selects on it. Hand-built test clients that leave `attached` nil must not panic.

- [ ] **Step 1: Write the failing unit test** (append to `agent/internal/tmux/control_lifecycle_test.go`; it already imports `context` — this test needs `io`, `os/exec`, `testing`, `time`, all present)

```go
// TestReadLoopSignalsAttached: AttachedChan closes on the FIRST %end (the attach
// reply terminator) and only once; %begin alone must not signal.
func TestReadLoopSignalsAttached(t *testing.T) {
	cmd := exec.Command("sleep", "30") // inert process so the deferred Wait can reap
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	c := &ControlClient{
		pane: "%0", cmd: cmd,
		quit: make(chan struct{}), Output: make(chan []byte, 8),
		Done: make(chan struct{}), attached: make(chan struct{}),
	}
	pr, pw := io.Pipe()
	go c.readLoop(pr)

	if _, err := pw.Write([]byte("%begin 1 0 0\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.AttachedChan():
		t.Fatal("attached signalled on %begin — must wait for %end")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := pw.Write([]byte("%end 1 0 0\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.AttachedChan():
	case <-time.After(time.Second):
		t.Fatal("attached not signalled after %end")
	}
	// A second %end must not re-close (would panic).
	if _, err := pw.Write([]byte("%end 2 1 0\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	pw.Close()
	_ = cmd.Process.Kill()
	<-c.Done
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /root/agentmon && go test ./agent/internal/tmux/ -run TestReadLoopSignalsAttached`
Expected: FAIL to compile — `unknown field attached` / `AttachedChan undefined`

- [ ] **Step 3: Implement** — `agent/internal/tmux/control.go`:

Add to the `ControlClient` struct (after the `quit` field):

```go
	// attached closes when the attach-session handshake's reply block terminates
	// (first %end/%error): from that point on, every pane write is guaranteed to be
	// delivered as %output, so a snapshot taken AFTER this cannot leave a gap
	// (bytes written pre-attach are in the snapshot; post-attach in the stream).
	attachOnce sync.Once
	attached   chan struct{}
```

In `NewControlClient`, add to the struct literal (next to `quit`):

```go
		attached: make(chan struct{}),
```

In `readLoop`, extend the `%end`/`%error` case:

```go
			case hasPrefix(line, "%end"), hasPrefix(line, "%error"):
				inBlock = false
				c.markAttached()
```

Add after `readLoop`:

```go
// markAttached signals AttachedChan exactly once, on the first %end/%error — the
// reply terminator of the implicit attach-session command. nil-safe: hand-built
// test clients may not populate the channel.
func (c *ControlClient) markAttached() {
	if c.attached == nil {
		return
	}
	c.attachOnce.Do(func() { close(c.attached) })
}

// AttachedChan closes once the control-mode attach handshake has completed. Callers
// should also watch DoneChan — a client that dies pre-attach never signals this.
func (c *ControlClient) AttachedChan() <-chan struct{} { return c.attached }
```

(`sync` is already imported.)

- [ ] **Step 4: Run the unit test + package**

Run: `cd /root/agentmon && go test ./agent/internal/tmux/`
Expected: PASS (all — including the pre-existing hand-built-client test, which leaves `attached` nil and never hits `%end`)

- [ ] **Step 5: Write the real-tmux integration test** — create `agent/internal/tmux/control_attach_integration_test.go`:

```go
package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A real tmux attach must signal AttachedChan promptly (the handshake's empty
// %begin/%end reply arrives immediately after the server registers the client).
func TestControlClientAttachedSignal_Integration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	const sock = "agentmon-attach-it" // dedicated socket; never the default one
	_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	if out, err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "s", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })

	out, err := exec.Command("tmux", "-L", sock, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	pane := strings.TrimSpace(string(out))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc, err := NewControlClient(ctx, sock, "s", pane)
	if err != nil {
		t.Fatalf("control client: %v", err)
	}
	defer cc.Close()
	go func() { // keep the parser drained so it can never park on Output
		for range cc.Output {
		}
	}()

	select {
	case <-cc.AttachedChan():
	case <-time.After(3 * time.Second):
		t.Fatal("attach handshake never signalled")
	}
}
```

- [ ] **Step 6: Run it**

Run: `cd /root/agentmon && go test ./agent/internal/tmux/ -run TestControlClientAttachedSignal_Integration -v`
Expected: PASS (or SKIP where tmux is absent)

- [ ] **Step 7: Commit**

```bash
git add agent/internal/tmux/control.go agent/internal/tmux/control_lifecycle_test.go \
        agent/internal/tmux/control_attach_integration_test.go
git commit -m "feat(agent): ControlClient signals attach-handshake completion"
```

---

### Task 8: Agent — gate the snapshot on attach

**Files:**
- Modify: `agent/internal/api/ws.go` (`PaneConn` interface + `Handler`, between `NewClient` and the capture)
- Test: `agent/internal/api/ws_test.go` (extend `fakePane` + `buildHandler`, add two tests)

**Interfaces:**
- Consumes: `AttachedChan() <-chan struct{}` (Task 7 — `*tmux.ControlClient` satisfies the extended interface; the production closure in `agent/cmd/agentmon-agent/main.go` needs no change).
- Produces: `PaneConn` gains `AttachedChan() <-chan struct{}`; package-level `var attachWait = 2 * time.Second` (var so tests shorten it). Handler behavior: capture runs only after attach, done, ctx-cancel, or timeout.

- [ ] **Step 1: Extend the fake + write the failing tests** — in `agent/internal/api/ws_test.go`:

Add a field to `fakePane` and the method; default it CLOSED in `newFakePane` so every existing test keeps its current timing:

```go
type fakePane struct {
	out      chan []byte
	done     chan struct{}
	inputs   chan []byte
	resizes  chan [2]int
	closed   chan struct{}
	attached chan struct{}
}

func newFakePane() *fakePane {
	f := &fakePane{
		out: make(chan []byte, 8), done: make(chan struct{}),
		inputs: make(chan []byte, 8), resizes: make(chan [2]int, 8),
		closed: make(chan struct{}), attached: make(chan struct{}),
	}
	close(f.attached) // attached-by-default: pre-existing tests assume no gate
	return f
}

func (f *fakePane) AttachedChan() <-chan struct{} { return f.attached }
```

Refactor `buildHandler` into a capture-injectable variant (delegating wrapper keeps every existing call site untouched):

```go
func buildHandler(t *testing.T, fake *fakePane) http.Handler {
	return buildHandlerCapture(t, fake, func(ctx context.Context, socket, pane string, lines int) ([]byte, error) {
		return []byte("SCROLLBACK"), nil
	})
}

func buildHandlerCapture(t *testing.T, fake *fakePane, capture func(context.Context, string, string, int) ([]byte, error)) http.Handler {
	t.Helper()
	cfg := testTarget()
	h := &PaneIO{
		Cfg:      cfg,
		Verifier: directive.NewVerifier("server-a", []byte(wsKey), nil),
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			// list-panes -a: pane %3 lives in session $1 (mimic real tmux output).
			return []byte("%3\\037$1\n"), nil
		},
		Capture:   capture,
		NewClient: func(ctx context.Context, socket, session, pane string) (PaneConn, error) { return fake, nil },
		Tune:      func(ctx context.Context, socket, session string) {},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /panes/{paneId}/io", RequireBearer(wsToken, h.Handler()))
	return mux
}
```

Append the tests:

```go
// The scrollback snapshot must not run until the control-mode attach completes —
// a byte written to the pane after capture-pane but before the attach lands is in
// NEITHER the snapshot NOR the %output stream (lost → blank terminal).
func TestSnapshotGatedOnAttach(t *testing.T) {
	fake := newFakePane()
	fake.attached = make(chan struct{}) // attach pending
	captureCalled := make(chan struct{})
	srv := httptest.NewServer(buildHandlerCapture(t, fake,
		func(ctx context.Context, socket, pane string, lines int) ([]byte, error) {
			close(captureCalled)
			return []byte("SNAP"), nil
		}))
	defer srv.Close()

	conn, _, err := dial(t, srv, "ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	select {
	case <-captureCalled:
		t.Fatal("capture ran before the attach handshake completed")
	case <-time.After(150 * time.Millisecond):
	}
	close(fake.attached)
	select {
	case <-captureCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("capture never ran after attach completed")
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "SNAP" {
		t.Fatalf("first frame = %q, want SNAP", msg)
	}
}

// A control client that never confirms attach must degrade to the old behavior
// (capture after attachWait), not block the terminal open.
func TestSnapshotAttachTimeoutFallsBack(t *testing.T) {
	old := attachWait
	attachWait = 100 * time.Millisecond
	defer func() { attachWait = old }()

	fake := newFakePane()
	fake.attached = make(chan struct{}) // never closes
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()

	conn, _, err := dial(t, srv, "ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "SCROLLBACK" {
		t.Fatalf("first frame = %q, want SCROLLBACK", msg)
	}
}
```

- [ ] **Step 2: Run to verify compilation fails, then behavior fails**

Run: `cd /root/agentmon && go test ./agent/internal/api/ -run TestSnapshot`
Expected: first a compile error (`attachWait` undefined; `fakePane` missing from `PaneConn` is fine since the interface isn't extended yet) — after adding ONLY the fake changes it must fail `TestSnapshotGatedOnAttach` with "capture ran before the attach handshake completed".

- [ ] **Step 3: Implement** — `agent/internal/api/ws.go`:

Extend `PaneConn`:

```go
type PaneConn interface {
	OutputChan() <-chan []byte
	DoneChan() <-chan struct{}
	AttachedChan() <-chan struct{}
	SendInput([]byte) error
	Resize(cols, rows int) error
	Close()
}
```

Add near `wsUpgrader`:

```go
// attachWait bounds how long the handler waits for the control-mode attach
// handshake before capturing anyway — a slow attach must degrade to the old
// racy-but-working behavior, never block the terminal open. Var so tests shorten it.
var attachWait = 2 * time.Second
```

In `Handler()`, between `defer cc.Close()` and the `// 1) scrollback bootstrap` block:

```go
		// Gate the snapshot on attach completion: a byte written to the pane after
		// capture-pane runs but before the attach lands would be in NEITHER the
		// snapshot NOR the %output stream (lost → blank terminal until the next
		// output). After the handshake, post-capture bytes always stream.
		select {
		case <-cc.AttachedChan():
		case <-cc.DoneChan():
			return // control client died before attaching
		case <-ctx.Done():
			return
		case <-time.After(attachWait):
		}
```

- [ ] **Step 4: Run the package's full suite (all pre-existing WS tests must stay green)**

Run: `cd /root/agentmon && go test ./agent/internal/api/`
Expected: PASS

- [ ] **Step 5: Build the agent binary to prove the production wiring still satisfies the interface**

Run: `cd /root/agentmon && go build ./agent/...`
Expected: clean build (the `main.go` closure returns `*tmux.ControlClient`, which now implements `AttachedChan`)

- [ ] **Step 6: Commit**

```bash
git add agent/internal/api/ws.go agent/internal/api/ws_test.go
git commit -m "fix(agent): wait for control-mode attach before the scrollback snapshot"
```

---

### Task 9: Full verification sweep

**Files:** none (verification only)

- [ ] **Step 1: Full Go suite**

Run: `cd /root/agentmon && go vet ./agent/... && go test ./agent/... ./shared/...`
Expected: PASS

- [ ] **Step 2: Full web suite + typecheck + production build**

Run: `cd /root/agentmon/web && npm run test:run && npm run build`
Expected: all tests PASS; build succeeds

- [ ] **Step 3: End-to-end sanity on this host** (agent runs here; hub is remote — this exercises the agent-side path only)

```bash
# The agentmon socket carries the owner's LIVE sessions — create/kill ONLY the repro session.
TOKEN=$(cat "$(grep -oP 'hub_token\s*=\s*"file:\K[^"]+' /etc/agentmon/agent.toml)" | tr -d '[:space:]')
curl -s -X POST "http://127.0.0.1:8377/sessions?target=default" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"plan-verify"}'
tmux -L agentmon list-sessions   # expect plan-verify listed
tmux -L agentmon kill-session -t "=plan-verify"
```

Expected: `{"name":"plan-verify"}` then the session appears and is cleanly removed. (NOTE: this verifies the deployed binary's API, not the new code — the new agent code ships on the next agent rebuild+restart; restart only per the KillMode caveat in `agent-restart-kills-sessions` memory.)

- [ ] **Step 4: Use superpowers:finishing-a-development-branch** to choose merge/PR/cleanup.

---

## Known trade-offs (documented decisions, not open questions)

- **Attach-gate duplication window:** after the fix, a byte written between attach-confirm and capture appears in both the snapshot and the stream (a few ms; cosmetic, self-heals on the connect-time resize repaint). The pre-fix code had BOTH this duplication mode and the loss mode; the fix removes loss without adding anything new.
- **`ended` is display-only:** the socket keeps its capped retry loop, so a recycled pane id (new session reusing `%0`) reconnects by itself and the banner clears on the next successful sessions fetch.
- **Ended detection is paneIdentity-based** (not session-name-based) so cross-device renames never false-positive; a recycled pane owned by a *different* session therefore shows as live — same accepted limitation as the existing "wrong session name if focused pane's row vanishes" note.
- **Freshness:** sessions queries refetch on window focus and existing invalidations (create/rename/kill); there is no polling, so an externally-killed session may show "disconnected — reconnecting…" until the next refetch. Acceptable — the banner upgrade is best-effort UX, not a liveness protocol.
- **Create-flow flash (fixed in-branch):** the create handler optimistically seeds the sessions cache with the returned Session before invalidating, so a just-created tile never computes `ended` from the pre-create list.
