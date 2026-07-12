import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ invalidateQueries: vi.fn() }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));

import { useBoardStream } from "@/hooks/useBoardStream";
import { useBoardAttention } from "@/store/board";

class FakeES {
  static instances: FakeES[] = [];
  listeners = new Map<string, (ev: MessageEvent) => void>();
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string, public opts?: EventSourceInit) { FakeES.instances.push(this); }
  addEventListener(type: string, fn: (ev: MessageEvent) => void) { this.listeners.set(type, fn); }
  close() {}
  emit(type: string, data: string) { this.listeners.get(type)?.({ data } as MessageEvent); }
}

function Harness({ onAttention }: { onAttention?: (f: unknown) => void }) {
  useBoardStream({ EventSourceCtor: FakeES as unknown as typeof EventSource }, onAttention);
  return null;
}

describe("useBoardStream", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeES.instances = [];
    h.invalidateQueries.mockReset();
    useBoardAttention.getState().reset();
  });
  afterEach(() => vi.useRealTimers());

  it("seeds the store from board-snapshot and invalidates once", () => {
    render(<Harness />);
    const es = FakeES.instances[0];
    act(() => es.emit("board-snapshot", JSON.stringify({
      projects: [], epics: [{ id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [], stage: "escalated", attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" }],
    })));
    expect(useBoardAttention.getState().attention.size).toBe(1);
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["board"] });
  });

  it("debounces delta invalidations and fires attention callback", () => {
    const onAttention = vi.fn();
    render(<Harness onAttention={onAttention} />);
    const es = FakeES.instances[0];
    const d = (stage: string) => JSON.stringify({ project_id: "p1", epic_id: "e1", issue: 1, stage, needs: "", title: "t" });
    act(() => {
      es.emit("board", d("implementing"));
      es.emit("board", d("reviewing"));
      es.emit("board", d("escalated"));
    });
    expect(h.invalidateQueries).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(350); });
    expect(h.invalidateQueries).toHaveBeenCalledTimes(1);
    expect(onAttention).toHaveBeenCalledTimes(1);
    expect(onAttention.mock.calls[0][0]).toMatchObject({ stage: "escalated", epic_id: "e1" });
  });

  it("invalidates the board when the document becomes visible", () => {
    render(<Harness />);
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(h.invalidateQueries).not.toHaveBeenCalled();
    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["board"] });
  });
});
