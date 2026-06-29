CREATE INDEX IF NOT EXISTS idx_state_events_received
  ON session_state_events(server_id, tmux_session_name, received_at DESC, id DESC);
