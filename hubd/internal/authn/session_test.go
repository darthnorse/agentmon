package authn

import (
	"testing"
	"time"
)

func TestSessionNewGetDelete(t *testing.T) {
	s := NewStore(time.Hour)
	sess, err := s.New("u1", "patrik", "Patrik")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Token == "" || sess.CSRFToken == "" || sess.Token == sess.CSRFToken {
		t.Fatalf("tokens bad: %+v", sess)
	}
	got, ok := s.Get(sess.Token)
	if !ok || got.PrincipalID != "u1" || got.Username != "patrik" {
		t.Fatalf("get: %+v ok=%v", got, ok)
	}
	s.Delete(sess.Token)
	if _, ok := s.Get(sess.Token); ok {
		t.Fatal("deleted session still present")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewStore(time.Minute)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }
	sess, _ := s.New("u1", "p", "P")
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := s.Get(sess.Token); ok {
		t.Fatal("expired session must not be returned")
	}
}
