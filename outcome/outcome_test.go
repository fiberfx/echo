// SPDX-License-Identifier: MIT

package outcome_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v6/outcome"
)

// coder is an error that states a status, as the root's *HTTPError does.
type coder struct{ code int }

func (c coder) Error() string   { return "coder" }
func (c coder) StatusCode() int { return c.code }

func checkClassify(f outcome.Facts, want outcome.Kind) error {
	if got := outcome.Classify(f); got != want {
		return fmt.Errorf("Classify(%+v) = %s, want %s", f, got, want)
	}
	return nil
}

func checkDecide(code int, committed bool, want outcome.Decision) error {
	if got := outcome.Decide(code, committed); got != want {
		return fmt.Errorf("Decide(%d, %t) = %+v, want %+v", code, committed, got, want)
	}
	return nil
}

func checkStatusOf(err error, want int) error {
	if got := outcome.StatusOf(err); got != want {
		return fmt.Errorf("StatusOf(%v) = %d, want %d", err, got, want)
	}
	return nil
}

func checkString(k outcome.Kind, want string) error {
	if got := k.String(); got != want {
		return fmt.Errorf("Kind(%d).String() = %q, want %q", uint8(k), got, want)
	}
	return nil
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		f    outcome.Facts
		want outcome.Kind
	}{
		{"no facts", outcome.Facts{}, outcome.Unknown},
		{"an error of 500 after a 200 was written", outcome.Facts{ErrStatus: 500, Status: 200, Routed: true}, outcome.WorkerFailure},
		{"no error and a 500 written", outcome.Facts{Status: 500, Routed: true}, outcome.WorkerFailure},
		{"an error of 404 unrouted with a 200 written", outcome.Facts{ErrStatus: 404, Status: 200}, outcome.AdmissionRefusal},
		{"an error of 404 routed", outcome.Facts{ErrStatus: 404, Status: 404, Routed: true}, outcome.BusinessRefusal},
		{"gone beats a 500", outcome.Facts{ErrStatus: 500, Status: 500, Routed: true, Gone: true}, outcome.LostContact},
		{"gone with a 200 written", outcome.Facts{Status: 200, Routed: true, Gone: true}, outcome.LostContact},
		{"gone with no status", outcome.Facts{Gone: true}, outcome.LostContact},
		{"panicked beats gone", outcome.Facts{Status: 200, Routed: true, Panicked: true, Gone: true}, outcome.WorkerFailure},
		{"panicked with a 200 written", outcome.Facts{Status: 200, Routed: true, Panicked: true}, outcome.WorkerFailure},
		{"panicked with no status", outcome.Facts{Panicked: true}, outcome.WorkerFailure},
		{"the error's 400 beats a 500 on the wire", outcome.Facts{ErrStatus: 400, Status: 500, Routed: true}, outcome.BusinessRefusal},
		{"an error status below 100 is not replaced by the wire's", outcome.Facts{ErrStatus: 42, Status: 200, Routed: true}, outcome.Unknown},
		{"a negative status", outcome.Facts{Status: -1}, outcome.Unknown},
		{"status 99", outcome.Facts{Status: 99, Routed: true}, outcome.Unknown},
		{"status 100", outcome.Facts{Status: 100, Routed: true}, outcome.OK},
		{"status 200 unrouted", outcome.Facts{Status: 200}, outcome.OK},
		{"status 399", outcome.Facts{Status: 399, Routed: true}, outcome.OK},
		{"status 400 routed", outcome.Facts{Status: 400, Routed: true}, outcome.BusinessRefusal},
		{"status 400 unrouted", outcome.Facts{Status: 400}, outcome.AdmissionRefusal},
		{"status 499 routed", outcome.Facts{Status: 499, Routed: true}, outcome.BusinessRefusal},
		{"status 499 unrouted", outcome.Facts{Status: 499}, outcome.AdmissionRefusal},
		{"status 500 unrouted", outcome.Facts{Status: 500}, outcome.WorkerFailure},
		{"status 599", outcome.Facts{Status: 599, Routed: true}, outcome.WorkerFailure},
		{"an error of 599 unrouted", outcome.Facts{ErrStatus: 599, Status: 200}, outcome.WorkerFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkClassify(tc.f, tc.want); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestClassifyViolation(t *testing.T) {
	if checkClassify(outcome.Facts{Status: 404}, outcome.BusinessRefusal) == nil {
		t.Fatal("a 4xx with no handler attributed passed as a business refusal")
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name      string
		code      int
		committed bool
		want      outcome.Decision
	}{
		{"no stated status", 0, false, outcome.Decision{Status: 500, Write: true}},
		{"no stated status, committed", 0, true, outcome.Decision{Status: 500, Write: false}},
		{"a stated 404", 404, false, outcome.Decision{Status: 404, Write: true}},
		{"a stated 404, committed", 404, true, outcome.Decision{Status: 404, Write: false}},
		{"a stated 503", 503, false, outcome.Decision{Status: 503, Write: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkDecide(tc.code, tc.committed, tc.want); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestDecideViolation(t *testing.T) {
	if checkDecide(0, false, outcome.Decision{Status: 0, Write: true}) == nil {
		t.Fatal("a decision with status 0 passed for an error that states none")
	}
	if checkDecide(0, false, outcome.Decision{Status: 0, Write: false}) == nil {
		t.Fatal("a decision with status 0 and no write passed for an uncommitted response")
	}
}

func TestStatusOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"an error with no coder", errors.New("plain"), 0},
		{"a wrapped error with no coder", fmt.Errorf("outer: %w", errors.New("plain")), 0},
		{"a coder", coder{code: 404}, 404},
		{"a pointer to a coder", &coder{code: 409}, 409},
		{"a wrapped coder", fmt.Errorf("outer: %w", coder{code: 403}), 403},
		{"a coder wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", coder{code: 502})), 502},
		{"the first of two joined coders", errors.Join(coder{code: 400}, coder{code: 500}), 400},
		{"a coder joined after a plain error", errors.Join(errors.New("plain"), coder{code: 418}), 418},
		{"a coder that states 0", coder{}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkStatusOf(tc.err, tc.want); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestStatusOfViolation(t *testing.T) {
	if checkStatusOf(fmt.Errorf("outer: %w", coder{code: 404}), 0) == nil {
		t.Fatal("a wrapped coder's status passed as 0")
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		k    outcome.Kind
		want string
	}{
		{outcome.Unknown, "unknown"},
		{outcome.OK, "ok"},
		{outcome.AdmissionRefusal, "admission-refusal"},
		{outcome.BusinessRefusal, "business-refusal"},
		{outcome.WorkerFailure, "worker-failure"},
		{outcome.LostContact, "lost-contact"},
		{outcome.Kind(6), "unknown"},
		{outcome.Kind(255), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if err := checkString(tc.k, tc.want); err != nil {
				t.Error(err)
			}
		})
	}
	var zero outcome.Kind
	if zero != outcome.Unknown {
		t.Errorf("the zero Kind is %s, want unknown", zero)
	}
}

func TestKindStringViolation(t *testing.T) {
	if checkString(outcome.AdmissionRefusal, "business-refusal") == nil {
		t.Fatal("an admission refusal passed under a business refusal's name")
	}
}
