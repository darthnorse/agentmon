import type { SessionRow } from "@/components/SessionList";
import type { SessionState } from "@/lib/contracts";
import { sortBlockedFirst, stateKey } from "@/lib/state";

// Pure focus-next helper. Given the session rows and the currently-focused key
// (a `stateKey`, or null), return the next `blocked` row to jump to — in
// blocked-first order, wrapping past the end, and *excluding* the current row so
// repeated calls cycle through every blocked session. Returns null when nothing
// is blocked (or there are no rows). When `currentKey` matches no row (e.g. the
// focused pane was closed), it falls back to the first blocked row.
export function nextBlocked(
  rows: SessionRow[],
  stateOf: (row: SessionRow) => SessionState,
  currentKey: string | null,
): SessionRow | null {
  const ordered = sortBlockedFirst(rows, stateOf);
  const n = ordered.length;
  if (n === 0) return null;

  const keyOf = (r: SessionRow) => stateKey(r.server.id, r.session.target, r.session.name);
  const startIdx = currentKey == null ? -1 : ordered.findIndex((r) => keyOf(r) === currentKey);
  const found = startIdx >= 0;
  // When the current row is in the list we scan the OTHER n-1 rows (skip self);
  // otherwise we scan all n rows starting from index 0.
  const span = found ? n - 1 : n;
  const base = found ? startIdx : -1;

  for (let i = 1; i <= span; i++) {
    const r = ordered[(base + i) % n];
    if (stateOf(r) === "blocked") return r;
  }
  return null;
}
