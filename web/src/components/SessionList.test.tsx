import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionList, flattenSessions, matchesQuery, type SessionRow } from "@/components/SessionList";

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
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={onOpen} />);
    await userEvent.click(screen.getByText("demo-web"));
    expect(onOpen).toHaveBeenCalledWith(rows[0]);
  });
});
