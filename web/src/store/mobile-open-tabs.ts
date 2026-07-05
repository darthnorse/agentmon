import { create } from "zustand";
import { persist } from "zustand/middleware";
import { paneIdentity } from "@/lib/pane-identity";

export const OPEN_TABS_STORAGE_KEY = "agentmon-mobile-open-tabs";

// A session the user has explicitly opened on mobile. Keyed by pane identity
// (serverId:target:paneId) — stable across rename (target, not the mutable name) and
// across reload (tmux paneId persists). The session NAME is never stored; it is always
// resolved from the live session list so a rename reflects on refetch.
export interface OpenTab {
  serverId: string;
  target: string;
  paneId: string;
}

interface MobileOpenTabsState {
  open: OpenTab[]; // ordered = tab render order (insertion order)
  add(t: OpenTab): void; // append if absent (idempotent)
  remove(id: string): void; // remove by paneIdentity
  has(id: string): boolean;
}

const idOf = (t: OpenTab) => paneIdentity(t.serverId, t.target, t.paneId);

// Per-device open set persisted to localStorage (v1 is single-user, one device — a hub
// table is a later add). Drives the mobile tab bar and pane warming.
export const useMobileOpenTabs = create<MobileOpenTabsState>()(
  persist(
    (set, get) => ({
      open: [],
      add: (t) =>
        set((s) => (s.open.some((x) => idOf(x) === idOf(t)) ? s : { open: [...s.open, t] })),
      remove: (id) => set((s) => ({ open: s.open.filter((x) => idOf(x) !== id) })),
      has: (id) => get().open.some((x) => idOf(x) === id),
    }),
    {
      name: OPEN_TABS_STORAGE_KEY,
      partialize: (s) => ({ open: s.open }), // persist data only, never the actions
    },
  ),
);
