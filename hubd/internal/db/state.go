package db

import (
	"context"
	"database/sql"
)

// StateEvent represents a row in session_state_events.
// EventTs and ReceivedAt are pre-formatted strings stamped by the caller
// (single-clock invariant: the hub stamps received_at; the agent stamps event_ts).
type StateEvent struct {
	ID, ServerID, TargetID, Session, Pane   string
	Source, RawEvent, DerivedState, Payload string
	EventTs, ReceivedAt                     string
}

// AppendStateEvent inserts a new row into session_state_events.
// EventTs and ReceivedAt are stored verbatim; no datetime('now') is used here.
func (d *DB) AppendStateEvent(ctx context.Context, e StateEvent) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO session_state_events(id, server_id, target_id, tmux_session_name, tmux_pane_id,
		    source, raw_event, derived_state, payload, event_ts, received_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.ServerID, nullIfEmpty(e.TargetID), e.Session, nullIfEmpty(e.Pane),
		e.Source, e.RawEvent, e.DerivedState, nullIfEmpty(e.Payload), e.EventTs, e.ReceivedAt)
	return err
}

// LatestSessionEvent returns the most recent event for the given server/target/session triple.
// Returns (zero, false, nil) when no rows exist.
func (d *DB) LatestSessionEvent(ctx context.Context, serverID, target, session string) (StateEvent, bool, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT id, server_id, COALESCE(target_id,''), tmux_session_name, COALESCE(tmux_pane_id,''),
		        source, raw_event, derived_state, COALESCE(payload,''), event_ts, received_at
		 FROM session_state_events
		 WHERE server_id=? AND COALESCE(target_id,'')=? AND tmux_session_name=?
		 ORDER BY received_at DESC, id DESC LIMIT 1`,
		serverID, target, session)
	var e StateEvent
	err := row.Scan(&e.ID, &e.ServerID, &e.TargetID, &e.Session, &e.Pane,
		&e.Source, &e.RawEvent, &e.DerivedState, &e.Payload, &e.EventTs, &e.ReceivedAt)
	if err == sql.ErrNoRows {
		return StateEvent{}, false, nil
	}
	if err != nil {
		return StateEvent{}, false, err
	}
	return e, true, nil
}
