import { cn } from "@/lib/utils";
import { stageMeta } from "@/lib/board";

export function StageChip({ stage, className }: { stage: string; className?: string }) {
  const meta = stageMeta(stage);
  return (
    <span className={cn("inline-flex flex-none items-center gap-1.5 rounded-full border border-border bg-card px-2 py-0.5 text-[11px] font-medium", className)}>
      <span className={cn("inline-block size-1.5 rounded-full", meta.dotClass)} />
      {meta.label}
    </span>
  );
}
