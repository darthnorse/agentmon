import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ThemeName } from "@/lib/terminal-themes";

export const PREFS_STORAGE_KEY = "agentmon-prefs";

// Per-device UI prefs persisted to localStorage (v1 is single-user, one device at
// a time — §11.7 — so per-device is sufficient; a hub prefs table is a later add).
export interface PrefsState {
  fontSizeDesktop: number;
  fontSizeMobile: number;
  terminalTheme: ThemeName;
  alertOnDone: boolean;
  setFontSizeDesktop(n: number): void;
  setFontSizeMobile(n: number): void;
  setTerminalTheme(t: ThemeName): void;
  setAlertOnDone(v: boolean): void;
}

export const usePrefs = create<PrefsState>()(
  persist(
    (set) => ({
      fontSizeDesktop: 13,
      fontSizeMobile: 10,
      terminalTheme: "dark",
      alertOnDone: false,
      setFontSizeDesktop: (n) => set({ fontSizeDesktop: n }),
      setFontSizeMobile: (n) => set({ fontSizeMobile: n }),
      setTerminalTheme: (t) => set({ terminalTheme: t }),
      setAlertOnDone: (v) => set({ alertOnDone: v }),
    }),
    {
      name: PREFS_STORAGE_KEY,
      // Persist only the data fields, never the setters.
      partialize: (s) => ({
        fontSizeDesktop: s.fontSizeDesktop,
        fontSizeMobile: s.fontSizeMobile,
        terminalTheme: s.terminalTheme,
        alertOnDone: s.alertOnDone,
      }),
    },
  ),
);
