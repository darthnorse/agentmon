// Terminal key overrides: raw bytes we send to the pty in place of xterm.js's
// default output for specific key chords. Kept as pure logic (no xterm/DOM deps)
// so it is unit-testable; XTerm.tsx wires it via attachCustomKeyEventHandler.

/** The minimal shape of a KeyboardEvent this module inspects. */
export interface KeyEventLike {
  type: string;
  key: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  metaKey: boolean;
}

// A bare LF (0x0a). This is the byte Claude Code's input parser treats as a soft
// newline — it is exactly what Ctrl+J (the default `chat:newline` binding) sends.
// CR (0x0d, what Enter sends) submits; LF inserts a newline. No terminal-protocol
// negotiation (Kitty / modifyOtherKeys / extended-keys) is required.
export const SOFT_NEWLINE = "\n";

// keyOverride returns the raw bytes to send in place of xterm.js's default for a
// key event, or null to let xterm handle the key normally.
//
// Shift+Enter → SOFT_NEWLINE: xterm.js emits CR (\r) for BOTH plain and shifted
// Enter, so at the pty the two are otherwise indistinguishable and Claude Code
// submits on both. We claim ONLY the Shift-exact chord: plain Enter still submits,
// and Enter carrying any other modifier (Ctrl/Alt/Meta) is left to xterm so those
// bindings keep working.
export function keyOverride(ev: KeyEventLike): string | null {
  if (
    ev.type === "keydown" &&
    ev.key === "Enter" &&
    ev.shiftKey &&
    !ev.ctrlKey &&
    !ev.altKey &&
    !ev.metaKey
  ) {
    return SOFT_NEWLINE;
  }
  return null;
}
