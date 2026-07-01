import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { selectionMouseEvent, createTerminalGesture, type MouseKind } from "@/lib/terminal-touch";

// The handlers only read e.touches / call preventDefault, so we can drive them with plain
// fakes — no real xterm, canvas, or jsdom TouchEvent needed.
const touch = (x: number, y: number) => ({ touches: [{ clientX: x, clientY: y }] }) as any;
const twoFingers = () =>
  ({ touches: [{ clientX: 0, clientY: 0 }, { clientX: 9, clientY: 9 }] }) as any;
const move = (x: number, y: number) => ({ touches: [{ clientX: x, clientY: y }], preventDefault: vi.fn() }) as any;

describe("selectionMouseEvent", () => {
  it("sets detail:1 so xterm's SelectionService actually starts a selection", () => {
    // Regression: xterm gates single-click selection on `1 === event.detail`; a synthetic
    // MouseEvent defaults detail to 0, which silently begins no selection.
    expect(selectionMouseEvent("mousedown", 3, 4).detail).toBe(1);
  });

  it("carries the point and the primary button; mouseup drops buttons", () => {
    const down = selectionMouseEvent("mousedown", 5, 6);
    expect([down.clientX, down.clientY, down.button, down.buttons]).toEqual([5, 6, 0, 1]);
    expect(selectionMouseEvent("mouseup", 5, 6).buttons).toBe(0);
  });
});

describe("createTerminalGesture", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const setup = (over: Partial<Parameters<typeof createTerminalGesture>[0]> = {}) => {
    const fireMouse = vi.fn<(type: MouseKind, x: number, y: number) => void>();
    const scrollLines = vi.fn<(n: number) => void>();
    const g = createTerminalGesture({
      fireMouse, scrollLines, fontSize: () => 13, mouseTracking: () => false, ...over,
    });
    return { g, fireMouse, scrollLines };
  };

  it("long-press then drag selects: mousedown → mousemove → mouseup", () => {
    const { g, fireMouse, scrollLines } = setup();
    g.onStart(touch(10, 10));
    vi.advanceTimersByTime(450);
    expect(fireMouse).toHaveBeenCalledWith("mousedown", 10, 10);
    g.onMove(move(30, 40));
    expect(fireMouse).toHaveBeenCalledWith("mousemove", 30, 40);
    g.onEnd();
    expect(fireMouse).toHaveBeenCalledWith("mouseup", 30, 40);
    expect(scrollLines).not.toHaveBeenCalled();
  });

  it("a quick drag scrolls and never starts a selection", () => {
    const { g, fireMouse, scrollLines } = setup();
    g.onStart(touch(10, 100));
    g.onMove(move(10, 60));       // 40px before the hold fired → commits to scroll
    vi.advanceTimersByTime(450);  // long-press timer must have been cleared
    expect(scrollLines).toHaveBeenCalled();
    expect(fireMouse).not.toHaveBeenCalled();
  });

  it("releases the selection with a mouseup when a second finger interrupts it", () => {
    // Regression: xterm removes its document listeners + drag-scroll interval only on mouseup;
    // a 2nd finger that reset state without a mouseup left the selection drag stuck.
    const { g, fireMouse } = setup();
    g.onStart(touch(10, 10));
    vi.advanceTimersByTime(450);  // enters select (mousedown)
    fireMouse.mockClear();
    g.onStart(twoFingers());
    expect(fireMouse).toHaveBeenCalledWith("mouseup", 10, 10);
  });

  it("does not inject clicks when the app has mouse tracking on", () => {
    // Regression: with tmux/vim mouse mode, xterm forwards mouse events to the PTY, so the
    // bridge would send clicks to the app instead of selecting — suppress the select gesture.
    const { g, fireMouse } = setup({ mouseTracking: () => true });
    g.onStart(touch(10, 10));
    vi.advanceTimersByTime(450);
    expect(fireMouse).not.toHaveBeenCalled();
  });

  it("teardown cancels a pending long-press so it can't fire after unmount", () => {
    const { g, fireMouse } = setup();
    g.onStart(touch(10, 10));
    g.teardown();
    vi.advanceTimersByTime(450);
    expect(fireMouse).not.toHaveBeenCalled();
  });
});
