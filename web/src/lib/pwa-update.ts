// A standalone PWA has no browser refresh / pull-to-refresh, so this is the in-app
// "get the latest deployed version" path. It asks the service worker to check for a
// new sw.js; our SW skipWaiting()s on install, so a found update activates and fires
// `controllerchange` → we reload to the fresh precached assets. If there's no update
// (or no SW at all), it falls back to a plain reload after a short grace window.
//
// `reload` is injectable for tests; production uses window.location.reload().
export async function reloadApp(reload: () => void = () => window.location.reload()): Promise<void> {
  const swc = typeof navigator !== "undefined" ? navigator.serviceWorker : undefined;
  if (!swc) {
    reload();
    return;
  }

  let done = false;
  const fire = () => {
    if (!done) {
      done = true;
      reload();
    }
  };
  // A newly-activated SW taking control means fresh assets are precached → reload.
  swc.addEventListener("controllerchange", fire, { once: true });
  try {
    const reg = await swc.getRegistration();
    await reg?.update();
  } catch {
    // ignore — the grace-window fallback below still reloads.
  }
  // No update arrived (or no SW change): reload the current version anyway so the
  // button always does something visible.
  setTimeout(fire, 1200);
}
