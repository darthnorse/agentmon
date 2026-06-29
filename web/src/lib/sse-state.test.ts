import { describe, it, expect, vi } from "vitest";
import { StateStream } from "@/lib/sse-state";

class FakeES {
  static instances: FakeES[] = [];
  url: string; opts: unknown;
  listeners: Record<string, ((ev: { data: string }) => void)[]> = {};
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(url: string, opts?: unknown) { this.url = url; this.opts = opts; FakeES.instances.push(this); }
  addEventListener(type: string, fn: (ev: { data: string }) => void) { (this.listeners[type] ??= []).push(fn); }
  close() { this.closed = true; }
  emit(type: string, data: string) { (this.listeners[type] ?? []).forEach((fn) => fn({ data })); }
}

function mk() {
  FakeES.instances = [];
  const onSnapshot = vi.fn(); const onDelta = vi.fn();
  const s = new StateStream({ onSnapshot, onDelta }, { EventSourceCtor: FakeES as unknown as typeof EventSource });
  s.open();
  return { s, es: FakeES.instances[0], onSnapshot, onDelta };
}

describe("StateStream", () => {
  it("connects to /api/v1/events", () => {
    const { es } = mk();
    expect(es.url).toBe("/api/v1/events");
  });
  it("parses a snapshot array → onSnapshot", () => {
    const { es, onSnapshot } = mk();
    es.emit("snapshot", JSON.stringify([{ server: "s", target: "t", session: "x", state: "blocked" }]));
    expect(onSnapshot).toHaveBeenCalledWith([{ server: "s", target: "t", session: "x", state: "blocked" }]);
  });
  it("parses a state delta → onDelta", () => {
    const { es, onDelta } = mk();
    es.emit("state", JSON.stringify({ server: "s", target: "t", session: "x", state: "done" }));
    expect(onDelta).toHaveBeenCalledWith({ server: "s", target: "t", session: "x", state: "done" });
  });
  it("re-fires onSnapshot on reconnect (server replays the snapshot)", () => {
    const { es, onSnapshot } = mk();
    es.emit("snapshot", JSON.stringify([]));
    es.emit("snapshot", JSON.stringify([{ server: "s", target: "t", session: "x", state: "idle" }]));
    expect(onSnapshot).toHaveBeenCalledTimes(2);
  });
  it("drops malformed JSON without throwing", () => {
    const { es, onSnapshot, onDelta } = mk();
    es.emit("snapshot", "{not json");
    es.emit("state", "{not json");
    expect(onSnapshot).not.toHaveBeenCalled();
    expect(onDelta).not.toHaveBeenCalled();
  });
  it("dispose() closes the EventSource and blocks re-open", () => {
    const { s, es } = mk();
    s.dispose();
    expect(es.closed).toBe(true);
    s.open();
    expect(FakeES.instances.length).toBe(1);
  });
});
