import { describe, it, expect, beforeEach, vi } from "vitest";
import { usePanes, GRID_TILE_CAP, paneKey } from "@/store/panes";
import { onReconnectKick } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";

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

  it("renamePane re-keys the open pane (session+id) and focus follows", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus("s:default:sess0:%0");
    usePanes.getState().renamePane("s:default:sess0:%0", "renamed");
    const p = usePanes.getState().panes[0];
    expect(p.session).toBe("renamed");
    expect(p.id).toBe("s:default:renamed:%0"); // paneId (%0) preserved → the WS survives
    expect(usePanes.getState().focusedId).toBe("s:default:renamed:%0"); // focus follows the re-key
  });

  it("renamePane leaves focus null when the renamed pane wasn't focused", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().renamePane("s:default:sess0:%0", "x");
    expect(usePanes.getState().panes[0].id).toBe("s:default:x:%0");
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("renamePane is a no-op for a pane that isn't open", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().renamePane("not:open:%9", "x");
    expect(usePanes.getState().panes[0].id).toBe("s:default:sess0:%0"); // unchanged
  });

  it("collapse clears focus but keeps panes", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus("s:default:sess0:%0");
    usePanes.getState().collapse();
    expect(usePanes.getState().focusedId).toBeNull();
    expect(usePanes.getState().panes).toHaveLength(1);
  });

  it("re-opening an already-open pane kicks its reconnect (no new tile)", () => {
    usePanes.getState().openPane(mk(0));
    const kicked = vi.fn();
    const off = onReconnectKick(paneIdentity("s", "default", "%0"), kicked);
    const r = usePanes.getState().openPane(mk(0)); // dedupe path
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(kicked).toHaveBeenCalledTimes(1);
    off();
  });

  it("first open does NOT kick (a fresh socket dials by itself)", () => {
    const kicked = vi.fn();
    const off = onReconnectKick(paneIdentity("s", "default", "%0"), kicked);
    usePanes.getState().openPane(mk(0));
    expect(kicked).not.toHaveBeenCalled();
    off();
  });
});

describe("openPane while a tile is expanded", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("collapses to grid when a NEW pane is opened while a tile is expanded", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus(paneKey("s", "default", "sess0", "%0")); // expand tile 0
    usePanes.getState().openPane(mk(1));                               // open a NEW pane
    expect(usePanes.getState().focusedId).toBeNull();                  // collapsed → new tile visible
    expect(usePanes.getState().panes).toHaveLength(2);
  });

  it("re-opening an already-open pane does NOT collapse", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().focus(paneKey("s", "default", "sess0", "%0"));
    usePanes.getState().openPane(mk(0));                               // same pane again → dedupe
    expect(usePanes.getState().focusedId).toBe(paneKey("s", "default", "sess0", "%0"));
  });
});
