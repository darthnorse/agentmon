import { describe, it, expect } from "vitest";
import { cn } from "@/lib/utils";

describe("foundation", () => {
  it("cn merges and dedupes tailwind classes", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
    expect(cn("text-sm", false && "hidden", "font-medium")).toBe("text-sm font-medium");
  });
});
