import type { SessionState } from "@/lib/contracts";

export const STATE_PRIORITY: Record<SessionState, number> = {
  blocked: 5, done: 4, working: 3, idle: 2, unknown: 1,
};

// Clamp any external/agent-supplied state string to a known union member. The SSE
// `stateEvent.State` has no omitempty and the degraded poller forwards the agent's
// raw inline state, so an empty/unexpected value can reach the wire; coercing it to
// "unknown" keeps STATE_META / STATE_PRIORITY lookups from going undefined (crash)
// or NaN (corrupted sort).
export function normalizeState(s: string | null | undefined): SessionState {
  return s != null && s in STATE_PRIORITY ? (s as SessionState) : "unknown";
}

// Roll up many states to the highest-priority one. Empty/unrecognized → "unknown".
export function rollUp(...states: SessionState[]): SessionState {
  let best: SessionState = "unknown";
  let bestP = STATE_PRIORITY.unknown;
  for (const s of states) {
    const p = STATE_PRIORITY[s];
    if (p === undefined) continue;
    if (p > bestP) { best = s; bestP = p; }
  }
  return best;
}

export interface StateMeta { label: string; dotClass: string; }
export const STATE_META: Record<SessionState, StateMeta> = {
  blocked: { label: "blocked", dotClass: "bg-red-500" },
  done:    { label: "done",    dotClass: "bg-blue-500" },
  working: { label: "working", dotClass: "bg-amber-500" },
  idle:    { label: "idle",    dotClass: "bg-green-500" },
  unknown: { label: "unknown", dotClass: "bg-zinc-400" },
};

const SEP = "\u001f"; // unit separator — never appears in a server id / target / session name
export function stateKey(server: string, target: string, session: string): string {
  return `${server}${SEP}${target}${SEP}${session}`;
}

// Only `done` is maskable: a focused or already-seen session reads idle.
export function present(state: SessionState, opts: { seen: boolean; focused: boolean }): SessionState {
  if (state === "done" && (opts.seen || opts.focused)) return "idle";
  return state;
}

export interface StateSnapshot {
  live: Map<string, SessionState>;
  seen: Set<string>;
  focusedKey: string | null;
}

export function effectiveSessionState(
  snap: StateSnapshot, server: string, target: string, session: string, fallback?: SessionState,
): SessionState {
  const key = stateKey(server, target, session);
  const raw = normalizeState(snap.live.get(key) ?? fallback);
  return present(raw, { seen: snap.seen.has(key), focused: snap.focusedKey === key });
}

// Stable sort by state priority (blocked first). Ties keep input order.
export function sortBlockedFirst<T>(items: T[], stateOf: (t: T) => SessionState): T[] {
  return items
    .map((item, i) => ({ item, i }))
    .sort((a, b) => (STATE_PRIORITY[stateOf(b.item)] - STATE_PRIORITY[stateOf(a.item)]) || (a.i - b.i))
    .map((x) => x.item);
}
