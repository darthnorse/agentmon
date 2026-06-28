package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

type UserLookup interface {
	GetUserByUsername(ctx context.Context, username string) (db.User, error)
}

type LoginDeps struct {
	Users               UserLookup
	Store               *Store
	Limiter             *Limiter
	Audit               *audit.Recorder
	CookieName          string
	CookieTTL           time.Duration
	ExternalOrigin      string
	TrustForwardedProto bool
}

// dummyHash is a valid argon2id encoding used to keep verify timing flat when the
// username does not exist (avoids a user-enumeration timing oracle).
var dummyHash, _ = HashPassword("agentmon-dummy-password")

// verifySem bounds concurrent argon2id verifications (each ~64MiB) so a burst of
// pre-auth login attempts (incl. distinct unknown usernames, which the per-username
// limiter does not throttle) cannot amplify into unbounded memory/CPU use.
var verifySem = make(chan struct{}, 4)

func (d LoginDeps) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !CheckOrigin(r, d.ExternalOrigin) {
			writeErr(w, http.StatusForbidden, "bad origin")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096) // cap pre-auth body; overflow → decode error → 400
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
		ip := clientIP(r)
		if !d.Limiter.Allowed(body.Username) {
			d.Audit.LoginFailure(r.Context(), body.Username, ip, r.UserAgent())
			writeErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		u, err := d.Users.GetUserByUsername(r.Context(), body.Username)
		hash := u.PasswordHash
		if err != nil {
			hash = dummyHash // constant-time-ish failure for unknown user
		}
		verifySem <- struct{}{}
		ok, _ := VerifyPassword(hash, body.Password)
		<-verifySem
		if err != nil || !ok || u.Status != "active" {
			d.Limiter.Fail(body.Username)
			d.Audit.LoginFailure(r.Context(), body.Username, ip, r.UserAgent())
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		d.Limiter.Reset(body.Username)
		sess, err := d.Store.New(u.ID, u.Username, u.DisplayName)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "session")
			return
		}
		SetSessionCookie(w, d.CookieName, sess.Token, SecureFromRequest(r, d.TrustForwardedProto), d.CookieTTL)
		d.Audit.LoginSuccess(r.Context(), u.ID, ip, r.UserAgent())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"principalId": u.ID, "username": u.Username,
			"displayName": u.DisplayName, "csrfToken": sess.CSRFToken,
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
