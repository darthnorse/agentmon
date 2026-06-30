import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionList, flattenSessions, matchesQuery, type SessionRow } from "@/components/SessionList";
import type { SessionState } from "@/lib/contracts";

const servers = [{ id: "s1", name: "aigallery", labels: [], enabled: true }];
const byServer = {
  s1: [{
    name: "demo-web", server: "s1", target: "default", cwd: "/home/dev/web", command: "claude",
    windows: [{ id: "@0", index: "0", name: "main", panes: [{ id: "%0", command: "claude", cwd: "/home/dev/web" }] }],
  }],
};

describe("flatten + filter", () => {
  it("flattens servers→sessions→first pane into rows", () => {
    const rows = flattenSessions(servers, byServer);
    expect(rows).toHaveLength(1);
    expect(rows[0].pane.id).toBe("%0");
    expect(rows[0].session.name).toBe("demo-web");
  });
  it("matchesQuery checks server, session name and cwd", () => {
    const row = flattenSessions(servers, byServer)[0];
    expect(matchesQuery(row, "aigall")).toBe(true);
    expect(matchesQuery(row, "demo")).toBe(true);
    expect(matchesQuery(row, "/web")).toBe(true);
    expect(matchesQuery(row, "nope")).toBe(false);
  });
});

describe("SessionList", () => {
  it("renders rows and fires onOpen", async () => {
    const rows = flattenSessions(servers, byServer);
    const onOpen = vi.fn();
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={onOpen} stateOf={() => "idle"} />);
    await userEvent.click(screen.getByText("demo-web"));
    expect(onOpen).toHaveBeenCalledWith(rows[0]);
  });
});

describe("SessionList state", () => {
  it("sorts blocked first and renders a dot per row", () => {
    const two = {
      s1: [
        { name: "calm", server: "s1", target: "default", cwd: "/a", command: "claude",
          windows: [{ id: "@0", index: "0", name: "m", panes: [{ id: "%0", command: "c", cwd: "/a" }] }] },
        { name: "needshelp", server: "s1", target: "default", cwd: "/b", command: "claude",
          windows: [{ id: "@1", index: "1", name: "m", panes: [{ id: "%1", command: "c", cwd: "/b" }] }] },
      ],
    };
    const rows = flattenSessions(servers, two);
    const stateOf = (r: SessionRow): SessionState => (r.session.name === "needshelp" ? "blocked" : "idle");
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    const labels = screen.getAllByText(/calm|needshelp/).map((n) => n.textContent);
    expect(labels[0]).toBe("needshelp"); // blocked floats up
    expect(screen.getAllByRole("img", { name: "blocked" })).toHaveLength(1);
  });
});

describe("SessionList sectioned inbox", () => {
  const allStates = {
    s1: [
      mkSession("s-idle", "@0", "%0"),
      mkSession("s-blocked", "@1", "%1"),
      mkSession("s-unknown", "@2", "%2"),
      mkSession("s-done", "@3", "%3"),
      mkSession("s-working", "@4", "%4"),
    ],
  };
  const stateMap: Record<string, SessionState> = {
    "s-idle": "idle", "s-blocked": "blocked", "s-unknown": "unknown",
    "s-done": "done", "s-working": "working",
  };
  const stateOf = (r: SessionRow): SessionState => stateMap[r.session.name];

  function orderedSequence(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll("ul > li")).map((li) => {
      const el = li as HTMLElement;
      if (el.dataset.section) return `H:${el.textContent}`;
      // The name lives in the editor's inner span (the outer .font-medium also
      // contains the ✎ rename button, so read the truncate child only).
      const name = el.querySelector(".font-medium .truncate")?.textContent;
      return `R:${name ?? el.textContent}`;
    });
  }

  it("groups rows under ordered section headers; rows sit under the right header", () => {
    const rows = flattenSessions(servers, allStates);
    const { container } = render(
      <SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />,
    );
    expect(orderedSequence(container)).toEqual([
      "H:Needs attention", "R:s-blocked",
      "H:Done", "R:s-done",
      "H:Working", "R:s-working",
      "H:Idle", "R:s-idle", "R:s-unknown",
    ]);
  });

  it("omits the header of an empty section", () => {
    const onlyIdle = { s1: [mkSession("s-idle", "@0", "%0")] };
    const rows = flattenSessions(servers, onlyIdle);
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    expect(screen.getByText("Idle")).toBeInTheDocument();
    expect(screen.queryByText("Needs attention")).not.toBeInTheDocument();
    expect(screen.queryByText("Done")).not.toBeInTheDocument();
    expect(screen.queryByText("Working")).not.toBeInTheDocument();
  });

  it("search filter still narrows rows within sections", () => {
    const rows = flattenSessions(servers, allStates);
    render(<SessionList rows={rows} query="blocked" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
    expect(screen.getByText("s-blocked")).toBeInTheDocument();
    expect(screen.queryByText("s-idle")).not.toBeInTheDocument();
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
    expect(screen.queryByText("Idle")).not.toBeInTheDocument();
  });
});

function mkSession(name: string, winId: string, paneId: string) {
  return {
    name, server: "s1", target: "default", cwd: `/home/${name}`, command: "claude",
    windows: [{ id: winId, index: "0", name: "m", panes: [{ id: paneId, command: "c", cwd: `/home/${name}` }] }],
  };
}
