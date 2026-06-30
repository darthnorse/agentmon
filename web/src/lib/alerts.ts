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
  // The blocked-only attention rule (M9). The live SSE path now uses
  // isAlertTransition (which adds the optional done-too gate); this stays as the
  // named blocked-only predicate — exactly isAlertTransition(...,false) — kept as
  // one source of truth and exercised by the M9 test suite. (No production caller
  // today; don't delete without also removing its tests.)
  return isAlertTransition(prev, next, focusedKey, key, false);
}

// Generalized alert-transition rule (M11): fires into `blocked` always, and into
// `done` only when `alertOnDone` is on. Same no-re-fire (prev !== target) and
// tab-aware (never alert the focused key) guards as the M9 blocked rule. A first
// sighting (prev === undefined) into an alerting state counts as a transition.
export function isAlertTransition(
  prev: SessionState | undefined,
  next: SessionState,
  focusedKey: string | null,
  key: string,
  alertOnDone: boolean,
): boolean {
  if (key === focusedKey) return false;
  if (next === "blocked") return prev !== "blocked";
  if (next === "done") return alertOnDone && prev !== "done";
  return false;
}

// The blocked-alert title shown in every tier — the in-app toast, the page-driven
// Notification (Tier 2), and the service-worker push notification (Tier 3). One
// source so the wording/emoji can't drift between the app bundle and the SW bundle.
// Pure + DOM-free, so the service worker can import it.
export function blockedTitle(session: string): string {
  return `🔴 ${session} needs input`;
}

// The done-alert title (M11 `prefs.alertOnDone`). Pure + DOM-free like
// `blockedTitle` so it can be shared across the app and any SW bundle.
export function doneTitle(session: string): string {
  return `✅ ${session} finished`;
}
