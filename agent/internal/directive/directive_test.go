package directive

import (
	"testing"
	"time"

	"agentmon/shared"
)

func testKey() []byte { return []byte("super-secret-signing-key-0123456789") }

// fixedNow returns a clock pinned to t for deterministic expiry/replay tests.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func baseDirective(now time.Time) shared.Directive {
	return shared.Directive{
		ServerID:    "server-a",
		Target:      "default",
		Resource:    shared.PaneID("server-a", "default", "%3"),
		Mode:        "rw",
		PrincipalID: "user_1",
		Action:      "terminal.write",
		Exp:         now.Add(60 * time.Second).Format(time.RFC3339),
		Nonce:       "nonce-1",
		RequestID:   "req-1",
	}
}

func TestVerifyRejectsEmptyNonce(t *testing.T) {
	// The nonce is the replay-prevention primitive; an empty nonce is meaningless
	// and must be rejected as malformed rather than accepted into the replay cache
	// (where the first one would then make every later empty-nonce directive look
	// like a replay).
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	d.Nonce = ""
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for an empty nonce")
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, err := Sign(testKey(), d)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	got, err := v.Verify(hdr, d.Resource, "default")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Mode != "rw" || got.Nonce != "nonce-1" {
		t.Fatalf("verified directive wrong: %+v", got)
	}
}

func TestVerifyRejectsForgedSignature(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign([]byte("the-WRONG-key-aaaaaaaaaaaaaaaaaaaa"), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for forged signature")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now.Add(61*time.Second)))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for expired directive")
	}
}

func TestVerifyRejectsResourceMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	other := shared.PaneID("server-a", "default", "%9") // different pane
	if _, err := v.Verify(hdr, other, "default"); err == nil {
		t.Fatal("want error when resource does not match the requested pane")
	}
}

func TestVerifyRejectsTargetMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "other-target"); err == nil {
		t.Fatal("want error when target does not match")
	}
}

func TestVerifyRejectsServerMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-B", testKey(), fixedNow(now)) // agent is a different server
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error when directive serverId is not this agent's")
	}
}

func TestVerifyRejectsReplayedNonce(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err != nil {
		t.Fatalf("first use should pass: %v", err)
	}
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error on replay of the same nonce")
	}
}

func TestVerifyRejectsBadMode(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	d.Mode = "admin" // not ro|rw
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for a mode that is not ro|rw")
	}
}

func TestVerifyRejectsMalformedHeader(t *testing.T) {
	v := NewVerifier("server-a", testKey(), fixedNow(time.Now()))
	for _, h := range []string{"", "no-dot", "a.b.c", "!!!.@@@"} {
		if _, err := v.Verify(h, "pane:server-a/default/%3", "default"); err == nil {
			t.Fatalf("want error for malformed header %q", h)
		}
	}
}

func TestVerifyRejectsFarFutureExp(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	d.Exp = now.Add(2 * time.Hour).Format(time.RFC3339) // beyond the sanity cap
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for an exp further out than the max lifetime cap")
	}
}

func TestSharedSignedDirectiveVerifies(t *testing.T) {
	key := []byte("k")
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := shared.Directive{ServerID: "server-a", Target: "default",
		Resource: shared.PaneID("server-a", "default", "%3"),
		Mode:     "rw", PrincipalID: "u1", Action: "terminal.write",
		Exp:   now.Add(60 * time.Second).Format(time.RFC3339),
		Nonce: "n1", RequestID: "r1"}
	header, err := shared.SignDirective(key, d)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("server-a", key, func() time.Time { return now })
	got, err := v.Verify(header, d.Resource, d.Target)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Mode != "rw" || got.Nonce != "n1" {
		t.Fatalf("got %+v", got)
	}
}

func TestSharedSignWrongKeyRejected(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := shared.Directive{ServerID: "server-a", Target: "default",
		Resource: shared.PaneID("server-a", "default", "%3"), Mode: "rw",
		Exp: now.Add(60 * time.Second).Format(time.RFC3339), Nonce: "n2"}
	header, _ := shared.SignDirective([]byte("KEY-A"), d)
	v := NewVerifier("server-a", []byte("KEY-B"), func() time.Time { return now })
	if _, err := v.Verify(header, d.Resource, d.Target); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}
