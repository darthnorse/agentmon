import type { ProjectDTO } from "@/lib/contracts";

export function ProjectSwitcher({ projects, needs, current, onSelect }: {
  projects: ProjectDTO[]; needs: Map<string, number>; current?: string;
  onSelect(id: string | null): void;
}) {
  return (
    <select
      aria-label="Project"
      value={current ?? ""}
      onChange={(e) => onSelect(e.target.value || null)}
      className="h-8 rounded-md border border-input bg-background px-2 text-sm"
    >
      <option value="">All projects</option>
      {projects.map((p) => {
        const n = needs.get(p.id) ?? 0;
        return (
          <option key={p.id} value={p.id}>
            {p.name}{n > 0 ? ` (${n}!)` : ""}
          </option>
        );
      })}
    </select>
  );
}
