package shared

import "testing"

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
