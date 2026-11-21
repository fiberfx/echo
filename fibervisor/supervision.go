// SPDX-License-Identifier: MIT

package fibervisor

import (
	"io"
	"os"
	"syscall"

	"github.com/fiberfx/mx-echo/v6/observe"
)

// Supervision is the attempt a supervised service runs in, as fv names it: the trace, the attempt's path, and
// AttemptNode, fv's node id of the attempt as 16 lowercase hex digits, the value `fv show` and `fv stop` accept.
// Only Read derives it: a Supervision built by hand carries no such guarantee.
type Supervision struct {
	Trace string // the trace fv runs the attempt in, 32 lowercase hex digits
	Path  string // the attempt's path, the value Attempt returns

	// AttemptNode is the attempt's node id, the span of the TRACEPARENT fv wrote. Only Read gives a value fv
	// derived; the field holds whatever a caller sets.
	AttemptNode string

	grace stopGrace // FV_GRACE_MS as Read found it; Grace returns it
}

// Read returns the attempt fv runs the service in, and true, when TRACEPARENT, FV_PATH and FV_TRACE describe one
// fv attempt by Attempt's test; else the zero Supervision and false, never a value with an empty field. It reads
// the environment when called: a service calls it once at start and keeps the value.
//
// AttemptNode is TRACEPARENT's span, which the test holds equal to the id fv derives from the trace and the path.
func Read() (Supervision, bool) {
	traceparent := os.Getenv("TRACEPARENT")
	s := Supervision{Trace: os.Getenv("FV_TRACE"), Path: os.Getenv("FV_PATH")}
	if !oneAttempt(traceparent, s.Path, s.Trace) {
		return Supervision{}, false
	}
	s.AttemptNode = traceparent[spanStart:spanEnd]
	s.grace = graceOf(os.Getenv("FV_GRACE_MS"))
	return s, true
}

// LinesOf returns the sink Lines returns, with two keys spliced into each line before its closing brace: "trace",
// s.Trace, and "attempt_node", s.AttemptNode, so that `fv show` can be run from any line. It writes them only when
// s.Trace is 32 and s.AttemptNode 16 lowercase hex digits, the shapes fv writes, so neither value needs escaping.
// For any other s, the zero Supervision among them, it writes exactly what Lines writes. The shapes are checked,
// not the derivation: only Read gives a node fv derived. It builds the keys once: a line costs what a line of
// Lines costs.
func LinesOf(w io.Writer, s Supervision) observe.Sink {
	if !isHex(s.Trace, traceEnd-traceStart) || !isHex(s.AttemptNode, spanEnd-spanStart) {
		return Lines(w)
	}
	tail := make([]byte, 0, 64)
	tail = append(tail, `,"trace":"`...)
	tail = append(tail, s.Trace...)
	tail = append(tail, `","attempt_node":"`...)
	tail = append(tail, s.AttemptNode...)
	tail = append(tail, `"}`...)
	return &lines{w: w, tail: tail}
}

// StopSignals returns the signals fv stops a leaf with: SIGTERM, which `fv stop` sends, and SIGINT (os.Interrupt),
// which the holder of `fv run` forwards as it got it, as it does SIGTERM. A service drains on either the same way.
// Each call returns a new slice, so a caller may change it.
//
// How fv reads the end. Its rule sees whether a stop was requested and whether the exit code is 0, nothing else:
// a requested stop decides stop whatever the code, and otherwise exit 0 decides done and any other code restarts
// while restarts remain. A service that ends without being asked exits non-zero, or fv does not restart it. fv
// sends SIGKILL to the group after its grace, 500 ms unless the call names another, and does not tell the leaf
// that grace: a drain that is longer is killed.
func StopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, os.Interrupt}
}
