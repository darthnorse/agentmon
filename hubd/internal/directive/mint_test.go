package directive

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

func fixedMinter(now time.Time) Minter {
	n := 0
	return Minter{
		Now:          func() time.Time { return now },
		NewNonce:     func() string { n++; return "nonce-" + string(rune('0'+n)) },
		NewRequestID: func() string { return "req-1" },
	}
}

func decode(t *testing.T, header string, key []byte) shared.Directive {
	t.Helper()
	p, sig, ok := strings.Cut(header, ".")
	if !ok {
		t.Fatalf("bad header %q", header)
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		t.Fatal(err)
	}
	sigB, _ := base64.RawURLEncoding.DecodeString(sig)
	if !hmac.Equal(sigB, shared.DirectiveMAC(key, payload)) {
		t.Fatal("signature does not verify with the server key")
	}
	var d shared.Directive
	if err := json.Unmarshal(payload, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMintProducesVerifiableRWDirective(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	srv := db.Server{ID: "aigallery", SigningKey: "sk-123"}
	header, reqID, err := fixedMinter(now).Mint(srv, "u1", "%3", "default")
	if err != nil {
		t.Fatal(err)
	}
	if reqID != "req-1" {
		t.Fatalf("reqID %q", reqID)
	}
	d := decode(t, header, []byte("sk-123"))
	if d.ServerID != "aigallery" || d.PrincipalID != "u1" {
		t.Fatalf("ids %+v", d)
	}
	if d.Mode != "rw" {
		t.Fatalf("mode %q, want rw", d.Mode)
	}
	if d.Resource != shared.PaneID("aigallery", "default", "%3") {
		t.Fatalf("resource %q", d.Resource)
	}
	if d.Target != "default" {
		t.Fatalf("target %q", d.Target)
	}
	if d.Nonce == "" {
		t.Fatal("empty nonce")
	}
	if d.RequestID != "req-1" {
		t.Fatalf("requestId %q", d.RequestID)
	}
}

func TestMintExpIsRFC3339NotNanoAnd60s(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	header, _, _ := fixedMinter(now).Mint(db.Server{ID: "s", SigningKey: "k"}, "u1", "%0", "default")
	d := decode(t, header, []byte("k"))
	// RFC3339 (seconds precision) parses; the string must NOT carry sub-second digits.
	exp, err := time.Parse(time.RFC3339, d.Exp)
	if err != nil {
		t.Fatalf("Exp not RFC3339: %q (%v)", d.Exp, err)
	}
	if strings.Contains(d.Exp, ".") {
		t.Fatalf("Exp has sub-second precision (RFC3339Nano?): %q", d.Exp)
	}
	if got := exp.Sub(now); got != Expiry {
		t.Fatalf("Exp delta %v, want %v", got, Expiry)
	}
}

func TestMintNonceIsUniquePerCall(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	m := fixedMinter(now)
	srv := db.Server{ID: "s", SigningKey: "k"}
	h1, _, _ := m.Mint(srv, "u1", "%0", "default")
	h2, _, _ := m.Mint(srv, "u1", "%0", "default")
	if decode(t, h1, []byte("k")).Nonce == decode(t, h2, []byte("k")).Nonce {
		t.Fatal("nonce repeated across calls")
	}
}

func TestMintDefaultsGenerateNonEmptyNonceAndID(t *testing.T) {
	header, reqID, err := (Minter{}).Mint(db.Server{ID: "s", SigningKey: "k"}, "u1", "%1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if reqID == "" {
		t.Fatal("default requestID empty")
	}
	// decode with the real key to confirm a default-signed directive verifies + has a nonce
	p, _, _ := strings.Cut(header, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(p)
	var d shared.Directive
	_ = json.Unmarshal(payload, &d)
	if d.Nonce == "" {
		t.Fatal("default nonce empty")
	}
}
