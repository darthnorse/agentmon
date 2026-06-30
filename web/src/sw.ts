/// <reference lib="webworker" />
// AgentMon service worker (vite-plugin-pwa `injectManifest` source).
// Owns: Workbox precache of the app shell + Web-Push display + notification focus.
import { precacheAndRoute } from "workbox-precaching";
import { stateKey } from "@/lib/state";
import { blockedTitle } from "@/lib/alerts";

// `self` is the ServiceWorkerGlobalScope here; cast rather than redeclare so this
// file still type-checks under the app's DOM-libbed tsconfig.
const sw = self as unknown as ServiceWorkerGlobalScope;

// Precache the build assets injected by vite-plugin-pwa at build time. The cast is
// stripped in the emitted JS, leaving the literal `self.__WB_MANIFEST` token that
// Workbox's injectManifest looks for.
precacheAndRoute(
  (self as unknown as { __WB_MANIFEST: Array<string | { url: string; revision: string | null }> })
    .__WB_MANIFEST,
);

// Activate updated SW immediately (registerType: 'autoUpdate' on the client side).
sw.addEventListener("install", () => {
  void sw.skipWaiting();
});
sw.addEventListener("activate", (event) => {
  event.waitUntil(sw.clients.claim());
});

interface PushPayload {
  type: string;
  server: string;
  target: string;
  session: string;
  ts?: string;
}

// Tier 3: page is dead — render a system notification from the Web-Push payload.
sw.addEventListener("push", (event) => {
  let data: PushPayload | undefined;
  try {
    data = event.data?.json() as PushPayload | undefined;
  } catch {
    data = undefined;
  }
  if (!data || data.type !== "blocked") {
    // The subscription is userVisibleOnly:true, so EVERY received push must produce a
    // visible notification or the browser may penalise/revoke the subscription. The hub
    // only sends type "blocked", but a malformed/undecryptable push must still show
    // something rather than returning silently.
    event.waitUntil(
      sw.registration.showNotification("AgentMon", { body: "An agent needs attention" }),
    );
    return;
  }
  event.waitUntil(
    sw.registration.showNotification(blockedTitle(data.session), {
      body: data.server,
      // Same canonical key the in-app Tier-2 Notification uses (stateKey, -
      // delimited), so the two tiers coalesce instead of double-notifying and distinct
      // (server,target,session) triples never collide into one tag.
      tag: stateKey(data.server, data.target, data.session),
      data,
    }),
  );
});

// Focus an existing window (or open one) when the user taps the notification.
sw.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    sw.clients.matchAll({ type: "window", includeUncontrolled: true }).then((cs) => {
      for (const c of cs) {
        if ("focus" in c) return c.focus();
      }
      return sw.clients.openWindow("/");
    }),
  );
});
