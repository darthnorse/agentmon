import * as React from "react";
import type { SessionState } from "@/lib/contracts";
import { SessionNameEditor } from "@/components/SessionNameEditor";
import { KillSessionModal } from "@/components/KillSessionModal";
import { killSession, ApiError, sessionsKey } from "@/lib/api-client";
import { usePanes, paneKey } from "@/store/panes";
import { queryClient } from "@/lib/query-client";
import { toast } from "sonner";

interface Props {
  serverId: string;
  serverName: string;
  target: string;
  name: string;
  paneId: string;
  state: SessionState;
}

// Per-session ⋯ overflow menu on the desktop sidebar: Rename… (reuses the inline
// editor) and Kill session… (confirmation modal). Its controls stopPropagation so
// the menu lives inside the click-to-open row.
export function SessionActionsMenu({ serverId, serverName, target, name, paneId, state }: Props) {
  const [open, setOpen] = React.useState(false);
  const [mode, setMode] = React.useState<"idle" | "rename">("idle");
  const [killOpen, setKillOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const ref = React.useRef<HTMLDivElement>(null);
  const stop = (e: React.SyntheticEvent) => e.stopPropagation();

  React.useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDoc); document.removeEventListener("keydown", onKey); };
  }, [open]);

  if (mode === "rename") {
    return (
      <SessionNameEditor serverId={serverId} target={target} name={name} paneId={paneId} autoEdit onDone={() => setMode("idle")} />
    );
  }

  async function doKill() {
    if (busy) return;
    setBusy(true);
    try {
      await killSession(serverId, name, target);
    } catch (err) {
      // 404 = already gone → treat as success; other errors: toast + keep the row.
      if (!(err instanceof ApiError && err.status === 404)) {
        toast.error(`Couldn't kill ${name}`);
        setBusy(false);
        setKillOpen(false);
        return;
      }
    }
    // Drop the session from the list + close any open tile for it.
    usePanes.getState().closePane(paneKey(serverId, target, name, paneId));
    queryClient.invalidateQueries({ queryKey: sessionsKey(serverId) });
    setBusy(false);
    setKillOpen(false);
  }

  // The outer span has NO onClick — clicks on the plain name text must bubble to
  // the sidebar row's open handler. The ⋯ button and menu items each call stop()
  // themselves, so they are already isolated.
  return (
    <span className="flex w-full min-w-0 items-center gap-1">
      <span className="truncate">{name}</span>
      <div className="relative flex-none ml-auto" ref={ref}>
        <button
          type="button"
          aria-label="Session actions"
          aria-haspopup="menu"
          onClick={(e) => { stop(e); setOpen((v) => !v); }}
          className="rounded p-0.5 text-muted-foreground opacity-60 hover:opacity-100"
        >
          ⋯
        </button>
        {open && (
          <div role="menu" className="absolute right-0 top-full z-20 mt-1 min-w-32 rounded-md border border-border bg-card py-1 shadow-md">
            <button
              type="button"
              role="menuitem"
              onClick={(e) => { stop(e); setOpen(false); setMode("rename"); }}
              className="block w-full px-3 py-1.5 text-left text-sm hover:bg-accent"
            >
              Rename…
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={(e) => { stop(e); setOpen(false); setKillOpen(true); }}
              className="block w-full px-3 py-1.5 text-left text-sm text-destructive hover:bg-accent"
            >
              Kill session…
            </button>
          </div>
        )}
      </div>
      {killOpen && (
        <KillSessionModal server={serverName} name={name} state={state} busy={busy} onConfirm={() => void doKill()} onClose={() => setKillOpen(false)} />
      )}
    </span>
  );
}
