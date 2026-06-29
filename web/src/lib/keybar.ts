// Pure key → byte encodings, inherited verbatim from the Phase 0.5 spike
// (spike-0.5/static/index.html). Single source of truth for the key bar and
// any keyboard shortcut path. No DOM, no xterm — fully unit-testable.

const encoder = new TextEncoder();

export function utf8(s: string): Uint8Array {
  return encoder.encode(s);
}

export const ESC = Uint8Array.of(0x1b); // lone escape, flushed immediately
export const TAB = Uint8Array.of(0x09);
export const ENTER = Uint8Array.of(0x0d); // CR submits
export const SOFT_NEWLINE = Uint8Array.of(0x0a); // LF inserts a newline WITHOUT submitting
export const SHIFT_TAB = utf8("\x1b[Z"); // ESC [ Z

export type ArrowDir = "up" | "down" | "left" | "right";
const ARROW_FINAL: Record<ArrowDir, string> = { up: "A", down: "B", right: "C", left: "D" };

// DECCKM application-cursor-keys mode swaps the CSI introducer ESC[ → ESCO.
export function arrow(dir: ArrowDir, appCursor: boolean): Uint8Array {
  return utf8("\x1b" + (appCursor ? "O" : "[") + ARROW_FINAL[dir]);
}

// Sticky Ctrl: the first char of `data` becomes a control byte; any remaining
// chars are appended literally (matches the spike's onData handler).
export function encodeCtrl(data: string): Uint8Array {
  const c = data.charCodeAt(0);
  let ctrl: number;
  if (c >= 97 && c <= 122) ctrl = c - 96; // a-z -> 0x01..0x1a
  else if (c >= 65 && c <= 90) ctrl = c - 64; // A-Z
  else ctrl = c & 0x1f;
  const rest = data.slice(1);
  if (!rest) return Uint8Array.of(ctrl);
  const tail = utf8(rest);
  const out = new Uint8Array(1 + tail.length);
  out[0] = ctrl;
  out.set(tail, 1);
  return out;
}

export type BarKey = "esc" | "tab" | "stab" | "up" | "down" | "left" | "right" | "nl" | "enter";
