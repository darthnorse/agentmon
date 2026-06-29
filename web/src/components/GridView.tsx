import { usePanes } from "@/store/panes";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";

// Live tiled grid. EVERY tile stays mounted (its own WS); expand is in-state, so
// the non-focused tiles are hidden with display:none — sockets + scrollback survive.
export function GridView() {
  const { panes, focusedId, focus, collapse, closePane } = usePanes();

  if (panes.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Open a session from the sidebar to start a terminal.
      </div>
    );
  }

  return (
    <div className="relative h-full w-full">
      <div
        className="grid h-full w-full gap-2 p-2"
        style={{
          gridTemplateColumns: focusedId ? "1fr" : "repeat(auto-fit, minmax(360px, 1fr))",
          // when expanded, the grid collapses to one cell; hidden tiles take no space
        }}
      >
        {panes.map((p) => {
          const expanded = focusedId === p.id;
          const hidden = focusedId !== null && !expanded;
          return (
            <div
              key={p.id}
              className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border"
              style={{ display: hidden ? "none" : "flex" }}
            >
              <div className="flex items-center justify-between border-b border-border bg-card px-2 py-1 text-xs">
                <button className="min-w-0 truncate text-left hover:underline"
                  onClick={() => (expanded ? collapse() : focus(p.id))}
                  title={expanded ? "Back to grid" : "Expand"}>
                  {p.serverName} · {p.session} · {p.paneId}
                </button>
                <span className="flex flex-none items-center gap-1">
                  {expanded ? (
                    <Button variant="ghost" size="sm" onClick={() => collapse()}>⊟ grid</Button>
                  ) : (
                    <Button variant="ghost" size="sm" onClick={() => focus(p.id)}>⤢</Button>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => closePane(p.id)} aria-label="Close">✕</Button>
                </span>
              </div>
              <div className="min-h-0 flex-1">
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
