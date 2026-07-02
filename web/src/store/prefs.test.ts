import { describe, it, expect, beforeEach } from "vitest";
import { usePrefs, PREFS_STORAGE_KEY } from "./prefs";

describe("prefs: windowSwitchShortcut", () => {
  beforeEach(() => {
    localStorage.clear();
    usePrefs.setState({ windowSwitchShortcut: "cmdCtrl" });
  });

  it("updates via the setter", () => {
    usePrefs.getState().setWindowSwitchShortcut("alt");
    expect(usePrefs.getState().windowSwitchShortcut).toBe("alt");
    usePrefs.getState().setWindowSwitchShortcut("off");
    expect(usePrefs.getState().windowSwitchShortcut).toBe("off");
  });

  it("persists the choice to localStorage", () => {
    usePrefs.getState().setWindowSwitchShortcut("alt");
    const raw = JSON.parse(localStorage.getItem(PREFS_STORAGE_KEY) || "{}");
    expect(raw.state.windowSwitchShortcut).toBe("alt");
  });
});
