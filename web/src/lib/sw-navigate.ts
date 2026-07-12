import { router } from "@/router";

// The service worker posts {kind:"navigate", url} when a notification is tapped
// while the SPA is already open (openWindow can't focus-and-route an existing
// client). Parse the URL here and drive the in-app router so the epic drawer
// opens without a full reload.
export function registerSwNavigation(): void {
  if (typeof navigator === "undefined" || !navigator.serviceWorker) return;
  navigator.serviceWorker.addEventListener("message", (event: MessageEvent) => {
    const d = event.data as { kind?: string; url?: string } | undefined;
    if (!d || d.kind !== "navigate" || typeof d.url !== "string") return;
    // Use the low-level history API, NOT the typed router.navigate: the URL is
    // a runtime string (path + search), and router.navigate's `to`/`search`
    // are strictly typed to the route registry — a plain string won't satisfy
    // the "board"|"timeline" tab union. history.push takes a raw path and lets
    // the router match + parse search itself. Keep it same-origin only.
    try {
      const u = new URL(d.url, location.origin);
      if (u.origin !== location.origin) return;
      router.history.push(u.pathname + u.search);
    } catch {
      /* malformed URL from a push — ignore */
    }
  });
}
