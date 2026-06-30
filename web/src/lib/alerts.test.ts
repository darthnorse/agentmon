import { describe, it, expect } from "vitest";
import { isAttentionTransition, isAlertTransition, blockedTitle, doneTitle } from "@/lib/alerts";

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

describe("isAlertTransition", () => {
  // --- blocked: identical to isAttentionTransition, regardless of alertOnDone ---
  it("true into blocked even when alertOnDone is off (blocked always alerts)", () => {
    expect(isAlertTransition("working", "blocked", null, KEY, false)).toBe(true);
  });

  it("true into blocked when alertOnDone is on", () => {
    expect(isAlertTransition("working", "blocked", null, KEY, true)).toBe(true);
  });

  it("true into blocked on a first sighting (prev undefined)", () => {
    expect(isAlertTransition(undefined, "blocked", null, KEY, false)).toBe(true);
  });

  it("false into blocked for the focused key", () => {
    expect(isAlertTransition("working", "blocked", KEY, KEY, false)).toBe(false);
  });

  it("no re-fire when already blocked", () => {
    expect(isAlertTransition("blocked", "blocked", null, KEY, true)).toBe(false);
  });

  // --- done: gated on alertOnDone ---
  it("true into done when alertOnDone is on (non-focused)", () => {
    expect(isAlertTransition("working", "done", null, KEY, true)).toBe(true);
  });

  it("true into done on a first sighting (prev undefined) when alertOnDone is on", () => {
    expect(isAlertTransition(undefined, "done", null, KEY, true)).toBe(true);
  });

  it("false into done when alertOnDone is off", () => {
    expect(isAlertTransition("working", "done", null, KEY, false)).toBe(false);
  });

  it("false into done for the focused key even when alertOnDone is on", () => {
    expect(isAlertTransition("working", "done", KEY, KEY, true)).toBe(false);
  });

  it("no re-fire when already done (alertOnDone on)", () => {
    expect(isAlertTransition("done", "done", null, KEY, true)).toBe(false);
  });

  // --- everything else never alerts ---
  it("false into working/idle/unknown regardless of alertOnDone", () => {
    expect(isAlertTransition("blocked", "working", null, KEY, true)).toBe(false);
    expect(isAlertTransition("done", "idle", null, KEY, true)).toBe(false);
    expect(isAlertTransition(undefined, "unknown", null, KEY, true)).toBe(false);
  });

  it("matches isAttentionTransition exactly when alertOnDone is false", () => {
    const cases: [import("@/lib/contracts").SessionState | undefined, import("@/lib/contracts").SessionState][] = [
      ["working", "blocked"], ["blocked", "blocked"], [undefined, "blocked"],
      ["working", "done"], ["idle", "working"], [undefined, "done"],
    ];
    for (const [prev, next] of cases) {
      expect(isAlertTransition(prev, next, "otherkey", KEY, false)).toBe(
        isAttentionTransition(prev, next, "otherkey", KEY),
      );
    }
  });
});

describe("doneTitle", () => {
  it("names the session in the shared done-alert title", () => {
    expect(doneTitle("api-refactor")).toBe("✅ api-refactor finished");
  });
});
