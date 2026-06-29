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
