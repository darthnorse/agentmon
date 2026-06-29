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
