import type { SessionState } from "@/lib/contracts";

// Pure attention-transition rule (M8 store stays a pure reducer; detection lives here).
// Returns true iff the session is transitioning *into* `blocked` (it was not already
// blocked) for a key that is *not* the actively-viewed (focused) session — never alert
// what you're already looking at. `prev`/`next` are normalized states; `key` is the
// caller's precomputed stateKey. A first sighting (prev === undefined) that is already
// blocked counts as a transition into blocked.
export function isAttentionTransition(
  prev: SessionState | undefined,
  next: SessionState,
  focusedKey: string | null,
  key: string,
): boolean {
  return next === "blocked" && prev !== "blocked" && key !== focusedKey;
}

// The blocked-alert title shown in every tier — the in-app toast, the page-driven
// Notification (Tier 2), and the service-worker push notification (Tier 3). One
// source so the wording/emoji can't drift between the app bundle and the SW bundle.
// Pure + DOM-free, so the service worker can import it.
export function blockedTitle(session: string): string {
  return `🔴 ${session} needs input`;
}
