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

// A synthetic mouse event that drives xterm's (mouse-only) SelectionService.
//   • detail:1 — xterm begins a FRESH selection only on a single click (`1 === event.detail`;
//     a synthetic MouseEvent defaults detail to 0). This is the path we always want.
//   • shiftKey — set ONLY when `force` (the running app has mouse tracking on). Then a plain
//     click would be forwarded to the PTY; on a non-Mac platform `shouldForceSelection` is
//     `shiftKey`, so shift keeps the click local. (With mouse tracking on, xterm's selection
//     service is disabled, so the shifted click still routes to the fresh single-click path,
//     not the extend path.) When mouse tracking is OFF we must NOT set shift: with the service
//     enabled, `_enabled && shiftKey` routes to "extend existing selection" — a no-op when no
//     selection is in progress, so the whole gesture would silently do nothing.
export function selectionMouseEvent(type: MouseKind, x: number, y: number, force: boolean): MouseEvent {
  return new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    detail: 1,
    shiftKey: force,
    clientX: x,
    clientY: y,
    button: 0,
    buttons: type === "mouseup" ? 0 : 1,
  });
}

export interface TerminalGestureDeps {
  // Dispatch a synthetic mouse event (mousedown at the point, move/up on document). `force`
  // sets shiftKey so the click stays local when the app has mouse tracking on.
  fireMouse(type: MouseKind, x: number, y: number, force: boolean): void;
  // Scroll the terminal's scrollback by n lines (positive = down/newer).
  scrollLines(n: number): void;
  // Current font size in px (for the swipe→lines cell math).
  fontSize(): number;
  // Whether the running app has mouse tracking on (a mouse-reporting TUI like Claude Code).
  // When on, a plain synthetic click is forwarded to the PTY, so we must force-select instead.
  mouseTracking(): boolean;
  // Whether the platform is Mac-class (incl. iPadOS, which reports as Mac). On Mac, xterm's
  // force-selection needs Alt + a Terminal option we don't set, and Alt column-selects — so
  // rather than inject clicks into the app, we skip the touch-select gesture there (Mac/iPad
  // users select with a real trackpad + keyboard). Only consulted when mouse tracking is on.
  isMac(): boolean;
  // TEMP diagnostic hook: trace the gesture pipeline so we can see WHERE mobile select breaks
  // on-device. Optional; remove once diagnosed.
  debug?(msg: string): void;
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
  let forcing = false; // does this select gesture carry shiftKey (mouse tracking was on)
  let lpTimer: ReturnType<typeof setTimeout> | null = null;

  const clearLp = () => { if (lpTimer !== null) { clearTimeout(lpTimer); lpTimer = null; } };
  // Release an in-flight selection so xterm tears down its document listeners + drag-scroll
  // interval (which it removes only on mouseup). Every abort path must go through this.
  const endSelect = () => { if (mode === "select") deps.fireMouse("mouseup", lastX, lastY, forcing); };

  const onStart = (e: Pick<TouchEvent, "touches">) => {
    if (e.touches.length !== 1) { endSelect(); mode = null; clearLp(); return; } // 2nd finger aborts
    const t = e.touches[0];
    startX = lastX = t.clientX; startY = lastY = t.clientY;
    mode = "pending";
    clearLp();
    deps.debug?.("touchstart");
    lpTimer = setTimeout(() => {
      lpTimer = null;
      if (mode !== "pending") return;          // already moved into a scroll
      const force = deps.mouseTracking();
      if (force && deps.isMac()) { deps.debug?.("longpress: SKIP (mac + mouse-tracking)"); return; }
      mode = "select"; forcing = force;
      deps.debug?.(`longpress: select force=${force}`);
      deps.fireMouse("mousedown", startX, startY, force); // begin a selection at the hold point
    }, LONG_PRESS_MS);
  };

  const onMove = (e: Pick<TouchEvent, "touches"> & { preventDefault(): void }) => {
    if (mode === null || e.touches.length !== 1) return;
    const t = e.touches[0];
    lastX = t.clientX; lastY = t.clientY;
    if (mode === "select") {
      deps.fireMouse("mousemove", t.clientX, t.clientY, forcing); // xterm extends the selection
      e.preventDefault();
      return;
    }
    if (mode === "pending") {
      if (Math.abs(t.clientY - startY) + Math.abs(t.clientX - startX) < MOVE_CANCEL) return;
      mode = "scroll"; clearLp(); deps.debug?.("scroll");  // moved before the hold fired → scroll
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
