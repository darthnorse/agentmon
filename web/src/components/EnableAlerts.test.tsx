import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Mock the feature-detected push client and the audio cue so the component can be
// exercised in jsdom (no real ServiceWorker / PushManager / WebAudio).
vi.mock("@/lib/push", () => ({ pushSupported: vi.fn(), enablePush: vi.fn(), disablePush: vi.fn(), getActiveRegistration: vi.fn() }));
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
    (push.getActiveRegistration as any).mockResolvedValue(fakeReg);

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    expect(audioCue.prime).toHaveBeenCalled();
    await waitFor(() => expect(push.enablePush).toHaveBeenCalledWith(fakeReg));
    // success → the toggle flips to a "Disable alerts" affordance
    await waitFor(() => expect(screen.getByRole("button", { name: /disable alerts/i })).toBeInTheDocument());
  });

  it("disables push when toggled off after being enabled", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    (push.enablePush as any).mockResolvedValue(true);
    const fakeReg = {} as ServiceWorkerRegistration;
    (push.getActiveRegistration as any).mockResolvedValue(fakeReg);
    (push.disablePush as any).mockResolvedValue(undefined);

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));
    const disableBtn = await screen.findByRole("button", { name: /disable alerts/i });

    await userEvent.click(disableBtn);
    await waitFor(() => expect(push.disablePush).toHaveBeenCalledWith(fakeReg));
    // back to the enable affordance
    await waitFor(() => expect(screen.getByRole("button", { name: /enable alerts/i })).toBeInTheDocument());
  });

  it("reflects a denied/failed enrolment without throwing", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    (push.enablePush as any).mockResolvedValue(false);
    (push.getActiveRegistration as any).mockResolvedValue({} as ServiceWorkerRegistration);

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    await waitFor(() => expect(push.enablePush).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText(/blocked/i)).toBeInTheDocument());
  });

  it("does not hang and shows blocked when no service worker is registered", async () => {
    (push.pushSupported as any).mockReturnValue(true);
    // getActiveRegistration resolves to undefined (no active SW) → blocked, never
    // enablePush, never a hang (the `.ready` trap lives behind getActiveRegistration).
    (push.getActiveRegistration as any).mockResolvedValue(undefined);

    render(<EnableAlerts />);
    await userEvent.click(screen.getByRole("button", { name: /enable alerts/i }));

    await waitFor(() => expect(screen.getByText(/blocked/i)).toBeInTheDocument());
    expect(push.enablePush).not.toHaveBeenCalled();
  });
});
