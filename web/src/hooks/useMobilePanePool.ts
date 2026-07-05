import * as React from "react";
import { paneIdentity } from "@/lib/pane-identity";

// Max terminals kept live at once in the mobile terminal view (eager-warm ceiling +
// LRU-eviction bound). Well within the hub's per-principal relay cap; mirrors the
// desktop grid's bounded pool.
export const MOBILE_POOL_CAP = 4;

export interface PoolPane {
  serverId: string;
  target: string;
  paneId: string;
}

export interface PoolState {
  panes: PoolPane[];       // insertion order (stable render order)
  focusedId: string | null; // paneIdentity of the focused pane
  lru: string[];           // identities, least-recently-focused first
}

export type PoolAction =
  | { type: "open"; pane: PoolPane; focus: boolean }
  | { type: "focus"; id: string }
  | { type: "close"; id: string };

const idOf = (p: PoolPane) => paneIdentity(p.serverId, p.target, p.paneId);

export const initialPool: PoolState = { panes: [], focusedId: null, lru: [] };

export function poolReducer(state: PoolState, action: PoolAction): PoolState {
  switch (action.type) {
    case "open": {
      const id = idOf(action.pane);
      const exists = state.panes.some((p) => idOf(p) === id);
      let panes = exists ? state.panes : [...state.panes, action.pane];
      // LRU: focusing → most-recent (end). New-but-not-focused → least-recent (front),
      // so it is the first to be evicted if never focused. Existing-not-focused → unchanged.
      let lru: string[];
      if (action.focus) lru = [...state.lru.filter((x) => x !== id), id];
      else if (!exists) lru = [id, ...state.lru];
      else lru = state.lru;
      const focusedId = action.focus ? id : state.focusedId;
      // Evict the least-recently-focused pane that is neither focused nor just-added.
      while (panes.length > MOBILE_POOL_CAP) {
        const victim = lru.find((x) => x !== focusedId && x !== id);
        if (!victim) break;
        panes = panes.filter((p) => idOf(p) !== victim);
        lru = lru.filter((x) => x !== victim);
      }
      return { panes, focusedId, lru };
    }
    case "focus": {
      if (!state.panes.some((p) => idOf(p) === action.id)) return state; // absent → no-op
      return { ...state, focusedId: action.id, lru: [...state.lru.filter((x) => x !== action.id), action.id] };
    }
    case "close": {
      if (!state.panes.some((p) => idOf(p) === action.id)) return state; // absent → no-op
      return {
        panes: state.panes.filter((p) => idOf(p) !== action.id),
        lru: state.lru.filter((x) => x !== action.id),
        // The route re-focuses a neighbor explicitly; a null focus for one render is safe.
        focusedId: state.focusedId === action.id ? null : state.focusedId,
      };
    }
  }
}

export function useMobilePanePool() {
  const [state, dispatch] = React.useReducer(poolReducer, initialPool);
  return React.useMemo(
    () => ({
      panes: state.panes,
      focusedId: state.focusedId,
      open: (p: PoolPane) => dispatch({ type: "open", pane: p, focus: false }),
      openAndFocus: (p: PoolPane) => dispatch({ type: "open", pane: p, focus: true }),
      focus: (id: string) => dispatch({ type: "focus", id }),
      close: (id: string) => dispatch({ type: "close", id }),
    }),
    [state],
  );
}
