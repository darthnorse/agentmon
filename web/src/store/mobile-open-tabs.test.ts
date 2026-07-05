import { describe, it, expect, beforeEach } from "vitest";
import { useMobileOpenTabs, OPEN_TABS_STORAGE_KEY, type OpenTab } from "@/store/mobile-open-tabs";
import { paneIdentity } from "@/lib/pane-identity";

const t = (paneId: string): OpenTab => ({ serverId: "s1", target: "default", paneId });
const id = (paneId: string) => paneIdentity("s1", "default", paneId);

beforeEach(() => {
  localStorage.clear();
  useMobileOpenTabs.setState({ open: [] });
});

describe("useMobileOpenTabs", () => {
  it("add appends in insertion order", () => {
    useMobileOpenTabs.getState().add(t("%0"));
    useMobileOpenTabs.getState().add(t("%1"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%0", "%1"]);
  });

  it("add is idempotent (no duplicate, no reorder)", () => {
    const s = useMobileOpenTabs.getState();
    s.add(t("%0"));
    s.add(t("%1"));
    s.add(t("%0"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%0", "%1"]);
  });

  it("remove drops by identity", () => {
    const s = useMobileOpenTabs.getState();
    s.add(t("%0"));
    s.add(t("%1"));
    s.remove(id("%0"));
    expect(useMobileOpenTabs.getState().open.map((x) => x.paneId)).toEqual(["%1"]);
  });

  it("persists the open set to localStorage", () => {
    useMobileOpenTabs.getState().add(t("%0"));
    expect(localStorage.getItem(OPEN_TABS_STORAGE_KEY)).toContain("%0");
  });
});
