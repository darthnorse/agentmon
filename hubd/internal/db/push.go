package db

import (
	"context"
	"database/sql"
)

// PushSubscription represents a row in the push_subscriptions table.
// UserAgent is stored as NULL when empty and returned as "".
type PushSubscription struct {
	PrincipalID, Endpoint, P256dh, Auth, UserAgent, CreatedAt string
}

// VAPIDKeys holds the server's persisted VAPID keypair.
type VAPIDKeys struct {
	Public, Private string
}

// UpsertSubscription inserts or updates a push subscription keyed by endpoint.
// On endpoint conflict the principal, keys and user agent are updated so a
// re-subscription (or re-assignment to a different principal) replaces the row
// rather than duplicating it.
func (d *DB) UpsertSubscription(ctx context.Context, s PushSubscription) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO push_subscriptions(principal_id, endpoint, p256dh, auth, user_agent, created_at)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(endpoint)
		 DO UPDATE SET principal_id=excluded.principal_id, p256dh=excluded.p256dh, auth=excluded.auth, user_agent=excluded.user_agent`,
		s.PrincipalID, s.Endpoint, s.P256dh, s.Auth, nullIfEmpty(s.UserAgent), s.CreatedAt)
	return err
}

// ListSubscriptionsForPrincipal returns all push subscriptions for the principal.
func (d *DB) ListSubscriptionsForPrincipal(ctx context.Context, principalID string) ([]PushSubscription, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT principal_id, endpoint, p256dh, auth, COALESCE(user_agent,''), created_at
		 FROM push_subscriptions WHERE principal_id=?`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.PrincipalID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSubscription removes the subscription with the given endpoint regardless
// of owner. This is the system-initiated prune used by the push dispatcher when
// the push service reports an endpoint expired (404/410). Deleting an absent
// endpoint is a no-op (no error).
func (d *DB) DeleteSubscription(ctx context.Context, endpoint string) error {
	_, err := d.sql.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint)
	return err
}

// DeleteSubscriptionForPrincipal removes the principal's OWN subscription with
// the given endpoint. This is the user-initiated unsubscribe; scoping by
// principal_id stops a principal removing another principal's subscription by
// its (capability-URL) endpoint. Deleting an absent/foreign endpoint is a no-op.
func (d *DB) DeleteSubscriptionForPrincipal(ctx context.Context, principalID, endpoint string) error {
	_, err := d.sql.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE principal_id=? AND endpoint=?`, principalID, endpoint)
	return err
}

// PrincipalIDsWithSubscriptions returns the distinct principal IDs that have at
// least one push subscription.
func (d *DB) PrincipalIDsWithSubscriptions(ctx context.Context) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT DISTINCT principal_id FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LoadOrCreateVAPID returns the persisted VAPID keypair, generating and storing
// one on first use. gen is only called when no keypair exists yet; on every
// subsequent call the persisted keys are returned and gen is not invoked.
func (d *DB) LoadOrCreateVAPID(ctx context.Context, gen func() (priv, pub string, err error), now string) (VAPIDKeys, error) {
	var k VAPIDKeys
	err := d.sql.QueryRowContext(ctx,
		`SELECT public_key, private_key FROM push_vapid WHERE id=1`).Scan(&k.Public, &k.Private)
	if err == nil {
		return k, nil
	}
	if err != sql.ErrNoRows {
		return VAPIDKeys{}, err
	}

	priv, pub, gerr := gen()
	if gerr != nil {
		return VAPIDKeys{}, gerr
	}
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO push_vapid(id, public_key, private_key, created_at) VALUES(1, ?, ?, ?)`,
		pub, priv, now); err != nil {
		return VAPIDKeys{}, err
	}
	return VAPIDKeys{Public: pub, Private: priv}, nil
}
