// A tiny pane-scoped signal: "someone just (re)opened or focused this pane —
// reconnect NOW instead of waiting out the backoff". Emitters (the panes-store
// dedupe path, tile activation) know only the pane identity; the mounted
// TerminalView's socket subscribes. Module-level rather than React context
// because emitters live in plain zustand stores. Keys are paneIdentity strings.
type Kick = () => void;

const listeners = new Map<string, Set<Kick>>();

export function onReconnectKick(id: string, fn: Kick): () => void {
  let set = listeners.get(id);
  if (!set) {
    set = new Set();
    listeners.set(id, set);
  }
  set.add(fn);
  return () => {
    set.delete(fn);
    if (set.size === 0) listeners.delete(id);
  };
}

export function kickReconnect(id: string): void {
  listeners.get(id)?.forEach((fn) => fn());
}
