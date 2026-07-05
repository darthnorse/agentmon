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
});
