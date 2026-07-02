import { describe, it, expect } from "vitest";
import { windowIndexFor, chordLabel } from "./window-shortcuts";

type Ev = Parameters<typeof windowIndexFor>[0];

function ev(over: Partial<Ev> = {}): Ev {
  return { code: "Digit1", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...over };
}

describe("windowIndexFor", () => {
  it("maps Alt+Digit1..9 to 1..9", () => {
    for (let n = 1; n <= 9; n++) {
      expect(windowIndexFor(ev({ code: `Digit${n}`, altKey: true }), "alt")).toBe(n);
    }
  });

  it("reads code, not key — a mangled Mac ⌥1 key is irrelevant", () => {
    // ShortcutKeyEvent has no `key`; only `code` is inspected. Alt+Digit1 → 1.
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "alt")).toBe(1);
  });

  it("requires Alt alone and rejects other modifiers", () => {
    expect(windowIndexFor(ev({ code: "Digit1" }), "alt")).toBeNull(); // no modifier
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true }), "alt")).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", metaKey: true }), "alt")).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true, shiftKey: true }), "alt")).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true, ctrlKey: true }), "alt")).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true, metaKey: true }), "alt")).toBeNull();
  });

  it("returns null for Digit0 and non-digit codes", () => {
    expect(windowIndexFor(ev({ code: "Digit0", altKey: true }), "alt")).toBeNull();
    expect(windowIndexFor(ev({ code: "KeyA", altKey: true }), "alt")).toBeNull();
  });

  it("off scheme is always null", () => {
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "off")).toBeNull();
  });
});

describe("chordLabel", () => {
  it("formats alt per platform and is empty for off", () => {
    expect(chordLabel("alt", true, 2)).toBe("⌥2");
    expect(chordLabel("alt", false, 2)).toBe("Alt+2");
    expect(chordLabel("off", false, 1)).toBe("");
    expect(chordLabel("off", true, 1)).toBe("");
  });
});
