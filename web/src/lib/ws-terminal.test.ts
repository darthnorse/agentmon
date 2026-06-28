import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { buildTerminalURL, nextDelay, TerminalSocket } from "@/lib/ws-terminal";

describe("buildTerminalURL", () => {
  it("builds a ws:// URL for http and escapes the target", () => {
    const url = buildTerminalURL(
      { serverId: "srv1", paneId: "%0", target: "my target" },
      { protocol: "http:", host: "localhost:5173" },
    );
    expect(url).toBe("ws://localhost:5173/api/v1/servers/srv1/panes/%250/io?target=my%20target");
  });
  it("builds a wss:// URL for https", () => {
    const url = buildTerminalURL(
      { serverId: "s", paneId: "%1", target: "default" },
      { protocol: "https:", host: "host" },
    );
    expect(url).toBe("wss://host/api/v1/servers/s/panes/%251/io?target=default");
  });
});

describe("nextDelay", () => {
  it("grows then caps", () => {
    expect(nextDelay(0)).toBe(1200);
    expect(nextDelay(1)).toBe(2400);
    expect(nextDelay(10)).toBe(10000); // capped
  });
});

// Minimal fake WebSocket the tests drive directly.
class FakeWS {
  static OPEN = 1;
  static instances: FakeWS[] = [];
  url: string;
  binaryType = "";
  readyState = 0;
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: { data: any }) => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeWS.instances.push(this);
  }
  send(data: any) { this.sent.push(data); }
  close() { this.readyState = 3; this.onclose && this.onclose(); }
  // test helpers
  fireOpen() { this.readyState = 1; this.onopen && this.onopen(); }
  fireMessage(data: any) { this.onmessage && this.onmessage({ data }); }
}

const target = { serverId: "s", paneId: "%0", target: "default" };
const loc = { protocol: "http:", host: "h" };

describe("TerminalSocket", () => {
  beforeEach(() => { FakeWS.instances = []; vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it("sends binary input and JSON resize frames", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    const ws = FakeWS.instances[0];
    ws.fireOpen();
    sock.send(Uint8Array.of(1, 2, 3));
    sock.resize(80, 24);
    expect(ws.sent[0]).toEqual(Uint8Array.of(1, 2, 3));
    expect(ws.sent[1]).toBe(JSON.stringify({ type: "resize", cols: 80, rows: 24 }));
  });

  it("delivers inbound binary frames to onData", () => {
    const got: Uint8Array[] = [];
    const sock = new TerminalSocket(target, { onData: (b) => got.push(b) }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    const ws = FakeWS.instances[0];
    ws.fireOpen();
    ws.fireMessage(Uint8Array.of(9, 9).buffer);
    expect(Array.from(got[0])).toEqual([9, 9]);
  });

  it("reconnects after an unexpected close", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    FakeWS.instances[0].fireOpen();
    FakeWS.instances[0].close(); // unexpected
    expect(FakeWS.instances.length).toBe(1);
    vi.advanceTimersByTime(1200);
    expect(FakeWS.instances.length).toBe(2); // reconnected
  });

  it("does not reconnect after dispose()", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    FakeWS.instances[0].fireOpen();
    sock.dispose();
    vi.advanceTimersByTime(20000);
    expect(FakeWS.instances.length).toBe(1);
  });
});
