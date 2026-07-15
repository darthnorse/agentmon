import { describe, expect, it } from "vitest";
import { fmtCost, fmtDuration, fmtTokens } from "@/lib/usage-format";

describe("fmtTokens", () => {
  it("formats millions with 2 decimals", () => {
    expect(fmtTokens(1_240_000)).toBe("1.24M");
  });
  it("formats thousands with 1 decimal", () => {
    expect(fmtTokens(12_300)).toBe("12.3k");
  });
  it("formats sub-thousand as a plain integer string", () => {
    expect(fmtTokens(950)).toBe("950");
  });
  it("is inclusive at the M and k thresholds", () => {
    expect(fmtTokens(1_000_000)).toBe("1.00M");
    expect(fmtTokens(1_000)).toBe("1.0k");
  });
});

describe("fmtCost", () => {
  it("renders a real em-dash for null", () => {
    expect(fmtCost(null)).toBe("$—");
  });
  it("renders a tilde-prefixed two-decimal dollar amount", () => {
    expect(fmtCost(3.4)).toBe("~$3.40");
  });
  it("handles zero", () => {
    expect(fmtCost(0)).toBe("~$0.00");
  });
});

describe("fmtDuration", () => {
  it("formats sub-hour durations as minutes only", () => {
    expect(fmtDuration(2_280_000)).toBe("38m");
  });
  it("formats an exact hour with no minutes remainder", () => {
    expect(fmtDuration(3_600_000)).toBe("1h");
  });
  it("formats hours plus a minutes remainder", () => {
    expect(fmtDuration(5_400_000)).toBe("1h 30m");
  });
});
