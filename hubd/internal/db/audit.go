package db

import "context"

func (d *DB) Append(ctx context.Context, e AuditEntry) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO audit_log(id, principal_id, action, resource, result, request_id, ip, user_agent, meta, ts)
		 VALUES(?,?,?,?,?,?,?,?,?, datetime('now'))`,
		e.ID, e.PrincipalID, e.Action, e.Resource, e.Result, e.RequestID, e.IP, e.UserAgent, e.Meta)
	return err
}

func (d *DB) Recent(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, COALESCE(principal_id,''), action, resource, result,
		        COALESCE(request_id,''), COALESCE(ip,''), COALESCE(user_agent,''), COALESCE(meta,'')
		 FROM audit_log ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.PrincipalID, &e.Action, &e.Resource, &e.Result,
			&e.RequestID, &e.IP, &e.UserAgent, &e.Meta); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
