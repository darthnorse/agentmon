import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// The push client posts subscriptions through the api-client; mock that boundary.
vi.mock("@/lib/api-client", () => ({
  getVapidPublicKey: vi.fn(),
  subscribePush: vi.fn(),
  unsubscribePush: vi.fn(),
}));

import { getVapidPublicKey, subscribePush, unsubscribePush } from "@/lib/api-client";
import {
  pushSupported,
  enablePush,
  disablePush,
  urlBase64ToUint8Array,
  getActiveRegistration,
} from "@/lib/push";

const mGetVapid = vi.mocked(getVapidPublicKey);
const mSubscribe = vi.mocked(subscribePush);
const mUnsubscribe = vi.mocked(unsubscribePush);

function enableSupport() {
  vi.stubGlobal("PushManager", class {});
  vi.stubGlobal("Notification", class {});
  Object.defineProperty(navigator, "serviceWorker", {
    value: {},
    configurable: true,
  });
}

function clearSupport() {
  try {
    delete (navigator as { serviceWorker?: unknown }).serviceWorker;
  } catch {
    /* ignore */
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
  clearSupport();
});
afterEach(() => {
  vi.unstubAllGlobals();
  clearSupport();
});

describe("getActiveRegistration", () => {
  it("returns undefined when serviceWorker is unavailable (no hang)", async () => {
    clearSupport();
    await expect(getActiveRegistration()).resolves.toBeUndefined();
  });

  it("returns the registration from getRegistration() (not .ready)", async () => {
    const reg = {} as ServiceWorkerRegistration;
    Object.defineProperty(navigator, "serviceWorker", {
      // `.ready` never resolves — getActiveRegistration must not await it.
      value: { ready: new Promise(() => {}), getRegistration: () => Promise.resolve(reg) },
      configurable: true,
    });
    await expect(getActiveRegistration()).resolves.toBe(reg);
  });

  it("swallows a throwing getRegistration and returns undefined", async () => {
    Object.defineProperty(navigator, "serviceWorker", {
      value: {
        getRegistration: () => {
          throw new Error("boom");
        },
      },
      configurable: true,
    });
    await expect(getActiveRegistration()).resolves.toBeUndefined();
  });
});

describe("pushSupported", () => {
  it("is false when the push APIs are absent (jsdom default)", () => {
    expect(pushSupported()).toBe(false);
  });

  it("is true when serviceWorker, PushManager and Notification are present", () => {
    enableSupport();
    expect(pushSupported()).toBe(true);
  });

  it("is false when only some APIs are present", () => {
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("Notification", class {});
    // no navigator.serviceWorker
    expect(pushSupported()).toBe(false);
  });
});

describe("urlBase64ToUint8Array", () => {
  it("round-trips a known base64url vector (with - and _ and missing padding)", () => {
    // bytes 0xFB 0xFF 0xBF -> base64 "+/+/" -> base64url "-_-_"
    const out = urlBase64ToUint8Array("-_-_");
    expect(Array.from(out)).toEqual([0xfb, 0xff, 0xbf]);
  });

  it("decodes a padded standard base64url string", () => {
    // "AAEC" -> bytes 0x00 0x01 0x02
    const out = urlBase64ToUint8Array("AAEC");
    expect(Array.from(out)).toEqual([0x00, 0x01, 0x02]);
  });
});

describe("enablePush", () => {
  function mockReg() {
    const subJSON = {
      endpoint: "https://push.example/abc",
      keys: { p256dh: "pk", auth: "au" },
    };
    const subscribe = vi.fn(async () => ({ toJSON: () => subJSON }));
    return { reg: { pushManager: { subscribe } } as unknown as ServiceWorkerRegistration, subscribe, subJSON };
  }

  it("subscribes and posts the subscription when permission is granted", async () => {
    enableSupport();
    vi.stubGlobal("Notification", { requestPermission: vi.fn(async () => "granted") });
    mGetVapid.mockResolvedValue({ publicKey: "AAEC" });
    mSubscribe.mockResolvedValue(undefined);
    const { reg, subscribe, subJSON } = mockReg();

    const ok = await enablePush(reg);

    expect(ok).toBe(true);
    expect(subscribe).toHaveBeenCalledOnce();
    const opts = (subscribe.mock.calls[0] as unknown[])[0] as {
      userVisibleOnly: boolean;
      applicationServerKey: Uint8Array;
    };
    expect(opts.userVisibleOnly).toBe(true);
    expect(Array.from(opts.applicationServerKey)).toEqual([0x00, 0x01, 0x02]);
    expect(mSubscribe).toHaveBeenCalledWith({
      endpoint: subJSON.endpoint,
      keys: { p256dh: "pk", auth: "au" },
    });
  });

  it("returns false (no throw) when permission is denied", async () => {
    enableSupport();
    vi.stubGlobal("Notification", { requestPermission: vi.fn(async () => "denied") });
    const { reg, subscribe } = mockReg();

    const ok = await enablePush(reg);

    expect(ok).toBe(false);
    expect(subscribe).not.toHaveBeenCalled();
    expect(mSubscribe).not.toHaveBeenCalled();
  });

  it("returns false when push is unsupported", async () => {
    // no enableSupport(): APIs absent
    const { reg } = mockReg();
    const ok = await enablePush(reg);
    expect(ok).toBe(false);
    expect(mGetVapid).not.toHaveBeenCalled();
  });

  it("returns false (swallows) when subscribe throws", async () => {
    enableSupport();
    vi.stubGlobal("Notification", { requestPermission: vi.fn(async () => "granted") });
    mGetVapid.mockResolvedValue({ publicKey: "AAEC" });
    const subscribe = vi.fn(async () => {
      throw new Error("subscribe failed");
    });
    const reg = { pushManager: { subscribe } } as unknown as ServiceWorkerRegistration;

    const ok = await enablePush(reg);
    expect(ok).toBe(false);
    expect(mSubscribe).not.toHaveBeenCalled();
  });
});

describe("disablePush", () => {
  it("unsubscribes the existing subscription and calls the API", async () => {
    const unsubscribe = vi.fn(async () => true);
    const sub = { endpoint: "https://push.example/abc", unsubscribe };
    const getSubscription = vi.fn(async () => sub);
    const reg = { pushManager: { getSubscription } } as unknown as ServiceWorkerRegistration;
    mUnsubscribe.mockResolvedValue(undefined);

    await disablePush(reg);

    expect(mUnsubscribe).toHaveBeenCalledWith("https://push.example/abc");
    expect(unsubscribe).toHaveBeenCalledOnce();
  });

  it("no-ops when there is no existing subscription", async () => {
    const getSubscription = vi.fn(async () => null);
    const reg = { pushManager: { getSubscription } } as unknown as ServiceWorkerRegistration;

    await expect(disablePush(reg)).resolves.toBeUndefined();
    expect(mUnsubscribe).not.toHaveBeenCalled();
  });

  it("never throws when the API call fails", async () => {
    const unsubscribe = vi.fn(async () => true);
    const sub = { endpoint: "https://push.example/abc", unsubscribe };
    const getSubscription = vi.fn(async () => sub);
    const reg = { pushManager: { getSubscription } } as unknown as ServiceWorkerRegistration;
    mUnsubscribe.mockRejectedValue(new Error("network"));

    await expect(disablePush(reg)).resolves.toBeUndefined();
    // best-effort: local unsubscribe still attempted
    expect(unsubscribe).toHaveBeenCalledOnce();
  });
});
