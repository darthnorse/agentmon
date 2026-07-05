import type { SessionState } from "@/lib/contracts";
import type { SessionRow } from "@/components/SessionList";
import type { OpenTab } from "@/store/mobile-open-tabs";
import { StateDot } from "@/components/StateDot";
import { paneIdentity } from "@/lib/pane-identity";

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

// Build the tab list from the explicit open set, resolved against the live session rows.
// Order follows the open set (insertion order) — deliberately NOT blocked-first, so a state
// change doesn't reshuffle tabs under the user's thumb; the state dot flags attention instead.
// An open-set entry with no matching live row (list still loading, or session gone) is skipped
// — no dead tabs. The current/entered session is guaranteed present: if it isn't in the rows
// yet, a synthetic active tab is prepended (first-paint fallback).
export function buildTabs(
  openTabs: OpenTab[],
  rows: SessionRow[],
  current: CurrentSession,
  stateOf: (row: SessionRow) => SessionState,
): SessionTab[] {
  const currentId = paneIdentity(current.serverId, current.target, current.paneId);
  const rowByIdent = new Map(
    rows.map((row) => [paneIdentity(row.server.id, row.session.target, row.pane.id), row] as const),
  );
  let matched = false;
  const tabs: SessionTab[] = [];
  for (const t of openTabs) {
    const key = paneIdentity(t.serverId, t.target, t.paneId);
    const row = rowByIdent.get(key);
    if (!row) continue; // open but not live (loading or gone) → no dead tab
    const active = key === currentId;
    if (active) matched = true;
    tabs.push({
      key,
      serverId: row.server.id,
      target: row.session.target,
      // The active tab follows the URL name (source of truth mid-rename); the rest show
      // their name from the cached list.
      name: active ? current.session : row.session.name,
      paneId: row.pane.id,
      state: stateOf(row),
      active,
    });
  }
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

// Which tab should take focus when `closingKey` is closed: the right neighbor, else the left,
// else null (closing the only visible tab). Pure so the route can unit-test the active-close
// focus jump without a router.
export function nextFocusAfterClose(tabs: SessionTab[], closingKey: string): SessionTab | null {
  const i = tabs.findIndex((t) => t.key === closingKey);
  if (i === -1) return null;
  return tabs[i + 1] ?? tabs[i - 1] ?? null;
}

// Mobile terminal header tab row: state dot + session name + close (×) per tab. Tap an
// inactive tab's body to switch; tap × to remove the tab from the bar (frees its socket,
// does NOT kill the tmux session). Rename lives on the home list now, not here.
export function MobileSessionTabs({
  tabs, onSwitch, onClose,
}: {
  tabs: SessionTab[];
  onSwitch(tab: SessionTab): void;
  onClose(tab: SessionTab): void;
}) {
  return (
    <nav aria-label="Sessions" className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
      {tabs.map((tab) => (
        <span
          key={tab.key}
          aria-current={tab.active ? "page" : undefined}
          className={
            "flex flex-none items-center gap-1 rounded-md px-2 py-1 text-xs " +
            (tab.active ? "bg-accent font-semibold" : "text-muted-foreground")
          }
        >
          {tab.active ? (
            <span className="flex min-w-0 items-center gap-1">
              <StateDot state={tab.state} />
              <span className="max-w-[8rem] truncate">{tab.name}</span>
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onSwitch(tab)}
              className="flex min-w-0 items-center gap-1 hover:opacity-80"
            >
              <StateDot state={tab.state} />
              <span className="max-w-[8rem] truncate">{tab.name}</span>
            </button>
          )}
          <button
            type="button"
            aria-label={`Close ${tab.name}`}
            onClick={() => onClose(tab)}
            className="ml-0.5 flex-none rounded px-1 leading-none text-muted-foreground hover:text-foreground"
          >
            ×
          </button>
        </span>
      ))}
    </nav>
  );
}
