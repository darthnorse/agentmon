import { describe, it, expect } from "vitest";
import { paneIdentity } from "@/lib/pane-identity";

describe("paneIdentity", () => {
  it("joins server:target:pane, independent of session name", () => {
    expect(paneIdentity("s1", "default", "%0")).toBe("s1:default:%0");
  });
});
