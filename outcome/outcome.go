// SPDX-License-Identifier: MIT

// Package outcome names how a request ended, in the Fibervisor's failure vocabulary, and holds the status half
// of the error rule. Everything in it is a function of its arguments: it reads no clock, no environment and no
// request, and it imports only errors.
package outcome

import "errors"

// Kind is how a request ended, in the Fibervisor's failure vocabulary. The zero value is Unknown.
type Kind uint8

const (
	// Unknown means nothing is known; Classify returns it only when there was no panic, the client had not
	// gone, and the status it reads (ErrStatus, or Status when ErrStatus is 0) is below 100, none included.
	Unknown Kind = iota
	// OK means the request was answered below 400.
	OK
	// AdmissionRefusal means the request was refused with a 4xx before a handler was attributed: no route,
	// method not allowed, a pre-routing guard.
	AdmissionRefusal
	// BusinessRefusal means a 4xx on a route with a handler attributed.
	BusinessRefusal
	// WorkerFailure means a panic, or a 5xx.
	WorkerFailure
	// LostContact means the client went away before the request ended; it says nothing of the handler's effects.
	LostContact
)

// String returns "unknown", "ok", "admission-refusal", "business-refusal", "worker-failure" or "lost-contact".
// A value outside the six constants is "unknown".
func (k Kind) String() string {
	switch k {
	case OK:
		return "ok"
	case AdmissionRefusal:
		return "admission-refusal"
	case BusinessRefusal:
		return "business-refusal"
	case WorkerFailure:
		return "worker-failure"
	case LostContact:
		return "lost-contact"
	default:
		return "unknown"
	}
}

// Facts is what a caller observed about one finished request. Every field is an input; none is derived here.
type Facts struct {
	ErrStatus int  // the status the returned error states (500 for an error that states none); 0 when no error was returned
	Status    int  // the response's status as written, or as net/http will write it
	Routed    bool // a registered handler was attributed to the request
	Panicked  bool // a panic unwound through the request path
	Gone      bool // the request's own context had ended when the chain returned
}

// Classify is the pure rule from facts to a kind: Panicked, then Gone, then ErrStatus if not 0 else Status.
//
// Panicked gives WorkerFailure and Gone gives LostContact, in that order. Otherwise the status is ErrStatus, or
// Status when ErrStatus is 0: below 100 it gives Unknown, from 500 WorkerFailure, from 400 BusinessRefusal when
// Routed and AdmissionRefusal when not, and anything else OK. So the kind follows the error when there is one
// and the wire when there is none: a handler that writes 200 and then returns an error is a WorkerFailure.
func Classify(f Facts) Kind {
	if f.Panicked {
		return WorkerFailure
	}
	if f.Gone {
		return LostContact
	}
	s := f.ErrStatus
	if s == 0 {
		s = f.Status
	}
	switch {
	case s < 100:
		return Unknown
	case s >= 500:
		return WorkerFailure
	case s >= 400:
		if f.Routed {
			return BusinessRefusal
		}
		return AdmissionRefusal
	default:
		return OK
	}
}

// Decision is the status half of the error rule, as a value.
type Decision struct {
	Status int  // the status to answer with
	Write  bool // false when the response is already committed: nothing more may be written
}

// Decide maps the status an error states (0 for none) and the committed flag to a Decision: 0 becomes 500.
func Decide(code int, committed bool) Decision {
	if code == 0 {
		code = 500
	}
	return Decision{Status: code, Write: !committed}
}

// StatusOf returns the status of the first error in err's chain that has a method StatusCode() int, or 0.
// A nil error has none.
func StatusOf(err error) int {
	var coder interface{ StatusCode() int }
	if errors.As(err, &coder) {
		return coder.StatusCode()
	}
	return 0
}
