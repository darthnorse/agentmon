package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Server struct {
	ID           string
	Name         string
	Hostname     string
	URL          string
	Status       string
	Bearer       string
	SigningKey   string
	Labels       []string
	OS           string
	Arch         string
	AgentVersion string
	LastSeenAt   string
}

// marshalLabels stores nil/empty as SQL NULL; otherwise a JSON array.
func marshalLabels(l []string) any {
	if len(l) == 0 {
		return nil
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func unmarshalLabels(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s.String), &out)
	return out
}

func (d *DB) EnrollServer(ctx context.Context, s Server) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO servers(id, name, hostname, url, status, bearer, signing_key, labels, os, arch, agent_version, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		s.ID, s.Name, s.Hostname, s.URL, s.Status, s.Bearer, s.SigningKey,
		marshalLabels(s.Labels), s.OS, s.Arch, s.AgentVersion)
	return err
}

func scanServer(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var labels sql.NullString
	var os, arch, ver, lastSeen sql.NullString
	if err := row.Scan(&s.ID, &s.Name, &s.Hostname, &s.URL, &s.Status, &s.Bearer,
		&s.SigningKey, &labels, &os, &arch, &ver, &lastSeen); err != nil {
		return Server{}, err
	}
	s.Labels = unmarshalLabels(labels)
	s.OS, s.Arch, s.AgentVersion, s.LastSeenAt = os.String, arch.String, ver.String, lastSeen.String
	return s, nil
}

const serverCols = `id, name, hostname, url, status, bearer, signing_key, labels, os, arch, agent_version, last_seen_at`

func (d *DB) GetServer(ctx context.Context, id string) (Server, error) {
	return scanServer(d.sql.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id=?`, id))
}

func (d *DB) FindServer(ctx context.Context, idOrHostname string) (Server, error) {
	return scanServer(d.sql.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id=? OR hostname=? ORDER BY id LIMIT 1`,
		idOrHostname, idOrHostname))
}

func (d *DB) ListServers(ctx context.Context, status string) ([]Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id`
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SetServerStatus(ctx context.Context, id, status string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE servers SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) DeleteServer(ctx context.Context, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM servers WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ApproveIfPending flips a PENDING server to active ATOMICALLY: the status
// predicate and the update are one statement, so a concurrent revoke/rm/re-enroll
// cannot be raced into resurrecting a non-pending server. ok=false when there is no
// such id OR it isn't pending (the admit UI relies on this for its "pending-only"
// guarantee rather than a separate read-then-write).
func (d *DB) ApproveIfPending(ctx context.Context, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE servers SET status='active', updated_at=datetime('now') WHERE id=? AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RejectIfPending deletes a PENDING enrollment ATOMICALLY; it never deletes an
// active server (that is the CLI-only DeleteServer). ok=false when there is no such
// id OR it isn't pending.
func (d *DB) RejectIfPending(ctx context.Context, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM servers WHERE id=? AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) TouchServerLastSeen(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE servers SET last_seen_at=datetime('now') WHERE id=?`, id)
	return err
}
