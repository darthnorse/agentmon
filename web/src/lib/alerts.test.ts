import { describe, it, expect } from "vitest";
import { isAttentionTransition, blockedTitle } from "@/lib/alerts";

const KEY = "srvdefaultsess";

describe("isAttentionTransition", () => {
  it("true when transitioning into blocked from working (non-focused)", () => {
    expect(isAttentionTransition("working", "blocked", null, KEY)).toBe(true);
  });

  it("true when transitioning into blocked from done", () => {
    expect(isAttentionTransition("done", "blocked", null, KEY)).toBe(true);
  });

  it("true when transitioning into blocked from idle", () => {
    expect(isAttentionTransition("idle", "blocked", null, KEY)).toBe(true);
  });

  it("true when transitioning into blocked from unknown", () => {
    expect(isAttentionTransition("unknown", "blocked", null, KEY)).toBe(true);
  });

  it("true when prev is undefined (first sighting is already blocked)", () => {
    expect(isAttentionTransition(undefined, "blocked", null, KEY)).toBe(true);
  });

  it("false when next is not blocked", () => {
    expect(isAttentionTransition("blocked", "working", null, KEY)).toBe(false);
    expect(isAttentionTransition("working", "done", null, KEY)).toBe(false);
    expect(isAttentionTransition("idle", "idle", null, KEY)).toBe(false);
    expect(isAttentionTransition(undefined, "working", null, KEY)).toBe(false);
  });

  it("false when prev was already blocked (no re-fire)", () => {
    expect(isAttentionTransition("blocked", "blocked", null, KEY)).toBe(false);
  });

  it("false when the key is the focused key even on a blocked transition", () => {
    expect(isAttentionTransition("working", "blocked", KEY, KEY)).toBe(false);
  });

  it("true when a different key is focused", () => {
    expect(isAttentionTransition("working", "blocked", "otherkey", KEY)).toBe(true);
  });
});

describe("blockedTitle", () => {
  it("names the session in the shared blocked-alert title", () => {
    expect(blockedTitle("api-refactor")).toBe("🔴 api-refactor needs input");
  });
});
