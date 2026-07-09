import { describe, it, expect } from "vitest";
import { providerOf } from "@/lib/provider";

describe("providerOf", () => {
  it("maps the exact native process names", () => {
    expect(providerOf("claude")).toBe("claude");
    expect(providerOf("codex")).toBe("codex");
  });
  it("returns undefined for anything else (wrapper installs, shells, empty)", () => {
    expect(providerOf("node")).toBeUndefined();   // npm-wrapped install
    expect(providerOf("bash")).toBeUndefined();
    expect(providerOf("")).toBeUndefined();
    expect(providerOf(undefined)).toBeUndefined();
    expect(providerOf("claude-wrapper")).toBeUndefined(); // exact match only
  });
});
