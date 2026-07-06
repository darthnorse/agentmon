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

// The servers whose sessions query has resolved successfully — the freshness gate
// for "confirmed gone" (an errored or not-yet-loaded list must read as unknown,
// never as ended). Positional zip: queries[i] belongs to servers[i], the
// useQueries convention both routes already rely on.
export const readyServerSet = (
  servers: ReadonlyArray<{ id: string }>,
  queries: ReadonlyArray<{ isSuccess: boolean } | undefined>,
): Set<string> => new Set(servers.filter((_, i) => queries[i]?.isSuccess).map((s) => s.id));

// The single "confirmed gone" predicate shared by the desktop grid and the mobile
// pane pool: the pane's server has a FRESH list and that list lacks the pane.
// Optional sets read as unknown → not ended (the desktop grid receives them as
// optional props).
export const paneEnded = (
  readyServers: Set<string> | undefined,
  liveIdents: Set<string> | undefined,
  serverId: string,
  target: string,
  paneId: string,
): boolean =>
  !!readyServers?.has(serverId) &&
  !!liveIdents && !liveIdents.has(paneIdentity(serverId, target, paneId));
