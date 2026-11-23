// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"errors"
	"testing"
)

// TestClassify holds every row of the error mapping: the phase, the error and, where it is asked, whether the
// transaction is known to be rolled back.
func TestClassify(t *testing.T) {
	type tc struct {
		name   string
		ph     phase
		err    error
		rolled bool
		asked  bool // the case may ask whether the transaction is rolled back
		want   Outcome
		poison bool
		cause  error
	}
	f := func(code int) error { return &fakeErr{code} }
	cases := []tc{
		{"before: context done", beforeCommit, context.Canceled, true, true, Refused, false, nil},
		{"before: deadline", beforeCommit, context.DeadlineExceeded, true, true, Refused, false, nil},
		{"before: BUSY", beforeCommit, f(sqliteBusy), true, true, Refused, false, ErrBusy},
		{"before: LOCKED", beforeCommit, f(sqliteLocked), true, true, Refused, false, ErrBusy},
		{"before: CONSTRAINT", beforeCommit, f(sqliteConstraint | 5<<8), true, true, Refused, false, nil},
		{"before: TOOBIG", beforeCommit, f(sqliteTooBig), true, true, Refused, false, nil},
		{"before: FULL", beforeCommit, f(sqliteFull), true, true, Refused, false, nil},
		{"before: READONLY", beforeCommit, f(sqliteReadOnly), true, true, Refused, false, nil},
		{"before: IOERR", beforeCommit, f(sqliteIOErr | 4<<8), true, true, Refused, true, nil},
		{"before: CORRUPT", beforeCommit, f(sqliteCorrupt), true, true, Refused, true, nil},
		{"before: NOTADB", beforeCommit, f(sqliteNotADB), true, true, Refused, true, nil},
		{"before: a failed rollback", beforeCommit, f(sqliteConstraint), false, true, Refused, true, nil},
		{"before: not a SQLite error", beforeCommit, errors.New("x"), true, true, Refused, false, nil},
		{"commit: BUSY, rolled back", atCommit, f(sqliteBusy), true, true, Refused, false, ErrBusy},
		{"commit: LOCKED, rolled back", atCommit, f(sqliteLocked), true, true, Refused, false, ErrBusy},
		{"commit: BUSY, rollback failed", atCommit, f(sqliteBusy), false, true, Unknown, true, nil},
		{"commit: FULL, rolled back", atCommit, f(sqliteFull), true, true, Refused, false, nil},
		{"commit: FULL, transaction left open", atCommit, f(sqliteFull), false, true, Unknown, true, nil},
		{"commit: IOERR_FSYNC", atCommit, f(codeIOFsync), true, false, Unknown, true, nil},
		{"commit: IOERR", atCommit, f(sqliteIOErr), true, false, Unknown, true, nil},
		{"commit: NOMEM", atCommit, f(sqliteNoMem), true, false, Unknown, true, nil},
		{"commit: INTERRUPT", atCommit, f(sqliteInterrupt), true, false, Unknown, true, nil},
		{"commit: CORRUPT", atCommit, f(sqliteCorrupt), true, false, Unknown, true, nil},
		{"commit: not a SQLite error", atCommit, errors.New("connection lost"), true, false, Unknown, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked := false
			v := classify(c.ph, c.err, func() bool { asked = true; return c.rolled })
			if asked && !c.asked {
				t.Error("asked whether the transaction is rolled back")
			}
			if v.outcome != c.want || v.poison != c.poison {
				t.Fatalf("outcome %v poison %v, want %v %v", v.outcome, v.poison, c.want, c.poison)
			}
			if c.cause != nil && !errors.Is(v.cause, c.cause) {
				t.Fatalf("cause %v does not match %v", v.cause, c.cause)
			}
			if !errors.Is(v.cause, c.err) {
				t.Fatalf("cause %v does not carry the error %v", v.cause, c.err)
			}
		})
	}
}

// TestOutcomeErrors: the error is nil only for Committed, and matches the outcome's sentinel otherwise.
func TestOutcomeErrors(t *testing.T) {
	cause := errors.New("cause")
	for _, tc := range []struct {
		o    Outcome
		want error
	}{{Conflict, ErrConflict}, {Refused, ErrRefused}, {Unknown, ErrUnknown}} {
		err := outcomeError(tc.o, cause)
		if !errors.Is(err, tc.want) || !errors.Is(err, cause) {
			t.Fatalf("%v: %v", tc.o, err)
		}
		for _, other := range []error{ErrConflict, ErrRefused, ErrUnknown} {
			if other != tc.want && errors.Is(err, other) {
				t.Fatalf("%v also matches %v", tc.o, other)
			}
		}
	}
	if outcomeError(Committed, nil) != nil {
		t.Fatal("Committed has an error")
	}
	for o, name := range map[Outcome]string{Committed: "committed", Conflict: "conflict", Refused: "refused", Unknown: "unknown"} {
		if o.String() != name {
			t.Fatalf("%d is %q", o, o.String())
		}
	}
}
