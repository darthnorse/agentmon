// Compact usage formatters for the board (epic card) and drawer surfaces.
// Exact behavior is locked by usage-format.test.ts — these are display-only,
// never used for accounting math.

export function fmtTokens(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}

// null (no priced model in the mix) renders as an em-dash, not "$0.00" — those
// mean different things (unknown cost vs. free).
export function fmtCost(c: number | null): string {
  if (c === null) return "$—";
  return `~$${c.toFixed(2)}`;
}

export function fmtDuration(ms: number): string {
  const m = Math.round(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  const r = m % 60;
  return r === 0 ? `${h}h` : `${h}h ${r}m`;
}
