package authz

import (
	"context"
	"testing"
)

func TestAuthorizeAllowsAuthenticatedPrincipalForEveryPhase1Action(t *testing.T) {
	ctx := context.Background()
	p := Principal{ID: "u1", Username: "patrik"}
	for _, a := range []Action{ServerView, SessionView, TerminalRead, TerminalWrite, AuditRead} {
		d, err := Authorize(ctx, p, a, "server:server-a")
		if err != nil || !d.Allow {
			t.Fatalf("action %q: allow=%v err=%v", a, d.Allow, err)
		}
	}
}

func TestAuthorizeDeniesEmptyPrincipal(t *testing.T) {
	d, err := Authorize(context.Background(), Principal{}, ServerView, "server:server-a")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("empty principal must be denied")
	}
	if d.Reason != "no principal" {
		t.Fatalf("expected reason %q, got %q", "no principal", d.Reason)
	}
}
