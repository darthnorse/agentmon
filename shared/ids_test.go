package shared

import "testing"

func TestPaneIDRoundTrip(t *testing.T) {
	rid := PaneID("server-a", "default", "%3")
	if rid != "pane:server-a/default/%3" {
		t.Fatalf("got %q", rid)
	}
	srv, tgt, pane, ok := ParsePaneID(rid)
	if !ok || srv != "server-a" || tgt != "default" || pane != "%3" {
		t.Fatalf("parse: srv=%q tgt=%q pane=%q ok=%v", srv, tgt, pane, ok)
	}
}

func TestSessionAndServerID(t *testing.T) {
	if got := ServerID("server-a"); got != "server:server-a" {
		t.Fatalf("ServerID=%q", got)
	}
	if got := SessionID("server-a", "default", "api-refactor"); got != "session:server-a/default/api-refactor" {
		t.Fatalf("SessionID=%q", got)
	}
}

func TestParsePaneIDRejectsJunk(t *testing.T) {
	if _, _, _, ok := ParsePaneID("session:a/b/c"); ok {
		t.Fatal("expected non-pane resource to fail")
	}
	if _, _, _, ok := ParsePaneID("pane:a/b"); ok {
		t.Fatal("expected too-few-parts to fail")
	}
}
