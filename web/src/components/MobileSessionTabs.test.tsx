import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileSessionTabs, buildTabs } from "@/components/MobileSessionTabs";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { SessionState } from "@/lib/contracts";

const servers = [{ id: "s1", name: "host-1", labels: [], enabled: true }];
function mkSession(name: string, winId: string, paneId: string) {
  return {
    name, server: "s1", target: "default", cwd: `/home/${name}`, command: "claude",
    windows: [{ id: winId, index: "0", name: "m", panes: [{ id: paneId, command: "c", cwd: `/home/${name}` }] }],
  };
}
const byServer = { s1: [mkSession("alpha", "@0", "%0"), mkSession("beta", "@1", "%1"), mkSession("gamma", "@2", "%2")] };
const rows = flattenSessions(servers, byServer);
const idle = (): SessionState => "idle";
const current = { serverId: "s1", target: "default", session: "beta", paneId: "%1" };

describe("buildTabs", () => {
  it("marks the current session active and keeps stable (not blocked-first) order", () => {
    const tabs = buildTabs(rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["alpha", "beta", "gamma"]);
    expect(tabs.filter((t) => t.active).map((t) => t.name)).toEqual(["beta"]);
  });

  it("synthesizes an active tab when the current session isn't in the list yet", () => {
    const tabs = buildTabs([], { serverId: "s1", target: "default", session: "solo", paneId: "%9" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ name: "solo", active: true, state: "unknown" });
  });

  it("carries each row's state through from stateOf", () => {
    const stateOf = (r: SessionRow): SessionState => (r.session.name === "gamma" ? "blocked" : "idle");
    const tabs = buildTabs(rows, current, stateOf);
    expect(tabs.find((t) => t.name === "gamma")?.state).toBe("blocked");
  });

  // Rename advances the URL name before the cached session list refetches. Matching on
  // pane identity (not name) must keep ONE active tab labelled from the URL — never a
  // synthetic new-name tab alongside a stale old-name row for the same pane.
  it("keeps a single active tab (no phantom) when the URL name leads the cached list mid-rename", () => {
    const stale = flattenSessions(servers, { s1: [mkSession("old-name", "@1", "%1")] });
    const tabs = buildTabs(stale, { serverId: "s1", target: "default", session: "new-name", paneId: "%1" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ active: true, name: "new-name", paneId: "%1" });
  });

  it("identifies the current session by pane, not by its (mutable) name", () => {
    // URL name differs from the list's %2 row, but the pane matches → gamma's row stays
    // the single active tab, labelled from the URL, with no synthetic duplicate.
    const tabs = buildTabs(rows, { serverId: "s1", target: "default", session: "renamed", paneId: "%2" }, idle);
    expect(tabs).toHaveLength(3);
    expect(tabs.filter((t) => t.active).map((t) => t.paneId)).toEqual(["%2"]);
    expect(tabs.find((t) => t.active)?.name).toBe("renamed");
  });
});

describe("MobileSessionTabs", () => {
  it("switches on tapping an inactive tab", async () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={buildTabs(rows, current, idle)} onSwitch={onSwitch} onRenamed={() => {}} />);
    await userEvent.click(screen.getByText("alpha"));
    expect(onSwitch).toHaveBeenCalledTimes(1);
    expect(onSwitch.mock.calls[0][0]).toMatchObject({ name: "alpha", paneId: "%0" });
  });

  it("renders the active tab as an inline rename control, not a switch button", () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={buildTabs(rows, current, idle)} onSwitch={onSwitch} onRenamed={() => {}} />);
    // active session exposes rename (SessionNameEditor); it is not a switch button
    expect(screen.getByRole("button", { name: "Rename session" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /beta/ })).not.toBeInTheDocument();
    // and tapping the active name never fires a switch
    expect(onSwitch).not.toHaveBeenCalled();
  });

  it("marks the active tab with aria-current for the current session", () => {
    render(<MobileSessionTabs tabs={buildTabs(rows, current, idle)} onSwitch={() => {}} onRenamed={() => {}} />);
    expect(document.querySelector('[aria-current="page"]')?.textContent).toContain("beta");
  });
});
