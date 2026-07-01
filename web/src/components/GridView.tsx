import { usePanes } from "@/store/panes";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";
import { useStateSnapshot } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import { StateDot } from "@/components/StateDot";
import { SessionNameEditor } from "@/components/SessionNameEditor";
import { usePrefs } from "@/store/prefs";
import { themeOf } from "@/lib/terminal-themes";
import { gridLayout } from "@/lib/grid-layout";

// Live tiled grid. EVERY tile stays mounted (its own WS); expand is in-state, so
// the non-focused tiles are hidden with display:none — sockets + scrollback survive.
export function GridView() {
  const { panes, focusedId, focus, collapse, closePane } = usePanes();
  // Guard against a stale focusedId pointing at a removed pane — fall back to grid view.
  const focused = panes.find((p) => p.id === focusedId);
  const activeId = focused ? focusedId : null;
  const snap = useStateSnapshot();
  const fontSize = usePrefs((s) => s.fontSizeDesktop);
  const theme = themeOf(usePrefs((s) => s.terminalTheme));
  const gridMaxColumns = usePrefs((s) => s.gridMaxColumns);

  if (panes.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Open a session from the sidebar to start a terminal.
      </div>
    );
  }

  const layout = gridLayout(panes.length, gridMaxColumns);

  return (
    <div className="relative h-full w-full">
      <div
        className="grid h-full w-full gap-2 p-2"
        style={
          activeId
            ? { gridTemplateColumns: "1fr" } // expanded: the single visible tile fills the grid
            : {
                gridTemplateColumns: `repeat(${layout.cols}, minmax(0, 1fr))`,
                gridTemplateRows: `repeat(${layout.rows}, minmax(0, 1fr))`,
              }
        }
      >
        {panes.map((p) => {
          const expanded = activeId === p.id;
          const hidden = activeId !== null && !expanded;
          return (
            <div
              // Key by the session-independent pane identity so a session RENAME
              // (which changes p.id) does NOT remount the tile and tear down its
              // WebSocket. p.id still drives focus/close/expand below.
              key={`${p.serverId}:${p.target}:${p.paneId}`}
              className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border focus-within:border-primary focus-within:ring-1 focus-within:ring-primary"
              style={{ display: hidden ? "none" : "flex" }}
            >
              <div className="flex items-center justify-between border-b border-border bg-card px-2 py-1 text-xs">
                <span className="flex min-w-0 items-center gap-1.5">
                  <StateDot state={effectiveSessionState(snap, p.serverId, p.target, p.session, p.state)} />
                  <button className="min-w-0 flex-none truncate text-left text-muted-foreground hover:underline"
                    onClick={() => (expanded ? collapse() : focus(p.id))}
                    title={expanded ? "Back to grid" : "Expand"}>
                    {p.serverName} ·
                  </button>
                  <SessionNameEditor className="min-w-0" serverId={p.serverId} target={p.target} name={p.session} paneId={p.paneId} />
                </span>
                <span className="flex flex-none items-center gap-1">
                  {expanded ? (
                    <Button variant="ghost" size="sm" onClick={() => collapse()}>⊟ grid</Button>
                  ) : (
                    <Button variant="ghost" size="sm" onClick={() => focus(p.id)} aria-label="Expand">⤢</Button>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => closePane(p.id)} aria-label="Close">✕</Button>
                </span>
              </div>
              <div className="min-h-0 flex-1 pl-2" style={{ background: theme.background }}>
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} fontSize={fontSize} theme={theme} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
