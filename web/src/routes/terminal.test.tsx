import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid="tv">{`${p.serverId}:${p.paneId}:${p.target}:${String(p.showKeyBar)}`}</div>,
}));
// Stub the rename editor — its query-client/panes deps are irrelevant to the route
// test, and stubbing it keeps the minimal react-router mock from needing the
// router's exports (the real editor → query-client → @/router).
vi.mock("@/components/SessionNameEditor", () => ({
  SessionNameEditor: (p: any) => <span>{p.name}</span>,
}));
vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ serverId: "s1", paneId: "%0" }),
  useSearch: () => ({ target: "default", session: "demo-web" }),
  useNavigate: () => vi.fn(),
}));
// The header tabs fetch the session list via react-query; stub it empty so the route
// falls back to a single synthetic active tab for the open session (no provider needed).
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
  useQueries: () => [],
}));

// vi.hoisted: vi.mock is hoisted above plain consts, so the mock fn must be too.
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
    expect(screen.getByTestId("tv")).toHaveTextContent("s1:%0:default:true");
    expect(screen.getByText("demo-web")).toBeInTheDocument();
  });

  it("marks the opened session seen/focused on mount", () => {
    useSessionState.getState().reset();
    postSeen.mockClear();
    render(<MobileTerminalRoute />);
    expect(useSessionState.getState().focusedKey).toBe(stateKey("s1", "default", "demo-web"));
    expect(postSeen).toHaveBeenCalledWith({ serverId: "s1", target: "default", sessionName: "demo-web" });
  });
});
