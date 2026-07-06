import { describe, it, expect } from "vitest";
import { paneIdentity, liveIdentSet } from "@/lib/pane-identity";

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
