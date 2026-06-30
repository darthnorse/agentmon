import { describe, it, expect, beforeEach } from "vitest";
import { usePrefs, PREFS_STORAGE_KEY } from "@/store/prefs";

// Reset the persisted prefs + the live store to defaults before every test so
// cases don't bleed state through localStorage (the persist middleware writes there).
function resetPrefs() {
  localStorage.clear();
  usePrefs.setState({
    fontSizeDesktop: 13,
    fontSizeMobile: 10,
    terminalTheme: "dark",
    alertOnDone: false,
  });
}

describe("prefs store", () => {
  beforeEach(resetPrefs);

  it("has the documented defaults", () => {
    const s = usePrefs.getState();
    expect(s.fontSizeDesktop).toBe(13);
    expect(s.fontSizeMobile).toBe(10);
    expect(s.terminalTheme).toBe("dark");
    expect(s.alertOnDone).toBe(false);
  });

  it("exposes setters that update each field", () => {
    usePrefs.getState().setFontSizeDesktop(18);
    usePrefs.getState().setFontSizeMobile(14);
    usePrefs.getState().setTerminalTheme("light");
    usePrefs.getState().setAlertOnDone(true);
    const s = usePrefs.getState();
    expect(s.fontSizeDesktop).toBe(18);
    expect(s.fontSizeMobile).toBe(14);
    expect(s.terminalTheme).toBe("light");
    expect(s.alertOnDone).toBe(true);
  });

  it("persists a setter's value to localStorage under agentmon-prefs", () => {
    usePrefs.getState().setTerminalTheme("highContrast");
    const raw = localStorage.getItem(PREFS_STORAGE_KEY);
    expect(raw).toBeTruthy();
    expect(JSON.parse(raw!).state.terminalTheme).toBe("highContrast");
  });

  it("rehydrates persisted prefs from localStorage", async () => {
    // Simulate a previously-persisted session, then rehydrate (fresh page load).
    localStorage.setItem(
      PREFS_STORAGE_KEY,
      JSON.stringify({
        version: 0,
        state: { fontSizeDesktop: 22, fontSizeMobile: 16, terminalTheme: "light", alertOnDone: true },
      }),
    );
    await usePrefs.persist.rehydrate();
    const s = usePrefs.getState();
    expect(s.fontSizeDesktop).toBe(22);
    expect(s.fontSizeMobile).toBe(16);
    expect(s.terminalTheme).toBe("light");
    expect(s.alertOnDone).toBe(true);
  });
});
