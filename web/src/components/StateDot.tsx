import { STATE_META } from "@/lib/state";
import type { SessionState } from "@/lib/contracts";
import { cn } from "@/lib/utils";

// A small themeable status dot. Not raw emoji (consistent render, themeable,
// assertable by aria-label). Color per §9.1.
export function StateDot({ state, className }: { state: SessionState; className?: string }) {
  const meta = STATE_META[state] ?? STATE_META.unknown; // defensive: never crash on an out-of-enum value
  return (
    <span
      role="img"
      aria-label={meta.label}
      title={meta.label}
      className={cn("inline-block size-2.5 flex-none rounded-full", meta.dotClass, className)}
    />
  );
}
