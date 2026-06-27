CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS servers (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  url TEXT NOT NULL,
  token_ref TEXT NOT NULL,
  labels TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tmux_targets (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  os_user TEXT NOT NULL,
  socket_name TEXT,
  label TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  UNIQUE(server_id, os_user, socket_name)
);

CREATE TABLE IF NOT EXISTS session_state_events (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  target_id TEXT,
  tmux_session_name TEXT NOT NULL,
  tmux_pane_id TEXT,
  source TEXT NOT NULL,
  raw_event TEXT NOT NULL,
  derived_state TEXT NOT NULL,
  payload TEXT,
  event_ts TEXT NOT NULL,
  received_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS principal_seen (
  principal_id TEXT NOT NULL,
  server_id TEXT NOT NULL,
  target_id TEXT NOT NULL DEFAULT '',
  tmux_session_name TEXT NOT NULL,
  last_seen_event_id TEXT,
  last_focused_at TEXT NOT NULL,
  PRIMARY KEY(principal_id, server_id, target_id, tmux_session_name)
);

CREATE TABLE IF NOT EXISTS audit_log (
  id TEXT PRIMARY KEY,
  principal_id TEXT,
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  result TEXT NOT NULL,
  request_id TEXT,
  ip TEXT,
  user_agent TEXT,
  meta TEXT,
  ts TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);
CREATE INDEX IF NOT EXISTS idx_state_events_session
  ON session_state_events(server_id, tmux_session_name, event_ts);
