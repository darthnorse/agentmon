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
