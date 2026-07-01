// A terminal pane's stable identity: server + tmux target + pane id. Deliberately
// NOT the session name (which is mutable via rename). Used to key the mobile tab
// strip and the mobile pane pool so a rename never re-identifies a pane.
export const paneIdentity = (serverId: string, target: string, paneId: string): string =>
  `${serverId}:${target}:${paneId}`;
