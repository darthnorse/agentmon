import { describe, expect, it } from "vitest";
import { shSingleQuote, planCommand } from "@/lib/shell-quote";

describe("shSingleQuote", () => {
  it("wraps plain text", () => expect(shSingleQuote("hi there")).toBe("'hi there'"));
  it("escapes single quotes", () => expect(shSingleQuote("it's")).toBe("'it'\\''s'"));
  it("neutralises $ and backticks", () => expect(shSingleQuote("$PATH `x`")).toBe("'$PATH `x`'"));
  it("empty → ''", () => expect(shSingleQuote("")).toBe("''"));
});

describe("planCommand", () => {
  it("empty vibe → bare slash command (claude)", () =>
    expect(planCommand("claude", "  ")).toBe(`IS_SANDBOX=1 claude --dangerously-skip-permissions "/plan-epics"`));
  it("seeds the vibe as $ARGUMENTS (claude)", () =>
    expect(planCommand("claude", "add dark mode")).toBe(
      `IS_SANDBOX=1 claude --dangerously-skip-permissions '/plan-epics add dark mode'`));
  it("codex variant seeds too", () =>
    expect(planCommand("codex", "add dark mode")).toBe(`codex -a never '/plan-epics add dark mode'`));
  it("a vibe with a quote stays shell-safe", () =>
    expect(planCommand("codex", "it's x")).toBe(`codex -a never '/plan-epics it'\\''s x'`));
});
