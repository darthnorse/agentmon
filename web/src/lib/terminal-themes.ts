import type { ITheme } from "@xterm/xterm";

export type ThemeName = "dark" | "light" | "highContrast";

// Three minimum-contrast presets (§14.3). `dark` preserves the original
// hard-coded xterm colors so existing panes look identical by default.
export const TERMINAL_THEMES: Record<ThemeName, ITheme> = {
  dark: {
    background: "#111418",
    foreground: "#cdd6e0",
    cursor: "#cdd6e0",
    selectionBackground: "#33415580",
  },
  light: {
    background: "#fbfbfd",
    foreground: "#1b1f24",
    cursor: "#1b1f24",
    selectionBackground: "#b4d5fe",
  },
  highContrast: {
    background: "#000000",
    foreground: "#ffffff",
    cursor: "#ffff00",
    selectionBackground: "#ffffff40",
  },
};

export const themeOf = (n: ThemeName): ITheme => TERMINAL_THEMES[n];
