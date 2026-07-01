import { describe, it, expect } from "vitest";
import { keyOverride, SOFT_NEWLINE } from "./terminal-keys";

type KeyEventLike = Parameters<typeof keyOverride>[0];

function ev(over: Partial<KeyEventLike> = {}): KeyEventLike {
  return {
    type: "keydown",
    key: "Enter",
    shiftKey: false,
    ctrlKey: false,
    altKey: false,
    metaKey: false,
    ...over,
  };
}

describe("keyOverride", () => {
  it("maps Shift+Enter to a soft newline (bare LF)", () => {
    expect(keyOverride(ev({ shiftKey: true }))).toBe(SOFT_NEWLINE);
    expect(SOFT_NEWLINE).toBe("\n"); // Claude Code's chat:newline (Ctrl+J = 0x0a)
  });

  it("leaves plain Enter to xterm so it still submits (CR)", () => {
    expect(keyOverride(ev())).toBeNull();
  });

  it("does not hijack Enter carrying another modifier alongside Shift", () => {
    expect(keyOverride(ev({ shiftKey: true, ctrlKey: true }))).toBeNull();
    expect(keyOverride(ev({ shiftKey: true, altKey: true }))).toBeNull();
    expect(keyOverride(ev({ shiftKey: true, metaKey: true }))).toBeNull();
  });

  it("leaves other Enter chords (Ctrl/Alt/Meta+Enter) to xterm", () => {
    expect(keyOverride(ev({ ctrlKey: true }))).toBeNull();
    expect(keyOverride(ev({ altKey: true }))).toBeNull();
    expect(keyOverride(ev({ metaKey: true }))).toBeNull();
  });

  it("only fires on keydown (not keyup/keypress)", () => {
    expect(keyOverride(ev({ shiftKey: true, type: "keyup" }))).toBeNull();
    expect(keyOverride(ev({ shiftKey: true, type: "keypress" }))).toBeNull();
  });

  it("ignores non-Enter keys, incl. Ctrl+J which xterm already sends as 0x0a", () => {
    expect(keyOverride(ev({ key: "a", shiftKey: true }))).toBeNull();
    expect(keyOverride(ev({ key: "j", ctrlKey: true }))).toBeNull();
  });
});
