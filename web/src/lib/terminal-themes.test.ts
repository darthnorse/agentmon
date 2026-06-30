import { describe, it, expect } from "vitest";
import { TERMINAL_THEMES, themeOf, type ThemeName } from "@/lib/terminal-themes";

const NAMES: ThemeName[] = ["dark", "light", "highContrast"];

describe("terminal-themes", () => {
  it("defines all three presets", () => {
    for (const n of NAMES) expect(TERMINAL_THEMES[n]).toBeTruthy();
  });

  it("every preset has a background + foreground", () => {
    for (const n of NAMES) {
      expect(typeof TERMINAL_THEMES[n].background).toBe("string");
      expect(typeof TERMINAL_THEMES[n].foreground).toBe("string");
    }
  });

  it("dark preserves the current xterm colors", () => {
    expect(TERMINAL_THEMES.dark.background).toBe("#111418");
    expect(TERMINAL_THEMES.dark.foreground).toBe("#cdd6e0");
  });

  it("themeOf returns the matching preset object", () => {
    for (const n of NAMES) expect(themeOf(n)).toBe(TERMINAL_THEMES[n]);
  });
});
