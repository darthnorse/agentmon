import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ThemeName } from "@/lib/terminal-themes";
import type { ShortcutScheme } from "@/lib/window-shortcuts";

export const PREFS_STORAGE_KEY = "agentmon-prefs";

// Per-device UI prefs persisted to localStorage (v1 is single-user, one device at
// a time — §11.7 — so per-device is sufficient; a hub prefs table is a later add).
export interface PrefsState {
  fontSizeDesktop: number;
  fontSizeMobile: number;
  terminalTheme: ThemeName;
  alertOnDone: boolean;
  gridMaxColumns: number;
  windowSwitchShortcut: ShortcutScheme;
  setFontSizeDesktop(n: number): void;
  setFontSizeMobile(n: number): void;
  setTerminalTheme(t: ThemeName): void;
  setAlertOnDone(v: boolean): void;
  setGridMaxColumns(n: number): void;
  setWindowSwitchShortcut(v: ShortcutScheme): void;
}

export const usePrefs = create<PrefsState>()(
  persist(
    (set) => ({
      fontSizeDesktop: 13,
      fontSizeMobile: 10,
      terminalTheme: "dark",
      alertOnDone: false,
      gridMaxColumns: 3,
      windowSwitchShortcut: "cmdCtrl",
      setFontSizeDesktop: (n) => set({ fontSizeDesktop: n }),
      setFontSizeMobile: (n) => set({ fontSizeMobile: n }),
      setTerminalTheme: (t) => set({ terminalTheme: t }),
      setAlertOnDone: (v) => set({ alertOnDone: v }),
      setGridMaxColumns: (n) => set({ gridMaxColumns: Math.max(1, Math.min(4, Math.floor(n))) }),
      setWindowSwitchShortcut: (v) => set({ windowSwitchShortcut: v }),
    }),
    {
      name: PREFS_STORAGE_KEY,
      // Persist only the data fields, never the setters.
      partialize: (s) => ({
        fontSizeDesktop: s.fontSizeDesktop,
        fontSizeMobile: s.fontSizeMobile,
        terminalTheme: s.terminalTheme,
        alertOnDone: s.alertOnDone,
        gridMaxColumns: s.gridMaxColumns,
        windowSwitchShortcut: s.windowSwitchShortcut,
      }),
    },
  ),
);
