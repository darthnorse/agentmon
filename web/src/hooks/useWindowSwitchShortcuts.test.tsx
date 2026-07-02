import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, cleanup } from "@testing-library/react";
import { useWindowSwitchShortcuts } from "./useWindowSwitchShortcuts";
import { usePrefs } from "@/store/prefs";
import { usePanes } from "@/store/panes";

function pane(paneId: string) {
  return { id: `s:t:sess:${paneId}`, serverId: "s", paneId, target: "t", session: "sess", serverName: "srv" };
}

function press(over: Partial<KeyboardEventInit> = {}) {
  const ev = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...over });
  document.dispatchEvent(ev);
  return ev;
}

describe("useWindowSwitchShortcuts", () => {
  beforeEach(() => {
    localStorage.clear();
    usePrefs.setState({ windowSwitchShortcut: "cmdCtrl" });
    usePanes.setState({ panes: [pane("p1"), pane("p2"), pane("p3")], focusedId: null });
    document.body.innerHTML = "";
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
  });
  afterEach(() => cleanup()); // unmount the hook → remove its document listener between tests

  it("focuses the Nth tile and consumes the event (Ctrl+2, non-Mac jsdom)", () => {
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit2", ctrlKey: true });
    expect(onFocus).toHaveBeenCalledWith("s:t:sess:p2");
    expect(ev.defaultPrevented).toBe(true);
  });

  it("does nothing when the scheme is off", () => {
    usePrefs.setState({ windowSwitchShortcut: "off" });
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit2", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("consumes but no-ops a number past the open tile count", () => {
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit8", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(true);
  });

  it("ignores the chord while a non-terminal input is focused (and lets the key through)", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    input.focus();
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit1", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false); // the input must still receive the keystroke
  });

  it("ignores the chord while a native <select> is focused", () => {
    const select = document.createElement("select");
    document.body.appendChild(select);
    select.focus();
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit1", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("moves the expanded tile when one is expanded", () => {
    usePanes.setState({ focusedId: "s:t:sess:p1" });
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    press({ code: "Digit3", ctrlKey: true });
    expect(usePanes.getState().focusedId).toBe("s:t:sess:p3");
  });
});
