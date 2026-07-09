import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Sidebar } from "@/components/Sidebar";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { ServerSummary, SessionState } from "@/lib/contracts";

const servers: ServerSummary[] = [
  { id: "s1", name: "alpha", labels: [], enabled: true },
  { id: "s2", name: "bravo", labels: [], enabled: true },
];
const byServer = {
  s1: [{ name: "calm", server: "s1", target: "default", cwd: "/a", command: "c",
    windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] }],
  s2: [{ name: "hot", server: "s2", target: "default", cwd: "/b", command: "codex",
    windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "codex", cwd: "/b" }] }] }],
};

describe("Sidebar state", () => {
  it("sorts the server holding a blocked session first, without a header dot", () => {
    const rows = flattenSessions(servers, byServer);
    const stateOf = (r: SessionRow): SessionState => (r.server.id === "s2" ? "blocked" : "idle");
    render(<Sidebar servers={servers} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    const headers = screen.getAllByText(/alpha|bravo/).map((n) => n.textContent);
    expect(headers[0]).toBe("bravo"); // blocked-first ordering still driven by the rollup
    // exactly the two SESSION dots remain — no server-header dots
    expect(screen.getAllByRole("img")).toHaveLength(2);
    expect(screen.getAllByRole("img", { name: "blocked" })).toHaveLength(1);
  });

  it("tags a codex session row and leaves non-agent rows untagged", () => {
    const rows = flattenSessions(servers, byServer); // s1 command "c", s2 command "codex"
    render(<Sidebar servers={servers} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
    expect(screen.getByText("codex")).toHaveAttribute("title", "Codex");
    expect(screen.queryByText("claude")).toBeNull();
  });
});

describe("Sidebar session-less servers", () => {
  it("renders a session-less server without any dot", () => {
    const withIdle: ServerSummary[] = [
      { id: "s1", name: "alpha", labels: [], enabled: true },
      { id: "s3", name: "charlie", labels: [], enabled: true, state: "blocked" },
    ];
    const onlyS1 = { s1: byServer.s1 };
    const rows = flattenSessions(withIdle, onlyS1);
    render(<Sidebar servers={withIdle} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
    expect(screen.getByText("charlie")).toBeInTheDocument();
    // its REST state no longer paints a dot anywhere
    expect(screen.queryByRole("img", { name: "blocked" })).toBeNull();
  });

  it("renders a server with no sessions and no state with just its name", () => {
    const oneServer: ServerSummary[] = [{ id: "s9", name: "empty", labels: [], enabled: true }];
    render(<Sidebar servers={oneServer} rows={[]} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
    expect(screen.getByText("empty")).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("a search query narrows session-less servers by name", () => {
    const two: ServerSummary[] = [
      { id: "s1", name: "alpha", labels: [], enabled: true, state: "idle" },
      { id: "s2", name: "bravo", labels: [], enabled: true, state: "idle" },
    ];
    render(<Sidebar servers={two} rows={[]} query="alpha" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.queryByText("bravo")).not.toBeInTheDocument();
  });
});
