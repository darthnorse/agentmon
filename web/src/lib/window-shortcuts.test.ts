import { describe, it, expect } from "vitest";
import { windowIndexFor, chordLabel } from "./window-shortcuts";

type Ev = Parameters<typeof windowIndexFor>[0];

function ev(over: Partial<Ev> = {}): Ev {
  return { code: "Digit1", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...over };
}

describe("windowIndexFor", () => {
  it("maps Digit1..9 to 1..9 for the matching modifier", () => {
    for (let n = 1; n <= 9; n++) {
      expect(windowIndexFor(ev({ code: `Digit${n}`, ctrlKey: true }), "cmdCtrl", false)).toBe(n);
    }
  });

  it("cmdCtrl uses metaKey on Mac and ctrlKey off Mac", () => {
    expect(windowIndexFor(ev({ code: "Digit2", metaKey: true }), "cmdCtrl", true)).toBe(2);
    expect(windowIndexFor(ev({ code: "Digit2", ctrlKey: true }), "cmdCtrl", true)).toBeNull(); // Ctrl left for the terminal on Mac
    expect(windowIndexFor(ev({ code: "Digit2", ctrlKey: true }), "cmdCtrl", false)).toBe(2);
    expect(windowIndexFor(ev({ code: "Digit2", metaKey: true }), "cmdCtrl", false)).toBeNull();
  });

  it("alt uses altKey on both platforms and reads code, not key (Mac ⌥1)", () => {
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "alt", true)).toBe(1);
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "alt", false)).toBe(1);
    expect(windowIndexFor(ev({ code: "Digit1", metaKey: true }), "alt", true)).toBeNull();
  });

  it("rejects extra modifiers on the chord", () => {
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true, shiftKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true, altKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true, shiftKey: true }), "alt", false)).toBeNull();
  });

  it("returns null for Digit0, non-digit codes, and no modifier", () => {
    expect(windowIndexFor(ev({ code: "Digit0", ctrlKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "KeyA", ctrlKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1" }), "cmdCtrl", false)).toBeNull();
  });

  it("off scheme is always null", () => {
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true }), "off", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "off", true)).toBeNull();
  });
});

describe("chordLabel", () => {
  it("formats per scheme and platform", () => {
    expect(chordLabel("cmdCtrl", true, 1)).toBe("⌘1");
    expect(chordLabel("cmdCtrl", false, 1)).toBe("Ctrl+1");
    expect(chordLabel("alt", true, 2)).toBe("⌥2");
    expect(chordLabel("alt", false, 2)).toBe("Alt+2");
    expect(chordLabel("off", false, 1)).toBe("");
  });
});
