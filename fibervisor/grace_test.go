// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7/fibervisor"
)

// setGrace puts FV_GRACE_MS in the environment for the rest of the test, or unsets it when set is false.
func setGrace(t *testing.T, value string, set bool) {
	t.Helper()
	t.Setenv("FV_GRACE_MS", value)
	if !set {
		if err := os.Unsetenv("FV_GRACE_MS"); err != nil {
			t.Fatal(err)
		}
	}
}

// checkGrace returns an error unless d and ok are what Grace owes: want and true when the grace is known, 0 among
// them, else 0 and false, the grace unknown rather than guessed.
func checkGrace(want time.Duration, known bool, d time.Duration, ok bool) error {
	if !known {
		want = 0
	}
	if ok != known || d != want {
		return fmt.Errorf("Grace() = %v, %v, want %v, %v", d, ok, want, known)
	}
	return nil
}

func TestGrace(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
		known bool
	}{
		{"fv's default grace", "500", true, 500 * time.Millisecond, true},
		{"the row's grace", "4000", true, 4 * time.Second, true},
		{"one millisecond", "1", true, time.Millisecond, true},
		{"zero: fv kills right after the stop signal", "0", true, 0, true},
		{"a leading zero, still decimal digits", "0400", true, 400 * time.Millisecond, true},
		{"the largest a Duration holds", "9223372036854", true, 9223372036854 * time.Millisecond, true},

		{"unset, as fv leaves it before train 2b", "", false, 0, false},
		{"set and empty", "", true, 0, false},
		{"negative", "-5", true, 0, false},
		{"a sign", "+5", true, 0, false},
		{"a duration's text", "4s", true, 0, false},
		{"a leading space", " 4000", true, 0, false},
		{"a trailing newline", "4000\n", true, 0, false},
		{"a fraction", "1.5", true, 0, false},
		{"more than a Duration holds", "9223372036855", true, 0, false},
		{"more than an int64", "99999999999999999999", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaf(fvTrace, fvAttempt).set(t)
			setGrace(t, tt.value, tt.set)
			s, ok := fibervisor.Read()
			if !ok {
				t.Fatalf("Read() = %+v, false under fv's leaf environment", s)
			}
			d, ok := s.Grace()
			if err := checkGrace(tt.want, tt.known, d, ok); err != nil {
				t.Errorf("%v, with FV_GRACE_MS=%q set=%v", err, tt.value, tt.set)
			}
		})
	}
}

func TestGraceNotSupervised(t *testing.T) {
	env{}.set(t)
	setGrace(t, "4000", true)
	s, ok := fibervisor.Read()
	if ok {
		t.Fatalf("Read() = %+v, true with no fv attempt", s)
	}
	d, ok := s.Grace()
	if err := checkGrace(0, false, d, ok); err != nil {
		t.Errorf("%v: a grace from a process fv does not hold", err)
	}
	d, ok = fvOwn.Grace()
	if err := checkGrace(0, false, d, ok); err != nil {
		t.Errorf("%v: a hand-built Supervision claims a grace", err)
	}
}

// TestGraceReadOnce holds the grace to the value Read found: a later change of the environment does not reach it.
func TestGraceReadOnce(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	setGrace(t, "4000", true)
	s, _ := fibervisor.Read()
	setGrace(t, "", false)
	d, ok := s.Grace()
	if err := checkGrace(4*time.Second, true, d, ok); err != nil {
		t.Error(err)
	}
}

func TestGraceViolation(t *testing.T) {
	for _, bad := range []struct {
		name  string
		want  time.Duration
		known bool
		d     time.Duration
		ok    bool
	}{
		{"-5 read as a known grace", 0, false, -5 * time.Millisecond, true},
		{"-5 read as a known zero", 0, false, 0, true},
		{"fv's default guessed for an unset grace", 0, false, 500 * time.Millisecond, true},
		{"a value returned with false", 0, false, 4 * time.Second, false},
		{"a known zero reported unknown", 0, true, 0, false},
		{"a known zero read as fv's default", 0, true, 500 * time.Millisecond, true},
		{"seconds read for milliseconds", 4 * time.Second, true, 4000 * time.Second, true},
		{"a known grace reported unknown", 4 * time.Second, true, 0, false},
	} {
		if checkGrace(bad.want, bad.known, bad.d, bad.ok) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}
