import { create } from "zustand";
import type { SessionState } from "@/lib/contracts";
import { kickReconnect } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";

export const GRID_TILE_CAP = 6; // client-side soft cap on simultaneously-live tiles

export interface OpenPane {
  id: string; // serverId:target:session:paneId
  serverId: string;
  paneId: string;
  target: string;
  session: string;
  serverName: string;
  state?: SessionState; // REST state captured at open — first-paint fallback until the SSE store has it
}

// The canonical pane id / grid key. Exported so callers that have the parts (the
// inbox rows, focus-next) build the SAME key instead of hand-joining it — if the
// format ever drifts, focus(id) must not silently miss.
export const paneKey = (serverId: string, target: string, session: string, paneId: string) =>
  `${serverId}:${target}:${session}:${paneId}`;

const idOf = (p: Omit<OpenPane, "id">) => paneKey(p.serverId, p.target, p.session, p.paneId);

interface PanesState {
  panes: OpenPane[];
  focusedId: string | null;
  openPane(p: Omit<OpenPane, "id">): { ok: boolean; reason?: "cap" };
  closePane(id: string): void;
  focus(id: string): void;
  collapse(): void;
  // Re-key an open pane after its session is renamed. Only `session` changes, so the
  // terminal WS (keyed by paneId) survives — no reconnect. No-op if the pane isn't open.
  renamePane(oldId: string, newSession: string): void;
}

export const usePanes = create<PanesState>((set, get) => ({
  panes: [],
  focusedId: null,
  openPane(p) {
    const id = idOf(p);
    const existing = get().panes.find((x) => x.id === id);
    if (existing) {
      // Already open → no-op on the grid, but the tile's socket may be asleep in
      // reconnect backoff (e.g. a recreated same-named session reuses pane %0 after
      // a tmux server restart) — kick it so the tile comes alive immediately.
      kickReconnect(paneIdentity(p.serverId, p.target, p.paneId));
      return { ok: true }; // do NOT change focusedId
    }
    if (get().panes.length >= GRID_TILE_CAP) return { ok: false, reason: "cap" };
    // A new pane must be visible immediately. If a tile is currently expanded, the
    // new one would be display:none behind it — collapse to the grid instead.
    set((s) => ({ panes: [...s.panes, { ...p, id }], focusedId: null }));
    return { ok: true };
  },
  closePane(id) {
    set((s) => ({
      panes: s.panes.filter((x) => x.id !== id),
      focusedId: s.focusedId === id ? null : s.focusedId,
    }));
  },
  focus(id) { set({ focusedId: id }); },
  collapse() { set({ focusedId: null }); },
  renamePane(oldId, newSession) {
    set((s) => {
      const pane = s.panes.find((p) => p.id === oldId);
      if (!pane) return s; // not open as a tile → nothing to re-key
      const newId = paneKey(pane.serverId, pane.target, newSession, pane.paneId);
      return {
        panes: s.panes.map((p) => (p.id === oldId ? { ...p, session: newSession, id: newId } : p)),
        focusedId: s.focusedId === oldId ? newId : s.focusedId,
      };
    });
  },
}));
