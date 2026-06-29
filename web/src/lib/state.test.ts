import { describe, it, expect } from "vitest";
import {
  rollUp, normalizeState, STATE_META, stateKey, present,
  effectiveSessionState, sortBlockedFirst, type StateSnapshot,
} from "@/lib/state";
import type { SessionState } from "@/lib/contracts";

describe("normalizeState", () => {
  it("passes through known states and clamps anything else to unknown", () => {
    (["blocked", "done", "working", "idle", "unknown"] as SessionState[]).forEach((s) =>
      expect(normalizeState(s)).toBe(s));
    expect(normalizeState("")).toBe("unknown");
    expect(normalizeState("garbage")).toBe("unknown");
    expect(normalizeState(undefined)).toBe("unknown");
    expect(normalizeState(null)).toBe("unknown");
  });
});

describe("effectiveSessionState clamps", () => {
  it("coerces an out-of-enum live value to unknown (no crash, no NaN sort)", () => {
    const live = new Map<string, SessionState>([[stateKey("s", "t", "x"), "weird" as SessionState]]);
    expect(effectiveSessionState({ live, seen: new Set(), focusedKey: null }, "s", "t", "x")).toBe("unknown");
  });
});

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
