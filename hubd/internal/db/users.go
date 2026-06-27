package db

import "context"

func (d *DB) CreateUser(ctx context.Context, u User) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO users(id, username, display_name, password_hash, status, created_at, updated_at)
		 VALUES(?,?,?,?,?, datetime('now'), datetime('now'))`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, u.Status)
	return err
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, status FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Status)
	return u, err
}
