import { describe, it, expect, beforeEach } from "vitest";
import { usePanes, GRID_TILE_CAP } from "@/store/panes";

const mk = (n: number) => ({
  serverId: "s", paneId: `%${n}`, target: "default", session: `sess${n}`, serverName: "aigallery",
});

describe("panes store", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("opens a pane and does NOT auto-focus (grid-first)", () => {
    const r = usePanes.getState().openPane(mk(0));
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(usePanes.getState().focusedId).toBeNull(); // grid-first: no auto-focus on open
  });

  it("is idempotent on the same pane id (no duplicate, focusedId unchanged)", () => {
    usePanes.getState().openPane(mk(0));
    const r = usePanes.getState().openPane(mk(0));
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(usePanes.getState().focusedId).toBeNull(); // focusedId unchanged (still null)
  });

  it("focus expands a pane and collapse returns to grid", () => {
    usePanes.getState().openPane(mk(0));
    expect(usePanes.getState().focusedId).toBeNull(); // grid-first: no focus on open
    usePanes.getState().focus("s:default:sess0:%0");
    expect(usePanes.getState().focusedId).toBe("s:default:sess0:%0");
    usePanes.getState().collapse();
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("rejects opening beyond the soft cap", () => {
    for (let i = 0; i < GRID_TILE_CAP; i++) expect(usePanes.getState().openPane(mk(i)).ok).toBe(true);
    const r = usePanes.getState().openPane(mk(GRID_TILE_CAP));
    expect(r.ok).toBe(false);
    expect(r.reason).toBe("cap");
    expect(usePanes.getState().panes).toHaveLength(GRID_TILE_CAP);
  });

  it("closePane removes it and clears focus if it was focused", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus("s:default:sess0:%0");
    usePanes.getState().closePane("s:default:sess0:%0");
    expect(usePanes.getState().panes).toHaveLength(0);
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("collapse clears focus but keeps panes", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus("s:default:sess0:%0");
    usePanes.getState().collapse();
    expect(usePanes.getState().focusedId).toBeNull();
    expect(usePanes.getState().panes).toHaveLength(1);
  });
});
