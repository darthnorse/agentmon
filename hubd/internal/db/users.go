package db

import "context"

func (d *DB) CreateUser(ctx context.Context, u User) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO users(id, username, display_name, password_hash, status, created_at, updated_at)
		 VALUES(?,?,?,?,?, datetime('now'), datetime('now'))`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, u.Status)
	return err
}

func (d *DB) SetPassword(ctx context.Context, id, username, displayName, passwordHash string) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO users(id, username, display_name, password_hash, status, created_at, updated_at)
		 VALUES(?,?,?,?, 'active', datetime('now'), datetime('now'))
		 ON CONFLICT(username) DO UPDATE SET
		   display_name=excluded.display_name,
		   password_hash=excluded.password_hash,
		   updated_at=datetime('now')`,
		id, username, displayName, passwordHash)
	return err
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, status FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Status)
	return u, err
}
