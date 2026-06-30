import { describe, it, expect, vi, afterEach } from "vitest";
import { reloadApp } from "@/lib/pwa-update";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("reloadApp", () => {
  it("reloads immediately when there is no service worker", async () => {
    vi.stubGlobal("navigator", {});
    const reload = vi.fn();
    await reloadApp(reload);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("reloads once a freshly-activated SW takes control, and not twice", async () => {
    let onControllerChange: () => void = () => {};
    const update = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", {
      serviceWorker: {
        addEventListener: (_type: string, h: () => void) => { onControllerChange = h; },
        getRegistration: async () => ({ update }),
      },
    });
    vi.useFakeTimers();
    const reload = vi.fn();

    await reloadApp(reload);
    expect(update).toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled(); // waits for the new SW to take control

    onControllerChange?.();
    expect(reload).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(5000); // the grace-window fallback must NOT double-reload
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("falls back to a reload after the grace window when no update arrives", async () => {
    vi.stubGlobal("navigator", {
      serviceWorker: {
        addEventListener: () => {},
        getRegistration: async () => ({ update: vi.fn().mockResolvedValue(undefined) }),
      },
    });
    vi.useFakeTimers();
    const reload = vi.fn();

    await reloadApp(reload);
    expect(reload).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1200);
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
