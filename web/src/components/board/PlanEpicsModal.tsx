import * as React from "react";
import { Button } from "@/components/ui/button";

interface Props {
  project: string;
  onSubmit(vibe: string): void;
  onClose(): void;
}

// Captures an optional one-line vibe to seed /plan-epics ($ARGUMENTS). Submitting
// empty is allowed — it launches the interactive brainstorm-from-scratch. Escape /
// backdrop / Cancel closes; Launch submits.
export function PlanEpicsModal({ project, onSubmit, onClose }: Props) {
  const [vibe, setVibe] = React.useState("");
  const ref = React.useRef<HTMLTextAreaElement>(null);
  React.useEffect(() => { ref.current?.focus(); }, []);
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={(e) => { e.stopPropagation(); onClose(); }}>
      <div className="w-full max-w-2xl rounded-lg border border-border bg-background p-4 shadow-lg"
        role="dialog" aria-modal="true" aria-labelledby="plan-epics-title"
        onClick={(e) => e.stopPropagation()}>
        <h2 id="plan-epics-title" className="text-base font-semibold">Plan epics — {project}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          A vibe — a line or a fuller brief — seeds the session. Leave blank to brainstorm from scratch.
        </p>
        <textarea ref={ref} value={vibe} onChange={(e) => setVibe(e.target.value)}
          rows={22} placeholder="e.g. per-project enforceable requirements injected into plan/build/review"
          className="mt-3 w-full max-h-[65vh] resize-y rounded-md border border-input bg-background p-2 text-sm" />
        <div className="mt-3 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => onSubmit(vibe)}>Launch</Button>
        </div>
      </div>
    </div>
  );
}
