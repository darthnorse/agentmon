import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Mock the feature-detected push client and the audio cue so the component can be
// exercised in jsdom (no real ServiceWorker / PushManager / WebAudio).
vi.mock("@/lib/push", () => ({ pushSupported: vi.fn(), enablePush: vi.fn() }));
vi.mock("@/lib/audio-cue", () => ({ audioCue: { prime: vi.fn(), play: vi.fn() } }));

import { EnableAlerts } from "@/components/EnableAlerts";
import * as push from "@/lib/push";
import { audioCue } from "@/lib/audio-cue";

describe("EnableAlerts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    delete (navigator as any).serviceWorker;
  });

  it("renders nothing when push is unsupported", () => {
    (push.pushSupported as any).mockReturnValue(false);
    const { container } = render(<EnableAlerts />);
    expect(container.firstChild).toBeNull();
  });

  it("renders an enable button when push is supported", () => {
    (push.pushSupported as any).mockReturnValue(true);
    render(<EnableAlerts />);
    expect(screen.getByRole("button", { name: /enable alerts/i })).toBeInTheDocument();
  });

  it("primes audio and enables push from the click gesture", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    (push.enablePush as any).mockResolvedValue(true);
    const fakeReg = {} as ServiceWorkerRegistration;
    (navigator as any).serviceWorker = { getRegistration: () => Promise.resolve(fakeReg) };

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    expect(audioCue.prime).toHaveBeenCalled();
    await waitFor(() => expect(push.enablePush).toHaveBeenCalledWith(fakeReg));
    // success state reflected to the user
    await waitFor(() => expect(screen.getByText(/alerts on/i)).toBeInTheDocument());
  });

  it("reflects a denied/failed enrolment without throwing", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    (push.enablePush as any).mockResolvedValue(false);
    (navigator as any).serviceWorker = { getRegistration: () => Promise.resolve({} as ServiceWorkerRegistration) };

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    await waitFor(() => expect(push.enablePush).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText(/blocked/i)).toBeInTheDocument());
  });

  it("does not hang and shows blocked when no service worker is registered", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    // `.ready` would never resolve here; the component must use getRegistration(),
    // which resolves to undefined → blocked, never enablePush, never a hang.
    (navigator as any).serviceWorker = {
      ready: new Promise(() => {}),
      getRegistration: () => Promise.resolve(undefined),
    };

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    await waitFor(() => expect(screen.getByText(/blocked/i)).toBeInTheDocument());
    expect(push.enablePush).not.toHaveBeenCalled();
  });
});
