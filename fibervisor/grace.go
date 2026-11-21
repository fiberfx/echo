// SPDX-License-Identifier: MIT

package fibervisor

import (
	"strconv"
	"time"
)

// Grace returns the stop grace fv gives this attempt, from FV_GRACE_MS as Read found it, and true; it is false
// while fv does not set that variable, which fv does not today: the grace is then unknown, not zero.
//
// FV_GRACE_MS is read as fv would write it, a positive number of milliseconds in decimal digits with no sign and
// no leading zero. Any other text, such as 0, -5 or 4s, gives 0 and false, never a guess.
func (s Supervision) Grace() (time.Duration, bool) {
	return s.grace, s.grace > 0
}

// graceOf returns the grace text names in milliseconds, or 0 when text is not a positive decimal number of
// milliseconds that a time.Duration holds.
func graceOf(text string) time.Duration {
	if text == "" || text[0] == '0' {
		return 0
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0
		}
	}
	ms, err := strconv.ParseInt(text, 10, 64)
	if err != nil || ms > int64(time.Duration(1<<63-1)/time.Millisecond) {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
