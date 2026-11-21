// SPDX-License-Identifier: MIT

package fibervisor

import (
	"os"
	"slices"
	"strings"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// envNames are the variables of fv's frame (fv's door.rs and main.rs): first the three that place a leaf in the
// tree, then the three that say where fv keeps its records. No other FV_ variable is copied: FV_CRASH, and
// whatever fv adds later, stays out of a child's environment until a release names it here.
var envNames = [...]string{"TRACEPARENT", "FV_PATH", "FV_TRACE", "FV_STATE", "XDG_STATE_HOME", "HOME"}

// placeNames and stateNames are envNames' two halves.
var (
	placeNames = envNames[:3]
	stateNames = envNames[3:]
)

// Env returns base for a child process that fv run records as a child of this attempt: TRACEPARENT naming the
// attempt's node, FV_PATH and FV_TRACE as Read found them, and FV_STATE, XDG_STATE_HOME and HOME as this process
// has them, so the child's records reach the same state. Entries of base with these names are replaced; the zero
// Supervision removes TRACEPARENT, FV_PATH and FV_TRACE and adds nothing. base is not changed.
//
// The other entries of base keep their order, and the new ones follow them. A state variable this process does
// not have is removed from base and not added. The result is a new slice, whatever s is. A nil base is
// os.Environ(), as a nil os/exec.Cmd.Env is; a non-nil empty base stays empty but for the added names.
func (s Supervision) Env(base []string) []string {
	if !s.held() {
		return without(base, placeNames)
	}
	env := without(base, envNames[:])
	env = append(env,
		"TRACEPARENT="+s.traceparent(s.AttemptNode),
		"FV_PATH="+s.Path,
		"FV_TRACE="+s.Trace)
	return appendState(env)
}

// LinkEnv returns base for a child that fv records as a new root in the same trace, linked to the request id:
// TRACEPARENT naming RequestNode(id), no FV_PATH and no FV_TRACE, and the same state variables as Env.
//
// When RequestNode(id) is false, it returns what Env returns for the zero Supervision.
func (s Supervision) LinkEnv(base []string, id reqid.ID) []string {
	node, ok := s.RequestNode(id)
	if !ok {
		return without(base, placeNames)
	}
	env := without(base, envNames[:])
	env = append(env, "TRACEPARENT="+s.traceparent(node))
	return appendState(env)
}

// appendState appends the state variables this process has, in stateNames' order.
func appendState(env []string) []string {
	for _, name := range stateNames {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// without returns a new slice of the entries of base whose name is not one of names, in base's order, with room
// for six more; a nil base is os.Environ(). An entry with no '=' is kept.
func without(base []string, names []string) []string {
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+len(envNames))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if ok && slices.Contains(names, name) {
			continue
		}
		env = append(env, kv)
	}
	return env
}
