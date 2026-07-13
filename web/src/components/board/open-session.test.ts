import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  createSession: vi.fn(), listSessions: vi.fn(), setQueryData: vi.fn(), invalidateQueries: vi.fn(), toast: Object.assign(vi.fn(), { error: vi.fn() }),
}));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, createSession: h.createSession, listSessions: h.listSessions };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { setQueryData: h.setQueryData, invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: h.toast }));

import { openOrFocusSession, openPaneTail, TILE_CAP_TOAST } from "@/components/board/open-session";
import { ApiError } from "@/lib/api-client";
import type { Session } from "@/lib/contracts";
import { paneKey, usePanes } from "@/store/panes";

const session = { name: "plan-school", server: "h1", target: "default", cwd: "/w", command: "", windows: [{ id: "w1", index: "0", name: "", panes: [{ id: "pane1", command: "", cwd: "/w" }] }] } as Session;

describe("openOrFocusSession", () => {
  beforeEach(() => {
    h.createSession.mockReset(); h.listSessions.mockReset(); h.setQueryData.mockReset(); h.invalidateQueries.mockReset(); h.toast.mockReset(); h.toast.error.mockReset();
    usePanes.setState({ panes: [], focusedId: null });
  });

  it("mobile: creates then navigates to /t", async () => {
    h.createSession.mockResolvedValue(session);
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school", cwd: "/w", command: 'claude "/plan-epics"' }, false, navigate);
    expect(h.createSession).toHaveBeenCalledWith("h1", { name: "plan-school", cwd: "/w", command: 'claude "/plan-epics"' }, "");
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: "/t/$serverId/$paneId" }));
  });

  it("treats an existing-session 409 as success and opens the re-listed session", async () => {
    h.createSession.mockRejectedValue(new ApiError(409, "session already exists"));
    h.listSessions.mockResolvedValue([session]);
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school" }, true, navigate);
    expect(h.listSessions).toHaveBeenCalledWith("h1", undefined);
    expect(navigate).toHaveBeenCalled();
  });

  it("a failed 409 re-list still navigates home rather than throwing", async () => {
    h.createSession.mockRejectedValue(new ApiError(409, "session already exists"));
    h.listSessions.mockRejectedValue(new Error("network"));
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school" }, true, navigate);
    expect(navigate).toHaveBeenCalledWith({ to: "/" });
  });

  it("opens a supplied existing session without trying to create it", async () => {
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "host", target: "", name: session.name, session }, true, navigate);
    expect(h.createSession).not.toHaveBeenCalled();
    // Default (glance) expands: the pane is open AND focused.
    expect(usePanes.getState().focusedId).toBe(paneKey("h1", "default", session.name, "pane1"));
    expect(navigate).toHaveBeenCalledWith({ to: "/" });
  });

  it("keeps the current view and toasts when the desktop tile cap is reached", async () => {
    usePanes.setState({
      panes: Array.from({ length: 6 }, (_, i) => ({
        id: `h1:default:s${i}:p${i}`, serverId: "h1", paneId: `p${i}`, target: "default",
        session: `s${i}`, serverName: "host",
      })),
      focusedId: null,
    });
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "host", target: "", name: session.name, session }, true, navigate);
    expect(h.toast).toHaveBeenCalledWith(TILE_CAP_TOAST);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("toasts and stops (no silent home) when session creation fails outright", async () => {
    h.createSession.mockRejectedValue(new ApiError(500, "host offline"));
    const navigate = vi.fn();
    await openOrFocusSession({ serverId: "h1", serverName: "h1", target: "", name: "plan-school", command: "x" }, true, navigate);
    expect(h.toast.error).toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("openPaneTail", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("mobile navigates to the terminal route and returns opened", () => {
    const navigate = vi.fn();
    const r = openPaneTail({ serverId: "h1", serverName: "host", target: "default", session: "s", paneId: "p1" }, false, navigate);
    expect(r).toBe("opened");
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: "/t/$serverId/$paneId" }));
  });

  it("desktop opens+focuses (expands) the tile by default, returns opened", () => {
    const navigate = vi.fn();
    const r = openPaneTail({ serverId: "h1", serverName: "host", target: "default", session: "s", paneId: "p1" }, true, navigate);
    expect(r).toBe("opened");
    expect(usePanes.getState().focusedId).toBe(paneKey("h1", "default", "s", "p1"));
  });

  it("desktop with expand=false opens into the grid without focusing (launch, not glance)", () => {
    const navigate = vi.fn();
    const r = openPaneTail({ serverId: "h1", serverName: "host", target: "default", session: "s", paneId: "p1" }, true, navigate, false);
    expect(r).toBe("opened");
    expect(usePanes.getState().panes.map((p) => p.id)).toContain(paneKey("h1", "default", "s", "p1"));
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("desktop expand=false collapses to grid even when the pane is already open", () => {
    // Pane s/p1 is already open; a DIFFERENT tile is currently expanded (focused).
    usePanes.setState({
      panes: [{ id: paneKey("h1", "default", "s", "p1"), serverId: "h1", paneId: "p1", target: "default", session: "s", serverName: "host" }],
      focusedId: paneKey("h1", "default", "other", "pX"),
    });
    const navigate = vi.fn();
    const r = openPaneTail({ serverId: "h1", serverName: "host", target: "default", session: "s", paneId: "p1" }, true, navigate, false);
    expect(r).toBe("opened");
    // Re-launching an already-open session must reveal it (collapse to grid), not
    // leave it hidden behind the other expanded tile.
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("desktop returns cap (no focus) when the tile grid is full", () => {
    usePanes.setState({
      panes: Array.from({ length: 6 }, (_, i) => ({ id: `h1:default:s${i}:p${i}`, serverId: "h1", paneId: `p${i}`, target: "default", session: `s${i}`, serverName: "host" })),
      focusedId: null,
    });
    const navigate = vi.fn();
    const r = openPaneTail({ serverId: "h1", serverName: "host", target: "default", session: "new", paneId: "pN" }, true, navigate);
    expect(r).toBe("cap");
    expect(usePanes.getState().focusedId).toBeNull();
  });
});
