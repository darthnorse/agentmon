package db

import (
	"context"
	"database/sql"
)

// PrincipalSeen represents a row in the principal_seen table.
// LastSeenEventID is empty string when the DB column is NULL.
// TargetID is NOT NULL DEFAULT '' — pass "" to match the PK.
type PrincipalSeen struct {
	PrincipalID, ServerID, TargetID, Session string
	LastSeenEventID, LastFocusedAt           string
}

// UpsertSeen inserts or updates a principal_seen row. On PK conflict the
// last_seen_event_id and last_focused_at columns are updated.
func (d *DB) UpsertSeen(ctx context.Context, s PrincipalSeen) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO principal_seen(principal_id, server_id, target_id, tmux_session_name, last_seen_event_id, last_focused_at)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(principal_id, server_id, target_id, tmux_session_name)
		 DO UPDATE SET last_seen_event_id=excluded.last_seen_event_id, last_focused_at=excluded.last_focused_at`,
		s.PrincipalID, s.ServerID, s.TargetID, s.Session, nullIfEmpty(s.LastSeenEventID), s.LastFocusedAt)
	return err
}

// GetSeen returns the principal_seen row for the given PK components.
// Returns (zero, false, nil) when no row exists.
func (d *DB) GetSeen(ctx context.Context, principalID, serverID, target, session string) (PrincipalSeen, bool, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT principal_id, server_id, target_id, tmux_session_name, COALESCE(last_seen_event_id,''), last_focused_at
		 FROM principal_seen WHERE principal_id=? AND server_id=? AND target_id=? AND tmux_session_name=?`,
		principalID, serverID, target, session)
	var s PrincipalSeen
	err := row.Scan(&s.PrincipalID, &s.ServerID, &s.TargetID, &s.Session, &s.LastSeenEventID, &s.LastFocusedAt)
	if err == sql.ErrNoRows {
		return PrincipalSeen{}, false, nil
	}
	if err != nil {
		return PrincipalSeen{}, false, err
	}
	return s, true, nil
}

// ListSeenForPrincipal returns all principal_seen rows for the given principal.
func (d *DB) ListSeenForPrincipal(ctx context.Context, principalID string) ([]PrincipalSeen, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT principal_id, server_id, target_id, tmux_session_name, COALESCE(last_seen_event_id,''), last_focused_at
		 FROM principal_seen WHERE principal_id=?`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrincipalSeen
	for rows.Next() {
		var s PrincipalSeen
		if err := rows.Scan(&s.PrincipalID, &s.ServerID, &s.TargetID, &s.Session, &s.LastSeenEventID, &s.LastFocusedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
