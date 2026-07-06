import { describe, it, expect, vi } from "vitest";
import { onReconnectKick, kickReconnect } from "@/lib/reconnect-kick";

describe("reconnect-kick bus", () => {
  it("delivers kicks only to same-id listeners, and unsubscribe stops delivery", () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = onReconnectKick("s:default:%0", a);
    const offB = onReconnectKick("s:default:%1", b);
    kickReconnect("s:default:%0");
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).not.toHaveBeenCalled();
    offA();
    kickReconnect("s:default:%0");
    expect(a).toHaveBeenCalledTimes(1);
    kickReconnect("s:default:%2"); // no listeners → no throw
    offB();
  });
});
