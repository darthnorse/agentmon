package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"agentmon/hubd/internal/db"
)

// Default bootstrap credentials, seeded on first run (an empty DB). The login
// response flags mustChangePassword while the operator is still logging in with
// these, nudging a change — see LoginHandler.
const (
	DefaultUsername = "admin"
	DefaultPassword = "changeme123"
)

const minPasswordLen = 8

// PasswordStore is the slice of the DB the change-password handler needs.
type PasswordStore interface {
	GetUserByUsername(ctx context.Context, username string) (db.User, error)
	SetPassword(ctx context.Context, id, username, displayName, passwordHash string) error
}

// PasswordAudit records a successful password change.
type PasswordAudit interface {
	PasswordChange(ctx context.Context, principalID, ip, ua string)
}

type PasswordDeps struct {
	Users               PasswordStore
	Audit               PasswordAudit
	Store               *Store // to clear the must-change flag on the live session
	CookieName          string
	TrustForwardedProto bool
}

// ChangeHandler serves POST /api/v1/auth/password for the authenticated principal.
// It is mounted behind RequireAuth (which enforces CSRF on this POST): verify the
// current password, then store the new one. 401 on a wrong current password; 400 on
// a too-short new password.
func (d PasswordDeps) ChangeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
		if len(body.NewPassword) < minPasswordLen {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("new password must be at least %d characters", minPasswordLen))
			return
		}
		u, err := d.Users.GetUserByUsername(r.Context(), p.Username)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Bound the argon2id verify/hash through the shared semaphore (as login does),
		// so a compromised authed session can't amplify into unbounded memory/CPU use.
		verifySem <- struct{}{}
		match, _ := VerifyPassword(u.PasswordHash, body.CurrentPassword)
		<-verifySem
		if !match {
			writeErr(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		verifySem <- struct{}{}
		hash, err := HashPassword(body.NewPassword)
		<-verifySem
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if err := d.Users.SetPassword(r.Context(), u.ID, u.Username, u.DisplayName, hash); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Password is no longer the default → clear the must-change flag on this session
		// so /me stops nudging across reloads.
		if d.Store != nil {
			if c, cerr := r.Cookie(d.CookieName); cerr == nil {
				d.Store.SetMustChange(c.Value, false)
			}
		}
		d.Audit.PasswordChange(r.Context(), u.ID, ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		w.WriteHeader(http.StatusNoContent)
	}
}
