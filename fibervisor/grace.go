// SPDX-License-Identifier: MIT

package fibervisor

import (
	"strconv"
	"time"
)

// Grace returns the stop grace fv gives this attempt, from FV_GRACE_MS as Read found it, and true; it is false
// while fv does not set FV_GRACE_MS (fv sets it from train 2b on); a grace of 0 means fv kills immediately after
// the stop signal. While it is false the grace is unknown, not zero.
//
// FV_GRACE_MS is read as fv writes it: ASCII decimal digits, at least one, with no sign, space or unit, a number of
// milliseconds that a time.Duration holds. Any other text, such as -5, +5 or 4s, gives 0 and false, never a guess.
func (s Supervision) Grace() (time.Duration, bool) {
	return s.grace.d, s.grace.known
}

// stopGrace is the grace Read found: known is false when FV_GRACE_MS was unset or not fv's text, and then d is 0.
type stopGrace struct {
	d     time.Duration
	known bool
}

// graceOf returns the grace text names in milliseconds, unknown when text is not decimal digits or names more
// milliseconds than a time.Duration holds.
func graceOf(text string) stopGrace {
	if text == "" {
		return stopGrace{}
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return stopGrace{}
		}
	}
	ms, err := strconv.ParseInt(text, 10, 64)
	// A value a Duration cannot hold is not guessed; fv's MAX_GRACE (24h) is far below it.
	if err != nil || ms > int64(time.Duration(1<<63-1)/time.Millisecond) {
		return stopGrace{}
	}
	return stopGrace{d: time.Duration(ms) * time.Millisecond, known: true}
}
