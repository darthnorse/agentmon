import * as React from "react";
import { renameSession, ApiError, sessionsKey } from "@/lib/api-client";
import { usePanes, paneKey } from "@/store/panes";
import { queryClient } from "@/lib/query-client";
import { isValidSessionName, SESSION_NAME_HINT } from "@/lib/session-name";

interface Props {
  serverId: string;
  target: string;
  name: string;
  paneId: string;
  /** Called with the new name once the rename succeeds (e.g. to update a URL). */
  onRenamed?: (newName: string) => void;
  className?: string;
  /** If true, start in edit mode immediately. */
  autoEdit?: boolean;
  /** Called after cancel or successful save — lets a parent hide the editor. */
  onDone?: () => void;
}

// Inline session-name editor: shows the name + a pencil; click → input, Enter/✓
// saves, Esc/✕ cancels. On success it renames via the hub, re-keys the open pane
// (the WS survives — paneId is unchanged), and invalidates the sessions query. Its
// interactive controls stopPropagation so it can live inside a click-to-open row.
export function SessionNameEditor({ serverId, target, name, paneId, onRenamed, className, autoEdit, onDone }: Props) {
  const [editing, setEditing] = React.useState(!!autoEdit);
  const [value, setValue] = React.useState(name);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const inputRef = React.useRef<HTMLInputElement>(null);

  React.useEffect(() => {
    if (!editing) setValue(name);
  }, [name, editing]);
  React.useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

  const stop = (e: React.SyntheticEvent) => e.stopPropagation();
  const startEdit = (e: React.SyntheticEvent) => { stop(e); setValue(name); setError(null); setEditing(true); };
  const cancel = () => { setEditing(false); setError(null); onDone?.(); };
  const dirtyValid = isValidSessionName(value) && value !== name;

  async function save() {
    if (busy) return;
    if (value === name) { cancel(); return; }
    if (!isValidSessionName(value)) {
      setError(SESSION_NAME_HINT);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await renameSession(serverId, name, value, target);
      usePanes.getState().renamePane(paneKey(serverId, target, name, paneId), value);
      queryClient.invalidateQueries({ queryKey: sessionsKey(serverId) });
      setEditing(false);
      onRenamed?.(value);
      onDone?.();
    } catch (err) {
      const status = err instanceof ApiError ? err.status : undefined;
      setError(status === 409 ? "A session with that name already exists." : "Rename failed.");
    } finally {
      setBusy(false);
    }
  }

  if (!editing) {
    // autoEdit mode: once editing ends, onDone hands control back to the parent
    // (which unmounts this) — render nothing rather than a stray name+pencil.
    if (autoEdit) return null;
    return (
      <span className={`inline-flex min-w-0 items-center gap-1 ${className ?? ""}`}>
        <span className="truncate">{name}</span>
        <button
          type="button"
          aria-label="Rename session"
          title="Rename session"
          onClick={startEdit}
          className="flex-none rounded p-0.5 text-muted-foreground opacity-60 hover:opacity-100"
        >
          ✎
        </button>
      </span>
    );
  }

  return (
    <span className={`inline-flex min-w-0 items-center gap-1 ${className ?? ""}`} onClick={stop}>
      <input
        ref={inputRef}
        value={value}
        autoFocus
        spellCheck={false}
        autoComplete="off"
        aria-label="New session name"
        aria-invalid={!!error}
        className="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-sm"
        onClick={stop}
        onChange={(e) => { setValue(e.target.value); setError(null); }}
        onKeyDown={(e) => {
          stop(e);
          if (e.key === "Enter") void save();
          else if (e.key === "Escape") cancel();
        }}
      />
      <button
        type="button"
        aria-label="Save name"
        disabled={!dirtyValid || busy}
        onClick={(e) => { stop(e); void save(); }}
        className="flex-none rounded px-1 text-sm disabled:opacity-40"
      >
        ✓
      </button>
      <button
        type="button"
        aria-label="Cancel rename"
        onClick={(e) => { stop(e); cancel(); }}
        className="flex-none rounded px-1 text-sm text-muted-foreground"
      >
        ✕
      </button>
      {error && (
        <span role="alert" className="ml-1 truncate text-xs text-destructive">
          {error}
        </span>
      )}
    </span>
  );
}
