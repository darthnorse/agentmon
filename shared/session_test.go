package shared

import "testing"

func TestRollUpPriority(t *testing.T) {
	cases := []struct {
		name string
		in   []State
		want State
	}{
		{"empty", nil, StateUnknown},
		{"single idle", []State{StateIdle}, StateIdle},
		{"blocked beats all", []State{StateIdle, StateWorking, StateDone, StateBlocked}, StateBlocked},
		{"done beats working", []State{StateWorking, StateDone, StateIdle}, StateDone},
		{"working beats idle", []State{StateIdle, StateWorking}, StateWorking},
		{"idle beats unknown", []State{StateUnknown, StateIdle}, StateIdle},
		{"all unknown", []State{StateUnknown, StateUnknown}, StateUnknown},
		{"unrecognized is unknown", []State{"weird"}, StateUnknown},
		{"unrecognized with idle", []State{"weird", StateIdle}, StateIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RollUp(c.in...); got != c.want {
				t.Fatalf("RollUp(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
