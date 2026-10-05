package lifecycle

import "testing"

func TestTransitionGraph(t *testing.T) {
	legal := map[Status]Status{
		Installing: Offline,
		Offline:    Starting,
		Starting:   Running,
		Running:    Stopping,
		Stopping:   Offline,
		Crashed:    Starting,
		Suspended:  Offline,
	}
	for from, to := range legal {
		if !CanTransition(from, to) {
			t.Errorf("%s → %s must be legal", from, to)
		}
		if err := Transition(from, to); err != nil {
			t.Errorf("Transition(%s, %s): %v", from, to, err)
		}
	}
}

func TestTransitionRejections(t *testing.T) {
	illegal := [][2]Status{
		{Offline, Running},    // must go through starting
		{Running, Starting},   // no direct restart
		{Starting, Suspended}, // suspension only from offline
		{Installing, Running},
		{Suspended, Running},
		{Stopping, Starting},
	}
	for _, pair := range illegal {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("%s → %s must be illegal", pair[0], pair[1])
		}
		if err := Transition(pair[0], pair[1]); err == nil {
			t.Errorf("Transition(%s, %s) must fail", pair[0], pair[1])
		}
	}
}

func TestTransitionSelfIsNoop(t *testing.T) {
	for _, s := range all {
		if err := Transition(s, s); err != nil {
			t.Errorf("self transition %s → %s must be a noop: %v", s, s, err)
		}
	}
}

func TestValid(t *testing.T) {
	for _, s := range all {
		if !Valid(string(s)) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, bad := range []string{"", "started", "ONLINE", "deleted"} {
		if Valid(bad) {
			t.Errorf("%q must not be valid", bad)
		}
	}
}

func TestActionAllowed(t *testing.T) {
	cases := []struct {
		action  PowerAction
		status  Status
		allowed bool
	}{
		{ActionStart, Offline, true},
		{ActionStart, Crashed, true},
		{ActionStart, Running, false},
		{ActionStart, Installing, false},
		{ActionStart, Suspended, false},
		{ActionStop, Running, true},
		{ActionStop, Starting, true},
		{ActionStop, Offline, false},
		{ActionRestart, Running, true},
		{ActionRestart, Crashed, true},
		{ActionRestart, Offline, false},
		{ActionKill, Running, true},
		{ActionKill, Crashed, true},
		{ActionKill, Offline, false},
	}
	for _, c := range cases {
		if got := ActionAllowed(c.action, c.status); got != c.allowed {
			t.Errorf("ActionAllowed(%s, %s) = %v, want %v", c.action, c.status, got, c.allowed)
		}
	}
}

func TestStatusForAction(t *testing.T) {
	if s, _ := StatusForAction(ActionStart); s != Starting {
		t.Error("start must lead to starting")
	}
	if s, _ := StatusForAction(ActionStop); s != Stopping {
		t.Error("stop must lead to stopping")
	}
	if s, _ := StatusForAction(ActionKill); s != Offline {
		t.Error("kill must lead straight to offline")
	}
	if _, err := StatusForAction("explode"); err == nil {
		t.Error("unknown action must error")
	}
}
