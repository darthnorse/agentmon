// Thin transport over one WebSocket to the M4 relay. Decoupled from xterm via
// callbacks; the WebSocket constructor + location are injectable for tests.
// Transparent protocol: binary frames = raw terminal bytes (every inbound binary
// frame, including the first scrollback snapshot, goes to onData); a JSON text
// frame {type:"resize",cols,rows} is the only control frame we send.

export interface TerminalTarget {
  serverId: string;
  paneId: string;
  target: string;
}

export interface Loc {
  protocol: string;
  host: string;
}

export function buildTerminalURL(t: TerminalTarget, loc: Loc): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  const path =
    `/api/v1/servers/${encodeURIComponent(t.serverId)}` +
    `/panes/${encodeURIComponent(t.paneId)}/io` +
    `?target=${encodeURIComponent(t.target)}`;
  return `${scheme}//${loc.host}${path}`;
}

const BACKOFF_BASE = 1200;
const BACKOFF_CAP = 10000;

export function nextDelay(attempt: number): number {
  return Math.min(BACKOFF_CAP, BACKOFF_BASE * 2 ** attempt);
}

export interface TerminalStateFrame {
  state: string;
  session: string;
}

export interface TerminalSocketHandlers {
  onData(bytes: Uint8Array): void;
  onOpen?(): void;
  onClose?(): void;
  onError?(): void;
  // Out-of-band hub state delta for this pane's session ({t:"state",state,session}).
  onState?(frame: TerminalStateFrame): void;
}

export interface TerminalSocketDeps {
  WebSocketCtor?: typeof WebSocket;
  loc?: Loc;
}

export class TerminalSocket {
  private ws: WebSocket | null = null;
  private disposed = false;
  private attempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly WS: typeof WebSocket;
  private readonly loc: Loc;
  private readonly url: string;

  constructor(
    target: TerminalTarget,
    private readonly handlers: TerminalSocketHandlers,
    deps: TerminalSocketDeps = {},
  ) {
    this.WS = deps.WebSocketCtor ?? WebSocket;
    this.loc = deps.loc ?? { protocol: location.protocol, host: location.host };
    this.url = buildTerminalURL(target, this.loc);
    this.onVisibility = this.onVisibility.bind(this);
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", this.onVisibility);
    }
  }

  get connected(): boolean {
    return !!this.ws && this.ws.readyState === 1;
  }

  open(): void {
    if (this.disposed || this.ws !== null) return;
    const ws = new this.WS(this.url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.handlers.onOpen?.();
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === "string") {
        // Text frames carry hub control, not terminal output. Today the only one
        // we read is the {t:"state"} session-state delta; anything else is ignored.
        try {
          const m = JSON.parse(ev.data);
          if (m && m.t === "state") {
            this.handlers.onState?.({ state: m.state, session: m.session });
          }
        } catch { /* malformed JSON — ignore, never treat as output */ }
        return;
      }
      this.handlers.onData(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onerror = () => {
      this.handlers.onError?.();
    };
    ws.onclose = () => {
      this.ws = null;
      this.handlers.onClose?.();
      this.scheduleReconnect();
    };
  }

  send(bytes: Uint8Array): void {
    if (this.ws && this.ws.readyState === 1) this.ws.send(bytes);
  }

  resize(cols: number, rows: number): void {
    if (this.ws && this.ws.readyState === 1) {
      this.ws.send(JSON.stringify({ type: "resize", cols, rows }));
    }
  }

  dispose(): void {
    this.disposed = true;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.onVisibility);
    }
    if (this.ws) {
      this.ws.onclose = null; // suppress reconnect on our own close
      this.ws.onopen = null;  // suppress stale open handler if racing
      this.ws.close();
      this.ws = null;
    }
  }

  private scheduleReconnect(): void {
    if (this.disposed || this.reconnectTimer) return;
    const delay = nextDelay(this.attempt++);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  private onVisibility(): void {
    if (this.disposed) return;
    if (document.visibilityState === "visible" && this.ws === null) {
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      this.open(); // wake → reconnect immediately
    }
  }
}
