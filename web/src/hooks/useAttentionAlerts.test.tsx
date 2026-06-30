import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/react";

// Mock the side-effecting boundaries so the test asserts on intent, not on
// real WebAudio / router / sonner. The router's useNavigate just needs to be a
// no-op function (the toast "Open" action onClick is not exercised here).
vi.mock("sonner", () => ({ toast: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("@/lib/audio-cue", () => ({
  audioCue: { play: vi.fn(), prime: vi.fn() },
}));

import { toast } from "sonner";
import { audioCue } from "@/lib/audio-cue";
import { useAttentionAlerts } from "@/hooks/useAttentionAlerts";
import { useStateStream } from "@/hooks/useStateStream";
import { useSessionState } from "@/store/session-state";
import { usePrefs } from "@/store/prefs";
import { stateKey } from "@/lib/state";
import type { StateEventFrame } from "@/lib/contracts";

const mToast = vi.mocked(toast);
const mPlay = vi.mocked(audioCue.play);

// A fake EventSource (mirrors useStateStream.test.tsx) so we can drive deltas.
class FakeES {
  static instances: FakeES[] = [];
  listeners: Record<string, ((ev: { data: string }) => void)[]> = {};
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string, public opts?: unknown) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: (ev: { data: string }) => void) {
    (this.listeners[t] ??= []).push(fn);
  }
  close() {
    this.closed = true;
  }
  emit(t: string, data: string) {
    (this.listeners[t] ?? []).forEach((fn) => fn({ data }));
  }
}

function emitState(frame: StateEventFrame) {
  FakeES.instances[0].emit("state", JSON.stringify(frame));
}

// Mount both hooks together: useAttentionAlerts produces the onAttention handler,
// useStateStream invokes it on a pure attention-transition.
function Harness() {
  const onAttention = useAttentionAlerts();
  useStateStream(
    { EventSourceCtor: FakeES as unknown as typeof EventSource },
    onAttention,
  );
  return null;
}

let vibrate: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  FakeES.instances = [];
  useSessionState.getState().reset();
  usePrefs.getState().setAlertOnDone(false);
  vibrate = vi.fn(() => true);
  Object.defineProperty(navigator, "vibrate", { value: vibrate, configurable: true });
  // jsdom default is "visible"; keep it explicit/resettable per test.
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
});

afterEach(() => {
  try {
    delete (navigator as { vibrate?: unknown }).vibrate;
  } catch {
    /* ignore */
  }
  usePrefs.getState().setAlertOnDone(false);
  vi.unstubAllGlobals();
});

const BLOCKED: StateEventFrame = { server: "srv", target: "t1", session: "sesh", state: "blocked" };
const DONE: StateEventFrame = { server: "srv", target: "t1", session: "sesh", state: "done" };

describe("useAttentionAlerts", () => {
  it("fires toast + sound + vibrate once on a blocked transition for a non-focused key", () => {
    render(<Harness />);
    emitState(BLOCKED);

    expect(mToast).toHaveBeenCalledTimes(1);
    expect(mPlay).toHaveBeenCalledTimes(1);
    expect(vibrate).toHaveBeenCalledTimes(1);
    // Toast carries the session in the title and the server as the description.
    const [title, opts] = mToast.mock.calls[0] as [string, { description?: string; action?: unknown }];
    expect(title).toContain("sesh");
    expect(opts.description).toBe("srv");
    expect(opts.action).toBeTruthy();
  });

  it("does not fire on a non-blocked transition", () => {
    render(<Harness />);
    emitState({ ...BLOCKED, state: "working" });

    expect(mToast).not.toHaveBeenCalled();
    expect(mPlay).not.toHaveBeenCalled();
    expect(vibrate).not.toHaveBeenCalled();
  });

  it("does not fire when the blocked key is the focused session", () => {
    useSessionState.getState().setFocusedKey(stateKey("srv", "t1", "sesh"));
    render(<Harness />);
    emitState(BLOCKED);

    expect(mToast).not.toHaveBeenCalled();
    expect(mPlay).not.toHaveBeenCalled();
    expect(vibrate).not.toHaveBeenCalled();
  });

  it("does not re-fire when the session was already blocked", () => {
    render(<Harness />);
    emitState(BLOCKED);
    emitState(BLOCKED);

    expect(mToast).toHaveBeenCalledTimes(1);
    expect(mPlay).toHaveBeenCalledTimes(1);
  });

  it("raises a system Notification when hidden and permission is granted (Tier 2)", () => {
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    const NotificationMock = vi.fn() as unknown as { (...a: unknown[]): void; permission: string };
    NotificationMock.permission = "granted";
    vi.stubGlobal("Notification", NotificationMock);

    render(<Harness />);
    emitState(BLOCKED);

    expect(NotificationMock).toHaveBeenCalledTimes(1);
    const [ntitle, nopts] = (NotificationMock as unknown as { mock: { calls: unknown[][] } }).mock
      .calls[0] as [string, { body?: string; tag?: string }];
    expect(ntitle).toContain("sesh");
    expect(nopts.body).toBe("srv");
    expect(nopts.tag).toBe(stateKey("srv", "t1", "sesh"));
  });

  it("does not raise a system Notification while visible", () => {
    const NotificationMock = vi.fn() as unknown as { (...a: unknown[]): void; permission: string };
    NotificationMock.permission = "granted";
    vi.stubGlobal("Notification", NotificationMock);

    render(<Harness />);
    emitState(BLOCKED);

    expect(NotificationMock).not.toHaveBeenCalled();
    // foreground path still fired the toast.
    expect(mToast).toHaveBeenCalledTimes(1);
  });

  it("fires a 'finished' toast on a done transition when alertOnDone is on", () => {
    usePrefs.getState().setAlertOnDone(true);
    render(<Harness />);
    emitState(DONE);

    expect(mToast).toHaveBeenCalledTimes(1);
    expect(mPlay).toHaveBeenCalledTimes(1);
    const [title, opts] = mToast.mock.calls[0] as [string, { description?: string }];
    expect(title).toContain("sesh");
    expect(title).toContain("finished");
    expect(title).not.toContain("needs input");
    expect(opts.description).toBe("srv");
  });

  it("does not fire on a done transition when alertOnDone is off", () => {
    render(<Harness />); // alertOnDone defaults off
    emitState(DONE);

    expect(mToast).not.toHaveBeenCalled();
    expect(mPlay).not.toHaveBeenCalled();
  });

  it("uses the done title (not blocked) in the system Notification when hidden", () => {
    usePrefs.getState().setAlertOnDone(true);
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    const NotificationMock = vi.fn() as unknown as { (...a: unknown[]): void; permission: string };
    NotificationMock.permission = "granted";
    vi.stubGlobal("Notification", NotificationMock);

    render(<Harness />);
    emitState(DONE);

    expect(NotificationMock).toHaveBeenCalledTimes(1);
    const [ntitle] = (NotificationMock as unknown as { mock: { calls: unknown[][] } }).mock
      .calls[0] as [string];
    expect(ntitle).toContain("finished");
  });
});
