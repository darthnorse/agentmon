import * as React from "react";
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
import { useWindowSwitchShortcuts } from "@/hooks/useWindowSwitchShortcuts";
import { chordLabel, isMacPlatform } from "@/lib/window-shortcuts";

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
  const windowSwitchShortcut = usePrefs((s) => s.windowSwitchShortcut);
  const isMac = isMacPlatform();
  const [activeWindowId, setActiveWindowId] = React.useState<string | null>(null);
  // Bumped on every chord jump so the target terminal refocuses even when it is already
  // the active tile (e.g. focus had left the grid) — a bare setActiveWindowId to the same
  // id is a no-op then, so the nonce forces TerminalView's focus effect to re-run.
  const [focusNonce, setFocusNonce] = React.useState(0);
  const jumpToTile = React.useCallback((id: string) => {
    setActiveWindowId(id);
    setFocusNonce((n) => n + 1);
  }, []);

  // On a jump: focus the target tile; if a tile is currently expanded, the hook has
  // already moved the expansion. jumpToTile drives TerminalView's `active`/`focusNonce` focus.
  useWindowSwitchShortcuts(jumpToTile);

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
        {panes.map((p, i) => {
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
              // Sync the keyboard-focus target ONLY when focus actually lands in the
              // terminal — never for header controls (expand/close buttons, the rename
              // input). Otherwise focusing e.g. the rename input would flip `active`
              // true and TerminalView's focus effect would yank focus into the terminal.
              onFocusCapture={(e) => {
                if ((e.target as HTMLElement).closest(".xterm")) setActiveWindowId(p.id);
              }}
            >
              <div className="flex items-center justify-between border-b border-border bg-card px-2 py-1 text-xs">
                <span className="flex min-w-0 items-center gap-1.5">
                  {windowSwitchShortcut !== "off" && i < 9 && (
                    <span
                      className="flex-none rounded border border-border px-1 text-[10px] leading-none text-muted-foreground"
                      title={chordLabel(windowSwitchShortcut, isMac, i + 1)}
                      aria-hidden="true"
                    >
                      {i + 1}
                    </span>
                  )}
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
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} active={activeWindowId === p.id} focusNonce={focusNonce} fontSize={fontSize} theme={theme} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
