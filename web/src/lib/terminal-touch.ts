// Touch gestures for the terminal host. Two gestures share one finger:
//   • a quick vertical drag scrolls the scrollback (and swallows the page scroll);
//   • a long-press (~450ms held still) then drag SELECTS text.
// xterm's selection is mouse-only (it binds no touch/pointer listeners), and the scroll
// preventDefault otherwise suppresses the mouse events iOS would synthesize — so drag-select
// never reaches xterm. We bridge the long-press drag to synthetic mouse events
// (mousedown → mousemove* → mouseup) that drive xterm's SelectionService; the key bar's Copy
// (getSelection) then copies it. Extracted from XTerm so the state machine is unit-testable
// without a real xterm/canvas (the handlers only read e.touches).

export type MouseKind = "mousedown" | "mousemove" | "mouseup";

// xterm's SelectionService starts a selection ONLY when `1 === event.detail` (single click).
// A synthetic MouseEvent defaults detail to 0, so without detail:1 the whole bridge dispatches
// events that never begin a selection — the feature would be a silent no-op on every platform.
export function selectionMouseEvent(type: MouseKind, x: number, y: number): MouseEvent {
  return new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    detail: 1,
    clientX: x,
    clientY: y,
    button: 0,
    buttons: type === "mouseup" ? 0 : 1,
  });
}

export interface TerminalGestureDeps {
  // Dispatch a synthetic mouse event (mousedown at the point, move/up on document).
  fireMouse(type: MouseKind, x: number, y: number): void;
  // Scroll the terminal's scrollback by n lines (positive = down/newer).
  scrollLines(n: number): void;
  // Current font size in px (for the swipe→lines cell math).
  fontSize(): number;
  // Whether the running app has mouse tracking on (tmux mouse mode, vim, htop…). When it is,
  // xterm forwards mouse events to the PTY and disables its own selection, so bridging would
  // inject clicks into the app instead of selecting — we suppress the select gesture there.
  mouseTracking(): boolean;
}

const LONG_PRESS_MS = 450;
const MOVE_CANCEL = 10; // px of travel that turns a still-pending hold into a scroll
const SCROLL_MIN = 6;   // px before a scroll drag registers

export interface TerminalGesture {
  onStart(e: Pick<TouchEvent, "touches">): void;
  onMove(e: Pick<TouchEvent, "touches"> & { preventDefault(): void }): void;
  onEnd(): void;
  teardown(): void;
}

export function createTerminalGesture(deps: TerminalGestureDeps): TerminalGesture {
  let startX = 0, startY = 0, lastX = 0, lastY = 0;
  let mode: "pending" | "scroll" | "select" | null = null;
  let lpTimer: ReturnType<typeof setTimeout> | null = null;

  const clearLp = () => { if (lpTimer !== null) { clearTimeout(lpTimer); lpTimer = null; } };
  // Release an in-flight selection so xterm tears down its document listeners + drag-scroll
  // interval (which it removes only on mouseup). Every abort path must go through this.
  const endSelect = () => { if (mode === "select") deps.fireMouse("mouseup", lastX, lastY); };

  const onStart = (e: Pick<TouchEvent, "touches">) => {
    if (e.touches.length !== 1) { endSelect(); mode = null; clearLp(); return; } // 2nd finger aborts
    const t = e.touches[0];
    startX = lastX = t.clientX; startY = lastY = t.clientY;
    mode = "pending";
    clearLp();
    lpTimer = setTimeout(() => {
      lpTimer = null;
      if (mode !== "pending") return;      // already moved into a scroll
      if (deps.mouseTracking()) return;    // app owns the mouse — don't inject clicks into it
      mode = "select";
      deps.fireMouse("mousedown", startX, startY); // begin an xterm selection at the hold point
    }, LONG_PRESS_MS);
  };

  const onMove = (e: Pick<TouchEvent, "touches"> & { preventDefault(): void }) => {
    if (mode === null || e.touches.length !== 1) return;
    const t = e.touches[0];
    lastX = t.clientX; lastY = t.clientY;
    if (mode === "select") {
      deps.fireMouse("mousemove", t.clientX, t.clientY); // xterm extends the selection
      e.preventDefault();
      return;
    }
    if (mode === "pending") {
      if (Math.abs(t.clientY - startY) + Math.abs(t.clientX - startX) < MOVE_CANCEL) return;
      mode = "scroll"; clearLp();                        // moved before the hold fired → scroll
    }
    const dy = startY - t.clientY;
    const cell = (deps.fontSize() || 13) * 1.2;
    if (Math.abs(dy) > SCROLL_MIN) {
      const lines = Math.trunc(dy / cell);
      if (lines !== 0) { deps.scrollLines(lines); startY = t.clientY; }
    }
    e.preventDefault();
  };

  const onEnd = () => { clearLp(); endSelect(); mode = null; };

  return { onStart, onMove, onEnd, teardown: clearLp };
}
