import * as React from "react";
import type { SessionState } from "@/lib/contracts";
import { Button } from "@/components/ui/button";

interface Props {
  server: string;
  name: string;
  state: SessionState;
  onConfirm(): void;
  onClose(): void;
}

// Confirmation for the irreversible kill. Escape / backdrop / Cancel closes; the
// single Kill button confirms. When the session is mid-task (working/blocked) it
// adds a warning line — a nudge, never a block.
export function KillSessionModal({ server, name, state, onConfirm, onClose }: Props) {
  const midTask = state === "working" || state === "blocked";
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="Kill session"
      onClick={onClose}
    >
      <div className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg" onClick={(e) => e.stopPropagation()}>
        <h2 className="text-base font-semibold">Kill session</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          Terminate <span className="font-medium text-foreground">{name}</span> on{" "}
          <span className="font-medium text-foreground">{server}</span>? This ends the tmux session
          and everything running in it. This can't be undone.
        </p>
        {midTask && (
          <p role="alert" className="mt-2 text-sm text-destructive">
            This agent is mid-task — killing it stops the agent.
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" onClick={onConfirm}>Kill session</Button>
        </div>
      </div>
    </div>
  );
}
