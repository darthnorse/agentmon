import { describe, it, expect, vi } from "vitest";
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
// sessions on one server so switching tabs has a real second pane to focus.
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
import { stateKey } from "@/lib/state";

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
    render(<MobileTerminalRoute />);
    await userEvent.click(screen.getByText("beta")); // the inactive tab
    expect(navigateSpy).not.toHaveBeenCalled(); // in-state focus, not a route change
    // both panes are now mounted (keep-alive): two mocked terminals present
    expect(screen.getAllByTestId(/^tv/).length).toBeGreaterThanOrEqual(2);
  });
});
