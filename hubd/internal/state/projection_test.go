package state

import (
	"testing"

	"agentmon/shared"
)

func TestProjectionSetGetServer(t *testing.T) {
	p := NewProjection()
	p.Set(SessionView{ServerID: "s", Session: "a", Global: shared.StateBlocked, LatestReceivedAt: "t1"})
	p.Set(SessionView{ServerID: "s", Session: "b", Global: shared.StateDone, LatestReceivedAt: "t2"})
	if v, ok := p.Session("s", "", "a"); !ok || v.Global != shared.StateBlocked {
		t.Fatalf("session a: %+v ok=%v", v, ok)
	}
	if len(p.Server("s")) != 2 {
		t.Fatalf("server should have 2 sessions")
	}
}

func TestProjectionReplaceServerPrunes(t *testing.T) {
	p := NewProjection()
	p.Set(SessionView{ServerID: "s", Session: "a", Global: shared.StateDone})
	p.Set(SessionView{ServerID: "s", Session: "b", Global: shared.StateDone})
	p.ReplaceServer("s", []SessionView{{ServerID: "s", Session: "a", Global: shared.StateWorking}})
	if _, ok := p.Session("s", "", "b"); ok {
		t.Error("session b should have been pruned")
	}
	if v, _ := p.Session("s", "", "a"); v.Global != shared.StateWorking {
		t.Error("session a should be updated to working")
	}
}
