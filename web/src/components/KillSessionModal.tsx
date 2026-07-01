import * as React from "react";
import type { SessionState } from "@/lib/contracts";
import { Button } from "@/components/ui/button";

interface Props {
  server: string;
  name: string;
  state: SessionState;
  busy?: boolean;
  onConfirm(): void;
  onClose(): void;
}

// Confirmation for the irreversible kill. Escape / backdrop / Cancel closes; the
// single Kill button confirms. When the session is mid-task (working/blocked) it
// adds a warning line — a nudge, never a block.
export function KillSessionModal({ server, name, state, busy = false, onConfirm, onClose }: Props) {
  const midTask = state === "working" || state === "blocked";
  const cancelRef = React.useRef<HTMLButtonElement>(null);

  // Focus Cancel (not the destructive button) when the modal opens.
  React.useEffect(() => { cancelRef.current?.focus(); }, []);

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={(e) => { e.stopPropagation(); onClose(); }}
    >
      <div
        className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg"
        role="dialog"
        aria-modal="true"
        aria-labelledby="kill-session-title"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="kill-session-title" className="text-base font-semibold">Kill session</h2>
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
          <Button ref={cancelRef} variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" disabled={busy} onClick={onConfirm}>Kill session</Button>
        </div>
      </div>
    </div>
  );
}
