import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

// Capture the handlers passed to TerminalSocket so we can drive onState directly.
let captured: { onState?: (f: { session: string; state: string }) => void } | null = null;
const retryNowSpy = vi.fn();
const sendSpy = vi.fn();
const resizeSpy = vi.fn();
vi.mock("@/lib/ws-terminal", async (orig) => {
  const mod = (await orig()) as object;
  return {
    ...mod,
    TerminalSocket: class {
      constructor(_t: unknown, handlers: typeof captured) {
        captured = handlers;
      }
      open() {}
      dispose() {}
      send(...a: unknown[]) { sendSpy(...a); }
      resize(...a: unknown[]) { resizeSpy(...a); }
      retryNow = retryNowSpy;
    },
  };
});

import { useTerminalSession } from "@/hooks/useTerminalSession";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
import { kickReconnect } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";

const target = { serverId: "s", target: "default", paneId: "%1" };
const key = stateKey("s", "default", "api");

describe("useTerminalSession onState gating (M11)", () => {
  beforeEach(() => {
    captured = null;
    useSessionState.getState().reset();
  });

  it("applies the {t:state} frame only for the FOCUSED pane (no SSE-alert pre-emption)", () => {
    renderHook(() => useTerminalSession(target));
    expect(typeof captured?.onState).toBe("function");

    // NOT focused → must NOT write the store; otherwise it pre-empts the SSE alert
    // gate (which reads the prior state from the store) and the alert is dropped.
    useSessionState.getState().setFocusedKey(null);
    captured!.onState!({ session: "api", state: "blocked" });
    expect(useSessionState.getState().live.get(key)).toBeUndefined();

    // Focused → the dot tracks the live terminal-WS state (alerts are suppressed for
    // the focused pane anyway, so this is safe).
    useSessionState.getState().setFocusedKey(key);
    captured!.onState!({ session: "api", state: "blocked" });
    expect(useSessionState.getState().live.get(key)).toBe("blocked");
  });
});

describe("useTerminalSession read-only (watch) mode", () => {
  beforeEach(() => { captured = null; sendSpy.mockClear(); resizeSpy.mockClear(); });

  it("forwards input and resize by default", () => {
    const { result } = renderHook(() => useTerminalSession(target));
    result.current.handleData("hi");
    result.current.handleResize(80, 24);
    expect(sendSpy).toHaveBeenCalled();
    expect(resizeSpy).toHaveBeenCalledWith(80, 24);
  });

  it("suppresses input and resize when readOnly, so a preview never touches the live pane", () => {
    const { result } = renderHook(() => useTerminalSession(target, { readOnly: true }));
    result.current.handleData("hi");
    result.current.handleResize(80, 24);
    expect(sendSpy).not.toHaveBeenCalled();
    expect(resizeSpy).not.toHaveBeenCalled();
  });
});

describe("useTerminalSession reconnect kick", () => {
  it("a kick for this pane's identity calls retryNow; unmount unsubscribes", () => {
    retryNowSpy.mockClear();
    const { unmount } = renderHook(() => useTerminalSession(target));
    kickReconnect(paneIdentity(target.serverId, target.target, target.paneId));
    expect(retryNowSpy).toHaveBeenCalledTimes(1);
    kickReconnect(paneIdentity("other", "default", "%9"));
    expect(retryNowSpy).toHaveBeenCalledTimes(1); // different pane → not ours
    unmount();
    kickReconnect(paneIdentity(target.serverId, target.target, target.paneId));
    expect(retryNowSpy).toHaveBeenCalledTimes(1); // unsubscribed
  });
});
