// Feature-detected Web-Push client (M9 Tier-3 enrolment). Every browser Push API is
// behind a capability check or try/catch so unsupported browsers (iOS non-installed,
// older Safari, jsdom) fall back cleanly to Tiers 1/2 with no error path. The VAPID
// key fetch + subscription POST go through the api-client; the SW push delivery is
// handled in sw.ts.

import { getVapidPublicKey, subscribePush, unsubscribePush } from "@/lib/api-client";
import type { PushSubscriptionJSON } from "@/lib/contracts";

/** True only when service workers, the Push API and the Notification API all exist. */
export function pushSupported(): boolean {
  return (
    typeof navigator !== "undefined" &&
    "serviceWorker" in navigator &&
    typeof window !== "undefined" &&
    "PushManager" in window &&
    "Notification" in window
  );
}

/**
 * Best-effort fetch of the service-worker registration. Uses getRegistration(),
 * NOT `.ready`: `.ready` only resolves once a worker is ACTIVE and otherwise stays
 * pending forever (dev/test where registration is prod-gated, or a failed install),
 * which would hang any awaiter — the trap this single helper exists to prevent in
 * every caller (sign-out teardown + the enable-alerts flow). Resolves to the
 * registration or undefined; never throws.
 */
export async function getActiveRegistration(): Promise<ServiceWorkerRegistration | undefined> {
  try {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return undefined;
    return (await navigator.serviceWorker.getRegistration()) ?? undefined;
  } catch {
    return undefined;
  }
}

/** Convert a base64url VAPID public key to the Uint8Array applicationServerKey wants. */
export function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

/**
 * Request Notification permission, then subscribe to push and register the
 * subscription with the hub. Returns true on success; returns false (never throws)
 * when unsupported, permission denied, or any step fails.
 */
export async function enablePush(reg: ServiceWorkerRegistration): Promise<boolean> {
  try {
    if (!pushSupported()) return false;
    const permission = await Notification.requestPermission();
    if (permission !== "granted") return false;

    const { publicKey } = await getVapidPublicKey();
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      // Cast: lib.dom types applicationServerKey as BufferSource; the Uint8Array's
      // ArrayBufferLike generic doesn't structurally match without a widen.
      applicationServerKey: urlBase64ToUint8Array(publicKey) as BufferSource,
    });

    const json = sub.toJSON() as Partial<PushSubscriptionJSON>;
    const endpoint = json.endpoint;
    const p256dh = json.keys?.p256dh;
    const auth = json.keys?.auth;
    if (!endpoint || !p256dh || !auth) return false;

    await subscribePush({ endpoint, keys: { p256dh, auth } });
    return true;
  } catch {
    // best-effort: enrolment failures degrade silently to Tiers 1/2.
    return false;
  }
}

/**
 * Best-effort teardown: tell the hub to forget this subscription, then unsubscribe
 * locally. Never throws — used on sign-out and "disable alerts".
 */
export async function disablePush(reg: ServiceWorkerRegistration): Promise<void> {
  try {
    const sub = await reg.pushManager.getSubscription();
    if (!sub) return;
    try {
      await unsubscribePush(sub.endpoint);
    } catch {
      // best-effort: a failed server delete must not block local unsubscribe.
    }
    try {
      await sub.unsubscribe();
    } catch {
      // best-effort
    }
  } catch {
    // best-effort
  }
}
