import type { ITheme } from "@xterm/xterm";
import { TerminalView } from "@/components/TerminalView";
import { paneIdentity } from "@/lib/pane-identity";
import type { PoolPane } from "@/hooks/useMobilePanePool";

// Renders the mobile pane pool single-visible: every pooled pane stays mounted (its own
// socket + scrollback survive), only the focused one is shown. Mirrors GridView's
// keep-mounted trick so switching is instant with no reconnect and no cross-session bleed.
export function MobileTerminalStack({
  panes, focusedId, fontSize, theme, endedIds, onClosePane,
}: {
  panes: PoolPane[];
  focusedId: string | null;
  fontSize: number;
  theme: ITheme;
  // paneIdentity ids confirmed gone (fresh sessions fetch without the pane).
  endedIds?: Set<string>;
  onClosePane?: (id: string) => void;
}) {
  return (
    <div className="relative h-full w-full">
      {panes.map((p) => {
        const id = paneIdentity(p.serverId, p.target, p.paneId);
        const active = id === focusedId;
        return (
          <div
            key={id}
            data-pane-wrapper
            className="absolute inset-0"
            style={{ display: active ? "flex" : "none" }}
          >
            <TerminalView
              serverId={p.serverId}
              paneId={p.paneId}
              target={p.target}
              showKeyBar={active}
              active={active}
              fontSize={fontSize}
              theme={theme}
              ended={endedIds?.has(id)}
              onClose={onClosePane ? () => onClosePane(id) : undefined}
            />
          </div>
        );
      })}
    </div>
  );
}
