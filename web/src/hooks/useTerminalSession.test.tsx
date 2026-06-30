import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

// Capture the handlers passed to TerminalSocket so we can drive onState directly.
let captured: { onState?: (f: { session: string; state: string }) => void } | null = null;
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
      send() {}
      resize() {}
    },
  };
});

import { useTerminalSession } from "@/hooks/useTerminalSession";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";

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
