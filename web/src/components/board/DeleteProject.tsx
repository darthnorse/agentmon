import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError, allBoardKey, deleteProject } from "@/lib/api-client";
import type { ProjectDTO } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";

export function DeleteProject({ project, onDeleted, onCancel }: {
  project: ProjectDTO; onDeleted(): void; onCancel(): void;
}) {
  const [confirmName, setConfirmName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  const del = async () => {
    setBusy(true);
    setError("");
    try {
      await deleteProject(project.id);
      void queryClient.invalidateQueries({ queryKey: allBoardKey() });
      onDeleted();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mt-4 rounded-lg border border-destructive/40 p-3">
      <div className="text-sm font-semibold text-destructive">Delete project</div>
      <p className="mt-1 text-xs text-muted-foreground">
        Removes the project and its finished-epic history. Only possible when nothing is running.
        Type <span className="font-mono">{project.name}</span> to confirm.
      </p>
      <div className="mt-2 flex items-center gap-2">
        <Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} placeholder={project.name} aria-label="Confirm project name" />
        <Button variant="destructive" size="sm" disabled={busy || confirmName !== project.name} onClick={() => void del()}>
          Delete project
        </Button>
        <Button variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
      </div>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
  );
}
