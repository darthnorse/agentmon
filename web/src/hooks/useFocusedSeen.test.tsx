import { describe, it, expect, beforeEach, vi } from "vitest";
import { render } from "@testing-library/react";

// vi.hoisted: vi.mock is hoisted above plain consts, so the mock fn must be too.
const { postSeen } = vi.hoisted(() => ({ postSeen: vi.fn(async () => {}) }));
vi.mock("@/lib/api-client", () => ({ postSeen }));

import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
import type { SeenRequest } from "@/lib/contracts";

function Harness({ req }: { req: SeenRequest | null }) { useFocusedSeen(req); return null; }

describe("useFocusedSeen", () => {
  beforeEach(() => { postSeen.mockClear(); useSessionState.getState().reset(); });

  it("sets focusedKey, optimistically marks seen, and POSTs", () => {
    const req = { serverId: "s", target: "default", sessionName: "a" };
    render(<Harness req={req} />);
    const key = stateKey("s", "default", "a");
    expect(useSessionState.getState().focusedKey).toBe(key);
    expect(useSessionState.getState().seen.has(key)).toBe(true);
    expect(postSeen).toHaveBeenCalledWith(req);
  });

  it("clears focusedKey on unmount and when req is null", () => {
    const { rerender, unmount } = render(<Harness req={{ serverId: "s", target: "t", sessionName: "a" }} />);
    expect(useSessionState.getState().focusedKey).not.toBeNull();
    rerender(<Harness req={null} />);
    expect(useSessionState.getState().focusedKey).toBeNull();
    unmount();
    expect(useSessionState.getState().focusedKey).toBeNull();
  });

  it("swallows a POST failure", async () => {
    postSeen.mockRejectedValueOnce(new Error("boom"));
    expect(() => render(<Harness req={{ serverId: "s", target: "t", sessionName: "a" }} />)).not.toThrow();
  });
});
