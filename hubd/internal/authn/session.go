package authn

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type Session struct {
	Token              string
	PrincipalID        string
	Username           string
	DisplayName        string
	CSRFToken          string
	MustChangePassword bool
	Expiry             time.Time
}

type Store struct {
	mu  sync.Mutex
	m   map[string]Session
	ttl time.Duration
	now func() time.Time
}

func NewStore(ttl time.Duration) *Store {
	return &Store{m: make(map[string]Session), ttl: ttl, now: time.Now}
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) New(principalID, username, displayName string) (Session, error) {
	tok, err := randToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randToken()
	if err != nil {
		return Session{}, err
	}
	sess := Session{
		Token: tok, PrincipalID: principalID, Username: username,
		DisplayName: displayName, CSRFToken: csrf, Expiry: s.now().Add(s.ttl),
	}
	s.mu.Lock()
	s.m[tok] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *Store) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[token]
	if !ok {
		return Session{}, false
	}
	if !s.now().Before(sess.Expiry) {
		delete(s.m, token)
		return Session{}, false
	}
	return sess, true
}

func (s *Store) Delete(token string) {
	s.mu.Lock()
	delete(s.m, token)
	s.mu.Unlock()
}

// SetMustChange updates the live session's must-change-password flag so /me reflects
// it across reloads — set at login (default creds), cleared after a password change.
func (s *Store) SetMustChange(token string, v bool) {
	s.mu.Lock()
	if sess, ok := s.m[token]; ok {
		sess.MustChangePassword = v
		s.m[token] = sess
	}
	s.mu.Unlock()
}
