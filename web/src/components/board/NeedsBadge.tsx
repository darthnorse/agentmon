import { cn } from "@/lib/utils";

// The red "epics needing attention" count pill, shared by the home Projects
// button and the pinned-project chips so the two token strings can't drift.
// Renders nothing when the count is zero; `className` carries the positioning
// variant (inline vs absolute overlay).
export function NeedsBadge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        "flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-bold text-destructive-foreground",
        className,
      )}
    >
      {count}
    </span>
  );
}
