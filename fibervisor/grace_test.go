// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v6/fibervisor"
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

// checkGrace returns an error unless d and ok are what Grace owes: want and true when want is positive, else 0 and
// false, the grace unknown rather than guessed.
func checkGrace(want, d time.Duration, ok bool) error {
	known := want > 0
	if ok != known || (known && d != want) || (!known && d != 0) {
		return fmt.Errorf("Grace() = %v, %v, want %v, %v", d, ok, want, known)
	}
	return nil
}

func TestGrace(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		want  time.Duration // 0 when the grace is unknown
	}{
		{"fv's default grace", "500", true, 500 * time.Millisecond},
		{"the row's grace", "4000", true, 4 * time.Second},
		{"one millisecond", "1", true, time.Millisecond},

		{"unset, as fv leaves it today", "", false, 0},
		{"set and empty", "", true, 0},
		{"zero", "0", true, 0},
		{"negative", "-5", true, 0},
		{"a sign", "+5", true, 0},
		{"a duration's text", "4s", true, 0},
		{"a leading zero", "0400", true, 0},
		{"a space", " 400", true, 0},
		{"a fraction", "1.5", true, 0},
		{"more than a Duration holds", "9223372036855", true, 0},
		{"more than an int64", "99999999999999999999", true, 0},
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
			if err := checkGrace(tt.want, d, ok); err != nil {
				t.Errorf("%v, with FV_GRACE_MS=%q set=%v", err, tt.value, tt.set)
			}
		})
	}
}

// TestGraceLargest holds the largest grace a Duration holds to true, so the bound is not off by one.
func TestGraceLargest(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	setGrace(t, "9223372036854", true)
	s, _ := fibervisor.Read()
	d, ok := s.Grace()
	if err := checkGrace(9223372036854*time.Millisecond, d, ok); err != nil {
		t.Error(err)
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
	if err := checkGrace(0, d, ok); err != nil {
		t.Errorf("%v: a grace from a process fv does not hold", err)
	}
	d, ok = fvOwn.Grace()
	if err := checkGrace(0, d, ok); err != nil {
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
	if err := checkGrace(4*time.Second, d, ok); err != nil {
		t.Error(err)
	}
}

func TestGraceViolation(t *testing.T) {
	for _, bad := range []struct {
		name string
		want time.Duration
		d    time.Duration
		ok   bool
	}{
		{"fv's default guessed for an unset grace", 0, 500 * time.Millisecond, true},
		{"an unknown grace claimed as zero", 0, 0, true},
		{"a value returned with false", 0, 4 * time.Second, false},
		{"seconds read for milliseconds", 4 * time.Second, 4000 * time.Second, true},
		{"a known grace reported unknown", 4 * time.Second, 0, false},
	} {
		if checkGrace(bad.want, bad.d, bad.ok) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}
