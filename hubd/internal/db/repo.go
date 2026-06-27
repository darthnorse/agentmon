package db

import "context"

type User struct {
	ID           string
	Username     string
	DisplayName  string
	PasswordHash string
	Status       string
}

type AuditEntry struct {
	ID          string
	PrincipalID string
	Action      string
	Resource    string
	Result      string
	RequestID   string
	IP          string
	UserAgent   string
	Meta        string
}

type UserRepo interface {
	CreateUser(ctx context.Context, u User) error
	GetUserByUsername(ctx context.Context, username string) (User, error)
}

type AuditRepo interface {
	Append(ctx context.Context, e AuditEntry) error
	Recent(ctx context.Context, limit int) ([]AuditEntry, error)
}
