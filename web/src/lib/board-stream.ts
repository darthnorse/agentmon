import type { BoardDeltaFrame, EpicDTO, ProjectDTO } from "@/lib/contracts";

const BOARD_EVENTS_URL = "/api/v1/orchestrator/events";

export interface BoardSnapshotFrame { projects: ProjectDTO[]; epics: EpicDTO[]; }

export interface BoardStreamHandlers {
  onSnapshot(s: BoardSnapshotFrame): void;
  onDelta(f: BoardDeltaFrame): void;
  onOpen?(): void;
  onError?(): void; // EventSource self-reconnects; connection indicator only
}

export interface BoardStreamDeps { EventSourceCtor?: typeof EventSource; url?: string; }

export class BoardStream {
  private es: EventSource | null = null;
  private disposed = false;
  private readonly ES: typeof EventSource | undefined;
  private readonly url: string;

  constructor(private handlers: BoardStreamHandlers, deps?: BoardStreamDeps) {
    this.ES = deps?.EventSourceCtor ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
    this.url = deps?.url ?? BOARD_EVENTS_URL;
  }

  open(): void {
    if (this.disposed || this.es || !this.ES) return;
    const es = new this.ES(this.url, { withCredentials: true });
    this.es = es;
    es.addEventListener("board-snapshot", (ev: MessageEvent) => {
      try { this.handlers.onSnapshot(JSON.parse(ev.data as string) as BoardSnapshotFrame); } catch { /* malformed frame — wait for the next */ }
    });
    es.addEventListener("board", (ev: MessageEvent) => {
      try { this.handlers.onDelta(JSON.parse(ev.data as string) as BoardDeltaFrame); } catch { /* ditto */ }
    });
    es.onopen = () => this.handlers.onOpen?.();
    es.onerror = () => this.handlers.onError?.();
  }

  dispose(): void {
    this.disposed = true;
    if (this.es) { this.es.close(); this.es = null; }
  }
}
