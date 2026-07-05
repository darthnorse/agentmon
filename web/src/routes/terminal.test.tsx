import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => (
    <div data-testid={`tv-${p.paneId}`}>{`${p.serverId}:${p.paneId}:${p.target}:${String(p.showKeyBar)}`}</div>
  ),
}));
// Stub the rename editor — its query-client/panes deps are irrelevant to the route
// test, and stubbing it keeps the minimal react-router mock from needing the
// router's exports (the real editor → query-client → @/router).
vi.mock("@/components/SessionNameEditor", () => ({
  SessionNameEditor: (p: any) => <span>{p.name}</span>,
}));

// vi.hoisted: vi.mock is hoisted above plain consts, so mock fns referenced inside a
// vi.mock factory must be too.
const { navigateSpy } = vi.hoisted(() => ({ navigateSpy: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ serverId: "s1", paneId: "%0" }),
  useSearch: () => ({ target: "default", session: "alpha" }),
  useNavigate: () => navigateSpy,
}));
// The header tabs + eager-warm fetch the session list via react-query; stub two
// sessions on one server so a second pane exists to open/switch to.
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [{ id: "s1", name: "host-1", labels: [], enabled: true }] }),
  useQueries: () => [{
    data: [
      { name: "alpha", server: "s1", target: "default", cwd: "/a", command: "c", windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] },
      { name: "beta", server: "s1", target: "default", cwd: "/b", command: "c", windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "c", cwd: "/b" }] }] },
    ],
  }],
}));

const { postSeen } = vi.hoisted(() => ({ postSeen: vi.fn(async () => {}) }));
vi.mock("@/lib/api-client", () => ({
  postSeen, listServers: vi.fn(), listSessions: vi.fn(),
  serversKey: () => ["servers"], sessionsKey: (id: string) => ["sessions", id],
}));

import { MobileTerminalRoute } from "@/routes/terminal";
import { useSessionState } from "@/store/session-state";
import { useMobileOpenTabs } from "@/store/mobile-open-tabs";
import { stateKey } from "@/lib/state";

// The mobile tab bar is driven by the explicit, persisted open set (NOT the full live-session
// list). The entered pane (%0/alpha, from the URL params) is added to the set on mount; any OTHER
// tab must be opened explicitly. localStorage persists the set, so reset it between tests.
const beta = { serverId: "s1", target: "default", paneId: "%1" };

beforeEach(() => {
  localStorage.clear();
  useMobileOpenTabs.setState({ open: [] });
});

describe("MobileTerminalRoute", () => {
  it("passes params/search into a key-bar TerminalView and shows the session header", () => {
    render(<MobileTerminalRoute />);
    expect(screen.getByTestId("tv-%0")).toHaveTextContent("s1:%0:default:true");
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it("marks the opened session seen/focused on mount", () => {
    useSessionState.getState().reset();
    postSeen.mockClear();
    render(<MobileTerminalRoute />);
    expect(useSessionState.getState().focusedKey).toBe(stateKey("s1", "default", "alpha"));
    expect(postSeen).toHaveBeenCalledWith({ serverId: "s1", target: "default", sessionName: "alpha" });
  });

  it("switches tabs in-state without navigating", async () => {
    navigateSpy.mockClear();
    useMobileOpenTabs.getState().add(beta); // beta opened earlier → in the tab bar
    render(<MobileTerminalRoute />); // mount adds alpha (%0), focused; open set = [beta, alpha]
    await userEvent.click(screen.getByText("beta")); // the inactive tab
    expect(navigateSpy).not.toHaveBeenCalled(); // in-state focus, not a route change
    // both panes are mounted (keep-alive): two mocked terminals present
    expect(screen.getAllByTestId(/^tv/).length).toBeGreaterThanOrEqual(2);
  });

  it("closing the active tab with another open focuses a neighbor, no navigation", async () => {
    navigateSpy.mockClear();
    useMobileOpenTabs.getState().add(beta); // a second open tab to fall back to
    render(<MobileTerminalRoute />); // open set = [beta, alpha]; alpha (%0) is active
    await userEvent.click(screen.getByRole("button", { name: "Close alpha" }));
    expect(navigateSpy).not.toHaveBeenCalled(); // neighbor exists → stay in the route
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument(); // beta (the neighbor) still mounted
    expect(screen.queryByTestId("tv-%0")).toBeNull(); // alpha (the closed pane) unmounted → socket freed
  });

  it("closing the last open tab navigates home", async () => {
    navigateSpy.mockClear();
    render(<MobileTerminalRoute />); // open set = [alpha] only (just the entered pane)
    await userEvent.click(screen.getByRole("button", { name: "Close alpha" }));
    expect(navigateSpy).toHaveBeenCalledWith({ to: "/" });
  });

  it("does not warm a stale open-set entry that is not a live session", () => {
    // %stale is persisted in the open set but absent from the live rows (%0/%1). It must NOT
    // be warmed into the pool — no hidden TerminalView, no relay socket for a dead pane.
    useMobileOpenTabs.getState().add({ serverId: "s1", target: "default", paneId: "%stale" });
    render(<MobileTerminalRoute />);
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument(); // entered pane still seeded
    expect(screen.queryByTestId("tv-%stale")).toBeNull(); // stale entry skipped (no socket)
  });
});
