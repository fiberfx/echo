// SPDX-License-Identifier: MIT

// Package observe holds the observation a service makes of one finished request: the value, its JSON form
// and the sink that receives it.
//
// The package is pure: it reads no clock, environment or network, and it writes nothing. An observation is
// the service's own report about itself, kept where the service chooses; it is not a supervision record.
package observe

import (
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// None is written for a fact that is absent: an absent fact is never an empty string.
const None = "none"

// Admission says where routing ended for a request. The zero value is AdmissionUnknown.
type Admission uint8

const (
	AdmissionUnknown Admission = iota // the caller cannot tell (the standard mux with no pattern)
	BeforeRouting                     // refused or answered before the router ran: no pattern, no handler
	NotFound                          // no route matched, or a registered not-found route did: no handler
	MethodNotAllowed                  // a path matched and the method did not: a pattern, no handler
	Routed                            // a registered route matched: a pattern and a handler
)

// String returns "unknown", "before-routing", "not-found", "method-not-allowed" or "routed".
// A value that is none of the constants is "unknown" too.
func (a Admission) String() string {
	switch a {
	case BeforeRouting:
		return "before-routing"
	case NotFound:
		return "not-found"
	case MethodNotAllowed:
		return "method-not-allowed"
	case Routed:
		return "routed"
	}
	return "unknown"
}

// Observation is one value per request, made after the error handler ran. It holds no body and no header value
// other than a parsed traceparent. It is not a supervision record. Every string in it may be kept.
type Observation struct {
	ID        reqid.ID     // the request's name
	Link      reqid.Link   // the incoming traceparent, or the zero Link
	Attempt   string       // the serving attempt the service stated, or None
	Build     string       // the build the service stated, or None
	Method    string       // the request's method
	Admission Admission    // where routing ended
	Pattern   string       // the registered pattern, never the concrete path; None when no path matched
	Handler   string       // the matched route's name (RouteInfo.Name); None unless Admission is Routed
	Kind      outcome.Kind // how it ended
	Status    int          // the response's status as written, or as net/http writes it when nothing was
	Committed bool         // the status line was written by the framework
	Panicked  bool         // a panic unwound through the request path; then Status is on the wire only if Committed
}

// Sink receives each observation once, by value, on the request's goroutine.
type Sink interface{ Observe(Observation) }

// SinkFunc makes a function a Sink.
type SinkFunc func(Observation)

// Observe calls f(o).
func (f SinkFunc) Observe(o Observation) { f(o) }

// Observer is the opt-in a service hands to the edge. The zero Observer is off.
type Observer struct {
	Sink    Sink            // nil turns naming and observing off
	Mint    func() reqid.ID // nil uses the edge's clock and random source
	Attempt string          // "" is reported as None
	Build   string          // "" is reported as None
}
