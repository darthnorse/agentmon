// A terminal pane's stable identity: server + tmux target + pane id. Deliberately
// NOT the session name (which is mutable via rename). Used to key the mobile tab
// strip and the mobile pane pool so a rename never re-identifies a pane.
export const paneIdentity = (serverId: string, target: string, paneId: string): string =>
  `${serverId}:${target}:${paneId}`;

// The set of live pane identities in a flattened session-row list. Used to decide
// whether an open tile/tab's pane still exists. Rename-safe by construction (the
// identity ignores the mutable session name). Structural row type so lib code does
// not import component modules.
export const liveIdentSet = (
  rows: ReadonlyArray<{ server: { id: string }; session: { target: string }; pane: { id: string } }>,
): Set<string> => new Set(rows.map((r) => paneIdentity(r.server.id, r.session.target, r.pane.id)));
