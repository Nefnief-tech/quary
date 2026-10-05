// Package lifecycle is the single source of truth for server power states.
// Transitions are validated here and persisted by the api/jobs layer; the
// daemon reports observed states as StatusChange events which must pass
// through the same validation.
package lifecycle

import (
	"fmt"
)

// Status is a server lifecycle state.
type Status string

const (
	Installing Status = "installing"
	Offline    Status = "offline"
	Starting   Status = "starting"
	Running    Status = "running"
	Stopping   Status = "stopping"
	Crashed    Status = "crashed"
	Suspended  Status = "suspended"
)

// All statuses, for validation of external input.
var all = []Status{Installing, Offline, Starting, Running, Stopping, Crashed, Suspended}

// Valid reports whether s is a known status.
func Valid(s string) bool {
	for _, known := range all {
		if known == Status(s) {
			return true
		}
	}
	return false
}

// transitions maps each status to the statuses it may legally move to.
var transitions = map[Status][]Status{
	Installing: {Offline, Crashed},
	Offline:    {Starting, Installing, Suspended},
	Starting:   {Running, Offline, Crashed},
	Running:    {Stopping, Crashed},
	Stopping:   {Offline, Crashed},
	Crashed:    {Starting, Offline, Installing},
	Suspended:  {Offline},
}

// CanTransition reports whether from → to is legal.
func CanTransition(from, to Status) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Transition validates from → to and returns an error describing the
// rejection when illegal.
func Transition(from, to Status) error {
	if !Valid(string(from)) || !Valid(string(to)) {
		return fmt.Errorf("unknown status (from=%q to=%q)", from, to)
	}
	if from == to {
		return nil // self-transitions (no-ops) are always fine
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("illegal transition %s → %s", from, to)
	}
	return nil
}

// PowerAction is a requested power operation.
type PowerAction string

const (
	ActionStart   PowerAction = "start"
	ActionStop    PowerAction = "stop"
	ActionRestart PowerAction = "restart"
	ActionKill    PowerAction = "kill"
)

// ActionAllowed reports whether a power action may be requested while the
// server is in the given status.
func ActionAllowed(action PowerAction, current Status) bool {
	switch action {
	case ActionStart:
		return current == Offline || current == Crashed
	case ActionStop:
		return current == Running || current == Starting || current == Stopping
	case ActionRestart:
		return current == Running || current == Crashed
	case ActionKill:
		return current == Running || current == Starting || current == Stopping || current == Crashed
	default:
		return false
	}
}

// StatusForAction is the status the server enters when the action is
// dispatched to the daemon.
func StatusForAction(action PowerAction) (Status, error) {
	switch action {
	case ActionStart, ActionRestart:
		return Starting, nil
	case ActionStop:
		return Stopping, nil
	case ActionKill:
		return Offline, nil // kill is immediate
	default:
		return "", fmt.Errorf("unknown power action %q", action)
	}
}
