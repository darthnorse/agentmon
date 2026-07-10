import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileSessionTabs, buildTabs, nextFocusAfterClose } from "@/components/MobileSessionTabs";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import type { OpenTab } from "@/store/mobile-open-tabs";
import type { SessionState } from "@/lib/contracts";

const servers = [{ id: "s1", name: "host-1", labels: [], enabled: true }];
// session.command ("zsh") deliberately DIVERGES from the pane's own command
// ("claude"): the provider tests below only pass if buildTabs derives from
// row.pane.command — the pane each tab shows — not tmux's active-pane session.command.
function mkSession(name: string, winId: string, paneId: string) {
  return {
    name, server: "s1", target: "default", cwd: `/home/${name}`, command: "zsh",
    windows: [{ id: winId, index: "0", name: "m", panes: [{ id: paneId, command: "claude", cwd: `/home/${name}` }] }],
  };
}
const byServer = { s1: [mkSession("alpha", "@0", "%0"), mkSession("beta", "@1", "%1"), mkSession("gamma", "@2", "%2")] };
const rows = flattenSessions(servers, byServer);
const idle = (): SessionState => "idle";
const current = { serverId: "s1", target: "default", session: "beta", paneId: "%1" };
const open = (paneId: string): OpenTab => ({ serverId: "s1", target: "default", paneId });
const openAll: OpenTab[] = [open("%0"), open("%1"), open("%2")];

describe("buildTabs", () => {
  it("renders open-set members in order and marks the current active", () => {
    const tabs = buildTabs(openAll, rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["alpha", "beta", "gamma"]);
    expect(tabs.filter((t) => t.active).map((t) => t.name)).toEqual(["beta"]);
  });

  it("honors open-set order (not row order)", () => {
    const tabs = buildTabs([open("%2"), open("%1"), open("%0")], rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["gamma", "beta", "alpha"]);
    expect(tabs.filter((t) => t.active).map((t) => t.name)).toEqual(["beta"]);
  });

  it("skips an open tab absent from the live rows (no dead tab)", () => {
    const tabs = buildTabs([open("%1"), open("%ghost")], rows, current, idle);
    expect(tabs.map((t) => t.name)).toEqual(["beta"]);
  });

  it("synthesizes an active tab when the current session isn't in the rows yet", () => {
    const tabs = buildTabs([open("%9")], [], { serverId: "s1", target: "default", session: "solo", paneId: "%9" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ name: "solo", active: true, state: "unknown" });
  });

  it("carries each row's state through from stateOf", () => {
    const stateOf = (r: SessionRow): SessionState => (r.session.name === "gamma" ? "blocked" : "idle");
    const tabs = buildTabs(openAll, rows, current, stateOf);
    expect(tabs.find((t) => t.name === "gamma")?.state).toBe("blocked");
  });

  it("keeps a single active tab (no phantom) when the URL name leads the cached list mid-rename", () => {
    const stale = flattenSessions(servers, { s1: [mkSession("old-name", "@1", "%1")] });
    const tabs = buildTabs([open("%1")], stale, { serverId: "s1", target: "default", session: "new-name", paneId: "%1" }, idle);
    expect(tabs).toHaveLength(1);
    expect(tabs[0]).toMatchObject({ active: true, name: "new-name", paneId: "%1" });
  });

  it("identifies the current session by pane, not by its (mutable) name", () => {
    const tabs = buildTabs(openAll, rows, { serverId: "s1", target: "default", session: "renamed", paneId: "%2" }, idle);
    expect(tabs).toHaveLength(3);
    expect(tabs.filter((t) => t.active).map((t) => t.paneId)).toEqual(["%2"]);
    expect(tabs.find((t) => t.active)?.name).toBe("renamed");
  });

  it("threads the provider from the resolved row into each tab", () => {
    const tabs = buildTabs(openAll, rows, current, idle);
    expect(tabs.map((t) => t.provider)).toEqual(["claude", "claude", "claude"]);
  });

  it("gives the synthetic first-paint tab no provider", () => {
    const tabs = buildTabs([], [], current, idle);
    expect(tabs[0].provider).toBeUndefined();
  });

  it("renders the tag inside a tab", () => {
    const tabs = buildTabs([open("%0")], rows, current, idle);
    render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
    expect(screen.getAllByText("claude").length).toBeGreaterThanOrEqual(1);
  });
});

describe("nextFocusAfterClose", () => {
  const tabs = buildTabs(openAll, rows, current, idle); // alpha(%0), beta(%1,active), gamma(%2)
  it("returns the right neighbor", () => {
    expect(nextFocusAfterClose(tabs, tabs[0].key)?.name).toBe("beta");
  });
  it("falls back to the left neighbor when closing the last tab", () => {
    expect(nextFocusAfterClose(tabs, tabs[2].key)?.name).toBe("beta");
  });
  it("returns null when closing the only visible tab", () => {
    const one = buildTabs([open("%0")], rows, { serverId: "s1", target: "default", session: "alpha", paneId: "%0" }, idle);
    expect(nextFocusAfterClose(one, one[0].key)).toBeNull();
  });
  it("returns null for an unknown key", () => {
    expect(nextFocusAfterClose(tabs, "nope")).toBeNull();
  });
});

describe("MobileSessionTabs", () => {
  const tabs = buildTabs(openAll, rows, current, idle);

  it("switches on tapping an inactive tab", async () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={() => {}} />);
    await userEvent.click(screen.getByText("alpha"));
    expect(onSwitch).toHaveBeenCalledTimes(1);
    expect(onSwitch.mock.calls[0][0]).toMatchObject({ name: "alpha", paneId: "%0" });
  });

  it("renders the active tab as a plain label (no rename editor, no switch button)", () => {
    const onSwitch = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={() => {}} />);
    expect(screen.queryByRole("button", { name: "Rename session" })).not.toBeInTheDocument();
    expect(onSwitch).not.toHaveBeenCalled();
  });

  it("shows a close button on every tab, including the active one", () => {
    render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
    expect(screen.getByRole("button", { name: "Close alpha" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close beta" })).toBeInTheDocument(); // active
    expect(screen.getByRole("button", { name: "Close gamma" })).toBeInTheDocument();
  });

  it("calls onClose (not onSwitch) when the close button is tapped", async () => {
    const onSwitch = vi.fn();
    const onClose = vi.fn();
    render(<MobileSessionTabs tabs={tabs} onSwitch={onSwitch} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: "Close alpha" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClose.mock.calls[0][0]).toMatchObject({ name: "alpha", paneId: "%0" });
    expect(onSwitch).not.toHaveBeenCalled();
  });

  it("marks the active tab with aria-current", () => {
    render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
    expect(document.querySelector('[aria-current="page"]')?.textContent).toContain("beta");
    expect(document.querySelector('[aria-current="page"]')?.textContent).toContain("claude");
  });
});
