import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render } from "@testing-library/react";
import { useStateStream } from "@/hooks/useStateStream";
import { useSessionState } from "@/store/session-state";
import { usePrefs } from "@/store/prefs";
import { stateKey } from "@/lib/state";
import type { StateEventFrame } from "@/lib/contracts";

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

function emitDelta(frame: StateEventFrame) {
  FakeES.instances[0].emit("state", JSON.stringify(frame));
}

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

describe("useStateStream done-too alert gate (prefs.alertOnDone)", () => {
  let onAttention: ReturnType<typeof vi.fn>;
  function AlertHarness() {
    useStateStream({ EventSourceCtor: FakeES as unknown as typeof EventSource }, onAttention);
    return null;
  }
  const DONE: StateEventFrame = { server: "srv", target: "t", session: "sesh", state: "done" };

  beforeEach(() => {
    FakeES.instances = [];
    useSessionState.getState().reset();
    usePrefs.getState().setAlertOnDone(false);
    onAttention = vi.fn();
  });
  afterEach(() => { usePrefs.getState().setAlertOnDone(false); });

  it("does NOT fire onAttention into done when alertOnDone is off", () => {
    render(<AlertHarness />);
    emitDelta(DONE);
    expect(onAttention).not.toHaveBeenCalled();
  });

  it("fires onAttention into done (non-focused) when alertOnDone is on", () => {
    usePrefs.getState().setAlertOnDone(true);
    render(<AlertHarness />);
    emitDelta(DONE);
    expect(onAttention).toHaveBeenCalledTimes(1);
    expect(onAttention.mock.calls[0][0]).toMatchObject({ state: "done", session: "sesh" });
  });

  it("does NOT fire into done for the focused key even when alertOnDone is on", () => {
    usePrefs.getState().setAlertOnDone(true);
    useSessionState.getState().setFocusedKey(stateKey("srv", "t", "sesh"));
    render(<AlertHarness />);
    emitDelta(DONE);
    expect(onAttention).not.toHaveBeenCalled();
  });

  it("does not re-fire into done when already done", () => {
    usePrefs.getState().setAlertOnDone(true);
    render(<AlertHarness />);
    emitDelta(DONE);
    emitDelta(DONE);
    expect(onAttention).toHaveBeenCalledTimes(1);
  });

  it("still fires into blocked regardless of alertOnDone", () => {
    render(<AlertHarness />); // alertOnDone off
    emitDelta({ ...DONE, state: "blocked" });
    expect(onAttention).toHaveBeenCalledTimes(1);
    expect(onAttention.mock.calls[0][0]).toMatchObject({ state: "blocked" });
  });
});
