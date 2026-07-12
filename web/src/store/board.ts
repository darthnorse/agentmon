import { create } from "zustand";
import type { BoardDeltaFrame, EpicDTO } from "@/lib/contracts";

// The single "which stages need a human" rule — reused by useBoardStream so the
// attention badge/count and the toast/sound/notification can never diverge.
export const needsAttention = (stage: string) => stage === "escalated" || stage === "stalled";

interface BoardAttentionStore {
  attention: Map<string, string>; // epicId → projectId, for escalated/stalled epics
  connected: boolean;
  applySnapshot(epics: EpicDTO[]): void;
  applyDelta(f: BoardDeltaFrame): void;
  setConnected(v: boolean): void;
  reset(): void;
}

export const useBoardAttention = create<BoardAttentionStore>((set) => ({
  attention: new Map(),
  connected: false,
  applySnapshot(epics) {
    const m = new Map<string, string>();
    for (const e of epics) if (needsAttention(e.stage)) m.set(e.id, e.project_id);
    set({ attention: m, connected: true });
  },
  applyDelta(f) {
    set((s) => {
      const has = s.attention.has(f.epic_id);
      if (needsAttention(f.stage) === has) return s;
      const m = new Map(s.attention);
      if (needsAttention(f.stage)) m.set(f.epic_id, f.project_id);
      else m.delete(f.epic_id);
      return { attention: m };
    });
  },
  setConnected(connected) { set({ connected }); },
  reset() { set({ attention: new Map(), connected: false }); },
}));

export const useNeedsTotal = (): number => useBoardAttention((s) => s.attention.size);

export function needsByProject(attention: Map<string, string>): Map<string, number> {
  const out = new Map<string, number>();
  for (const pid of attention.values()) out.set(pid, (out.get(pid) ?? 0) + 1);
  return out;
}
