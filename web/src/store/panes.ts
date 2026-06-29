import { create } from "zustand";

export const GRID_TILE_CAP = 6; // client-side soft cap on simultaneously-live tiles

export interface OpenPane {
  id: string; // serverId:target:session:paneId
  serverId: string;
  paneId: string;
  target: string;
  session: string;
  serverName: string;
}

const idOf = (p: Omit<OpenPane, "id">) => `${p.serverId}:${p.target}:${p.session}:${p.paneId}`;

interface PanesState {
  panes: OpenPane[];
  focusedId: string | null;
  openPane(p: Omit<OpenPane, "id">): { ok: boolean; reason?: "cap" };
  closePane(id: string): void;
  focus(id: string): void;
  collapse(): void;
}

export const usePanes = create<PanesState>((set, get) => ({
  panes: [],
  focusedId: null,
  openPane(p) {
    const id = idOf(p);
    const existing = get().panes.find((x) => x.id === id);
    if (existing) {
      set({ focusedId: id }); // already open → just focus/expand
      return { ok: true };
    }
    if (get().panes.length >= GRID_TILE_CAP) return { ok: false, reason: "cap" };
    set((s) => ({ panes: [...s.panes, { ...p, id }], focusedId: id }));
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
}));
