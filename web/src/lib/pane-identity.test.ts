import { describe, it, expect } from "vitest";
import { paneIdentity, liveIdentSet, paneEnded, readyServerSet } from "@/lib/pane-identity";

describe("paneIdentity", () => {
  it("joins server:target:pane, independent of session name", () => {
    expect(paneIdentity("s1", "default", "%0")).toBe("s1:default:%0");
  });
});

describe("liveIdentSet", () => {
  it("collects the pane identity of every row", () => {
    const rows = [
      { server: { id: "s1" }, session: { target: "default" }, pane: { id: "%0" } },
      { server: { id: "s2" }, session: { target: "alt" }, pane: { id: "%3" } },
    ];
    const set = liveIdentSet(rows);
    expect(set.has("s1:default:%0")).toBe(true);
    expect(set.has("s2:alt:%3")).toBe(true);
    expect(set.size).toBe(2);
  });
});

describe("readyServerSet", () => {
  it("keeps only servers whose positional query succeeded", () => {
    const servers = [{ id: "s1" }, { id: "s2" }, { id: "s3" }];
    const queries = [{ isSuccess: true }, { isSuccess: false }, undefined];
    expect(readyServerSet(servers, queries)).toEqual(new Set(["s1"]));
  });
});

describe("paneEnded", () => {
  const ready = new Set(["s1"]);
  const live = new Set(["s1:default:%0"]);
  it("ended only when the server is ready AND the pane is absent from the live set", () => {
    expect(paneEnded(ready, live, "s1", "default", "%9")).toBe(true);
    expect(paneEnded(ready, live, "s1", "default", "%0")).toBe(false); // pane live
    expect(paneEnded(ready, live, "s2", "default", "%9")).toBe(false); // server not ready
  });
  it("unknown (undefined sets) never reads as ended", () => {
    expect(paneEnded(undefined, live, "s1", "default", "%9")).toBe(false);
    expect(paneEnded(ready, undefined, "s1", "default", "%9")).toBe(false);
  });
});
