// Balanced grid geometry for the desktop tile view. Given n tiles and a max
// column cap, fill rows up to maxCols, then even the columns out across the
// required rows so the grid stays near-square. At maxCols=3:
//   1→1×1  2→2×1  3→3×1  4→2×2  5→3×2  6→3×2
export function gridLayout(n: number, maxCols: number): { cols: number; rows: number } {
  const cap = Math.max(1, Math.floor(maxCols));
  const count = Math.max(1, Math.floor(n));
  const rows = Math.ceil(count / cap);
  const cols = Math.ceil(count / rows);
  return { cols, rows };
}
