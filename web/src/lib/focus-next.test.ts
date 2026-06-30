import { describe, it, expect } from "vitest";
import { nextBlocked } from "@/lib/focus-next";
import { stateKey } from "@/lib/state";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { ServerSummary, SessionState } from "@/lib/contracts";

const servers: ServerSummary[] = [{ id: "s1", name: "alpha", labels: [], enabled: true }];

function mk(name: string, win: string, pane: string) {
  return {
    name, server: "s1", target: "default", cwd: `/${name}`, command: "claude",
    windows: [{ id: win, index: "0", name: "m", panes: [{ id: pane, command: "c", cwd: `/${name}` }] }],
  };
}

// Input order a,b,c,d. States: a idle, b blocked, c working, d blocked.
// sortBlockedFirst → [b, d, c, a].
const byServer = { s1: [mk("a", "@0", "%0"), mk("b", "@1", "%1"), mk("c", "@2", "%2"), mk("d", "@3", "%3")] };
const rows = flattenSessions(servers, byServer);
const stateMap: Record<string, SessionState> = { a: "idle", b: "blocked", c: "working", d: "blocked" };
const stateOf = (r: SessionRow): SessionState => stateMap[r.session.name];
const keyOf = (name: string) => stateKey("s1", "default", name);

describe("nextBlocked", () => {
  it("returns the first blocked (blocked-first order) when currentKey is null", () => {
    const got = nextBlocked(rows, stateOf, null);
    expect(got?.session.name).toBe("b");
  });

  it("returns the next blocked after the current, wrapping past the end", () => {
    // current = d (the last blocked) → wraps to b.
    expect(nextBlocked(rows, stateOf, keyOf("d"))?.session.name).toBe("b");
  });

  it("returns the following blocked when the current is itself blocked (excludes the focused)", () => {
    // current = b → next blocked is d, not b again.
    expect(nextBlocked(rows, stateOf, keyOf("b"))?.session.name).toBe("d");
  });

  it("skips non-blocked rows starting from a non-blocked current", () => {
    // current = a (idle, last in sorted order) → next blocked wrapping is b.
    expect(nextBlocked(rows, stateOf, keyOf("a"))?.session.name).toBe("b");
    // current = c (working) → next blocked wrapping is b.
    expect(nextBlocked(rows, stateOf, keyOf("c"))?.session.name).toBe("b");
  });

  it("falls back to the first blocked when currentKey matches no row", () => {
    expect(nextBlocked(rows, stateOf, keyOf("ghost"))?.session.name).toBe("b");
  });

  it("returns null when nothing is blocked", () => {
    const calm = (): SessionState => "idle";
    expect(nextBlocked(rows, calm, null)).toBeNull();
    expect(nextBlocked(rows, calm, keyOf("a"))).toBeNull();
  });

  it("returns null for an empty row set", () => {
    expect(nextBlocked([], stateOf, null)).toBeNull();
  });
});
