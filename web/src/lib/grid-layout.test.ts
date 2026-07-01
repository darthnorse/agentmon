import { describe, it, expect } from "vitest";
import { gridLayout } from "./grid-layout";

describe("gridLayout", () => {
  it("maxCols=3 gives the balanced mapping (1..6)", () => {
    expect(gridLayout(1, 3)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(2, 3)).toEqual({ cols: 2, rows: 1 });
    expect(gridLayout(3, 3)).toEqual({ cols: 3, rows: 1 });
    expect(gridLayout(4, 3)).toEqual({ cols: 2, rows: 2 }); // 2×2 square, not 3+1
    expect(gridLayout(5, 3)).toEqual({ cols: 3, rows: 2 });
    expect(gridLayout(6, 3)).toEqual({ cols: 3, rows: 2 });
  });

  it("maxCols=2 makes 2-wide stacks", () => {
    expect(gridLayout(3, 2)).toEqual({ cols: 2, rows: 2 });
    expect(gridLayout(4, 2)).toEqual({ cols: 2, rows: 2 });
    expect(gridLayout(5, 2)).toEqual({ cols: 2, rows: 3 });
    expect(gridLayout(6, 2)).toEqual({ cols: 2, rows: 3 });
  });

  it("maxCols=1 stacks vertically", () => {
    expect(gridLayout(1, 1)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(3, 1)).toEqual({ cols: 1, rows: 3 });
  });

  it("maxCols=4 lets 4 sit in one row; 6 stays 3×2", () => {
    expect(gridLayout(4, 4)).toEqual({ cols: 4, rows: 1 });
    expect(gridLayout(6, 4)).toEqual({ cols: 3, rows: 2 });
  });

  it("clamps n and maxCols to at least 1", () => {
    expect(gridLayout(0, 3)).toEqual({ cols: 1, rows: 1 });
    expect(gridLayout(3, 0)).toEqual({ cols: 1, rows: 3 });
  });
});
