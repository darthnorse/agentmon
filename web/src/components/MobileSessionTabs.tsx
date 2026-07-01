import type { SessionState } from "@/lib/contracts";
import type { SessionRow } from "@/components/SessionList";
import { StateDot } from "@/components/StateDot";
import { SessionNameEditor } from "@/components/SessionNameEditor";

// One tab per session (§ mobile session switcher). Kept flat/serializable so the
// route can build it from the cached session list and unit tests can assert on it.
export interface SessionTab {
  key: string;
  serverId: string;
  target: string;
  name: string;
  paneId: string;
  state: SessionState;
  active: boolean;
}

// The session currently open in the terminal (from the URL) — always shown as the
// active tab even before the session list has loaded (e.g. a hard reload straight
// onto a terminal URL), so the header is never momentarily empty.
export interface CurrentSession {
  serverId: string;
  target: string;
  session: string;
  paneId: string;
}

// A tab's identity is (server, target, pane) — deliberately NOT the session name.
// The name is mutable (rename), and after a rename the URL advances to the new name
// before the cached session list refetches; matching on the name would then briefly
// show TWO tabs for one pane (a synthetic new-name tab + the stale old-name row). Keying
// on the immutable pane identity keeps exactly one active tab across a rename, and lets
// the terminal survive a rename without a remount — the same discipline the grid uses.
const tabIdentity = (serverId: string, target: string, paneId: string) =>
  `${serverId}:${target}:${paneId}`;

// Build the tab list from the flattened session rows. STABLE order (rows are already
// in server/session order — deliberately NOT blocked-first, so a state change doesn't
// reshuffle tabs under the user's thumb; the state dot flags attention instead).
// Guarantees the current session appears: if the list hasn't loaded it yet, a synthetic
// active tab is prepended.
export function buildTabs(
  rows: SessionRow[],
  current: CurrentSession,
  stateOf: (row: SessionRow) => SessionState,
): SessionTab[] {
  const currentId = tabIdentity(current.serverId, current.target, current.paneId);
  let matched = false;
  const tabs: SessionTab[] = rows.map((row) => {
    const key = tabIdentity(row.server.id, row.session.target, row.pane.id);
    const active = key === currentId;
    if (active) matched = true;
    return {
      key,
      serverId: row.server.id,
      target: row.session.target,
      // The active tab follows the URL name (source of truth mid-rename); the rest show
      // their name from the cached list.
      name: active ? current.session : row.session.name,
      paneId: row.pane.id,
      state: stateOf(row),
      active,
    };
  });
  if (!matched) {
    tabs.unshift({
      key: currentId,
      serverId: current.serverId,
      target: current.target,
      name: current.session,
      paneId: current.paneId,
      state: "unknown",
      active: true,
    });
  }
  return tabs;
}

// Mobile terminal header tab row: state dot + session name per tab (no host name).
// Tap an inactive tab to switch sessions; the active tab's label is the inline rename
// editor (so tap-to-rename is preserved for the session you're actually on).
export function MobileSessionTabs({
  tabs, onSwitch, onRenamed,
}: {
  tabs: SessionTab[];
  onSwitch(tab: SessionTab): void;
  onRenamed(newName: string): void;
}) {
  return (
    <nav aria-label="Sessions" className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
      {tabs.map((tab) =>
        tab.active ? (
          <span
            key={tab.key}
            aria-current="page"
            className="flex flex-none items-center gap-1 rounded-md bg-accent px-2 py-1 text-xs font-semibold"
          >
            <StateDot state={tab.state} />
            <SessionNameEditor
              className="min-w-0"
              serverId={tab.serverId}
              target={tab.target}
              name={tab.name}
              paneId={tab.paneId}
              onRenamed={onRenamed}
            />
          </span>
        ) : (
          <button
            key={tab.key}
            type="button"
            onClick={() => onSwitch(tab)}
            className="flex flex-none items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent/50"
          >
            <StateDot state={tab.state} />
            <span className="max-w-[8rem] truncate">{tab.name}</span>
          </button>
        ),
      )}
    </nav>
  );
}
