package shared

import (
	"crypto/hmac"
	"encoding/base64"
	"strings"
	"testing"
)

func TestDirectiveCanonicalJSONStable(t *testing.T) {
	d := Directive{
		ServerID: "server-a", Target: "default",
		Resource: "pane:server-a/default/%3", Mode: "rw",
		PrincipalID: "user_1", Action: "terminal.write",
		Exp: "2026-06-27T10:32:00Z", Nonce: "n1", RequestID: "req_1",
	}
	a, err := d.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := d.CanonicalJSON()
	if string(a) != string(b) {
		t.Fatal("canonical JSON not stable across calls")
	}
	want := `{"serverId":"server-a","target":"default","resource":"pane:server-a/default/%3","mode":"rw","principalId":"user_1","action":"terminal.write","exp":"2026-06-27T10:32:00Z","nonce":"n1","requestId":"req_1"}`
	if string(a) != want {
		t.Fatalf("canonical shape changed:\n got %s\nwant %s", a, want)
	}
}

func TestSignDirectiveFormatAndMAC(t *testing.T) {
	key := []byte("server-key")
	d := Directive{ServerID: "s", Target: "default", Resource: "pane:s/default/%3",
		Mode: "rw", PrincipalID: "u1", Action: "terminal.write",
		Exp: "2026-06-28T12:01:00Z", Nonce: "n1", RequestID: "r1"}
	h, err := SignDirective(key, d)
	if err != nil {
		t.Fatal(err)
	}
	p, sig, ok := strings.Cut(h, ".")
	if !ok || p == "" || sig == "" {
		t.Fatalf("want payload.sig, got %q", h)
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		t.Fatalf("payload not base64url: %v", err)
	}
	sigB, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("sig not base64url: %v", err)
	}
	if !hmac.Equal(sigB, DirectiveMAC(key, payload)) {
		t.Fatal("sig is not DirectiveMAC(key, payload)")
	}
	want, _ := d.CanonicalJSON()
	if string(payload) != string(want) {
		t.Fatalf("payload != CanonicalJSON: %q vs %q", payload, want)
	}
}
