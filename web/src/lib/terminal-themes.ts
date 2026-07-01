import type { ITheme } from "@xterm/xterm";

export type ThemeName = "dark" | "light" | "highContrast";

// Three minimum-contrast presets (§14.3). `dark` preserves the original
// hard-coded xterm colors so existing panes look identical by default.
export const TERMINAL_THEMES: Record<ThemeName, ITheme> = {
  dark: {
    background: "#111418",
    foreground: "#cdd6e0",
    cursor: "#cdd6e0",
    // A saturated blue at high alpha reads clearly against the near-black bg (the old
    // dark-slate #33415580 was nearly invisible). Kept translucent so the selected text's
    // own colors still show through; selectionInactiveBackground stays visible on blur.
    selectionBackground: "#3b82f6b3",
    selectionInactiveBackground: "#3b82f666",
  },
  light: {
    background: "#fbfbfd",
    foreground: "#1b1f24",
    cursor: "#1b1f24",
    selectionBackground: "#79b8ff",
    selectionInactiveBackground: "#79b8ff80",
  },
  highContrast: {
    background: "#000000",
    foreground: "#ffffff",
    cursor: "#ffff00",
    // Opaque white box with black text — maximally visible (a translucent white was faint,
    // and a near-white box needs a dark foreground or the white text vanishes into it).
    selectionBackground: "#ffffff",
    selectionForeground: "#000000",
    selectionInactiveBackground: "#ffffffb3",
  },
};

export const themeOf = (n: ThemeName): ITheme => TERMINAL_THEMES[n];
