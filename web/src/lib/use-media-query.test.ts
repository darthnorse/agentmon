import { describe, it, expect, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useMediaQuery } from "@/lib/use-media-query";

describe("useMediaQuery", () => {
  it("returns the initial match state", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: true, media: q, addEventListener: () => {}, removeEventListener: () => {},
    }));
    const { result } = renderHook(() => useMediaQuery("(min-width: 1024px)"));
    expect(result.current).toBe(true);
  });
});
