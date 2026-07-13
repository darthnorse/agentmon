import type { ProjectDTO } from "@/lib/contracts";
import { NeedsBadge } from "@/components/board/NeedsBadge";

// Quick-jump chips for pinned projects, shown in the home header. Pure and
// presentational (styled after ProjectSwitcher): the parent supplies the live
// project list + per-project needs counts and handles navigation, so a deleted
// or unpinned project simply drops out of `projects`.
export function PinnedProjects({ projects, needs, onOpen }: {
  projects: ProjectDTO[]; needs: Map<string, number>; onOpen(id: string): void;
}) {
  const pinned = projects.filter((p) => p.pinned);
  if (pinned.length === 0) return null;
  return (
    <div className="flex min-w-0 items-center gap-1 overflow-x-auto" aria-label="Pinned projects">
      {pinned.map((p) => {
        const n = needs.get(p.id) ?? 0;
        return (
          <button
            key={p.id}
            onClick={() => onOpen(p.id)}
            className="inline-flex shrink-0 items-center rounded-full border border-border bg-card px-3 py-1 text-xs font-medium hover:bg-accent"
          >
            {p.name}
            <NeedsBadge count={n} className="ml-1.5" />
          </button>
        );
      })}
    </div>
  );
}
