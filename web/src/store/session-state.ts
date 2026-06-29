import { create } from "zustand";
import type { SessionState, StateEventFrame } from "@/lib/contracts";
import { stateKey, type StateSnapshot } from "@/lib/state";

interface SessionStateStore extends StateSnapshot {
  connected: boolean;
  applySnapshot(frames: StateEventFrame[]): void;
  applyDelta(frame: StateEventFrame): void;
  markSeen(key: string): void;
  setFocusedKey(key: string | null): void;
  setConnected(b: boolean): void;
  reset(): void;
}

export const useSessionState = create<SessionStateStore>((set) => ({
  live: new Map<string, SessionState>(),
  seen: new Set<string>(),
  focusedKey: null,
  connected: false,
  applySnapshot(frames) {
    const live = new Map<string, SessionState>();
    for (const f of frames) live.set(stateKey(f.server, f.target, f.session), f.state);
    set({ live, seen: new Set(), connected: true }); // fresh snapshot is already server-seen-projected
  },
  applyDelta(frame) {
    set((s) => {
      const key = stateKey(frame.server, frame.target, frame.session);
      const live = new Map(s.live); live.set(key, frame.state);
      let seen = s.seen;
      if (seen.has(key)) { seen = new Set(seen); seen.delete(key); } // a delta = changed/re-alert → un-suppress
      return { live, seen };
    });
  },
  markSeen(key) {
    set((s) => {
      if (s.seen.has(key)) return {} as Partial<SessionStateStore>;
      const seen = new Set(s.seen); seen.add(key);
      return { seen };
    });
  },
  setFocusedKey(focusedKey) { set({ focusedKey }); },
  setConnected(connected) { set({ connected }); },
  reset() { set({ live: new Map(), seen: new Set(), focusedKey: null, connected: false }); },
}));
