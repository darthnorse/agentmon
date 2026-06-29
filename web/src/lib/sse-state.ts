import type { StateEventFrame } from "@/lib/contracts";

const EVENTS_URL = "/api/v1/events";

export interface StateStreamHandlers {
  onSnapshot(frames: StateEventFrame[]): void;
  onDelta(frame: StateEventFrame): void;
  onOpen?(): void;
  onError?(): void; // EventSource self-reconnects; this is a connection indicator only
}
export interface StateStreamDeps { EventSourceCtor?: typeof EventSource; url?: string; }

function parseJSON<T>(data: unknown): T | null {
  try { return JSON.parse(String(data)) as T; } catch { return null; }
}

// Thin EventSource transport. EventSource reconnects natively; the hub sends no
// `id:`, so each reconnect replays `event: snapshot` (= resync). DI'd for tests.
export class StateStream {
  private es: EventSource | null = null;
  private disposed = false;
  private readonly ES: typeof EventSource | undefined;
  private readonly url: string;

  constructor(private readonly handlers: StateStreamHandlers, deps: StateStreamDeps = {}) {
    this.ES = deps.EventSourceCtor ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
    this.url = deps.url ?? EVENTS_URL;
  }

  open(): void {
    if (this.disposed || this.es || !this.ES) return;
    const es = new this.ES(this.url, { withCredentials: true });
    this.es = es;
    es.addEventListener("snapshot", (ev: MessageEvent) => {
      const frames = parseJSON<StateEventFrame[]>(ev.data);
      if (Array.isArray(frames)) this.handlers.onSnapshot(frames);
    });
    es.addEventListener("state", (ev: MessageEvent) => {
      const frame = parseJSON<StateEventFrame>(ev.data);
      if (frame && typeof frame === "object" && !Array.isArray(frame)) this.handlers.onDelta(frame);
    });
    es.onopen = () => this.handlers.onOpen?.();
    es.onerror = () => this.handlers.onError?.();
  }

  dispose(): void {
    this.disposed = true;
    if (this.es) { this.es.close(); this.es = null; }
  }
}
