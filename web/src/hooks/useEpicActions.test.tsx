import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  epicAction: vi.fn(),
  invalidateQueries: vi.fn(),
  toast: Object.assign(vi.fn(), { error: vi.fn() }),
}));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: h.toast }));

import { useEpicActions } from "@/hooks/useEpicActions";
import { ApiError } from "@/lib/api-client";

describe("useEpicActions", () => {
  beforeEach(() => { h.epicAction.mockReset(); h.invalidateQueries.mockReset(); h.toast.mockClear(); h.toast.error.mockReset(); });

  it("posts, toasts success, and invalidates board queries", async () => {
    h.epicAction.mockResolvedValue({ ok: true });
    const { result } = renderHook(() => useEpicActions("p1"));
    let ok = false;
    await act(async () => { ok = await result.current.act({ action: "approve", epic_id: "e1" }, "Approving #7"); });
    expect(ok).toBe(true);
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "approve", epic_id: "e1" });
    expect(h.toast).toHaveBeenCalledWith("Approving #7");
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["board"] });
  });

  it("surfaces typed 409 user errors verbatim", async () => {
    h.epicAction.mockRejectedValue(new ApiError(409, "epic is not escalated"));
    const { result } = renderHook(() => useEpicActions("p1"));
    let ok = true;
    await act(async () => { ok = await result.current.act({ action: "approve", epic_id: "e1" }); });
    expect(ok).toBe(false);
    expect(h.toast.error).toHaveBeenCalledWith("epic is not escalated");
  });
});
