// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The primary SQLite result codes this package acts on (the low byte of an extended code).
const (
	sqliteError      = 1
	sqliteBusy       = 5
	sqliteLocked     = 6
	sqliteNoMem      = 7
	sqliteReadOnly   = 8
	sqliteInterrupt  = 9
	sqliteIOErr      = 10
	sqliteCorrupt    = 11
	sqliteFull       = 13
	sqliteConstraint = 19
	sqliteTooBig     = 18
	sqliteNotADB     = 26
)

// phase is where in a write an error arose.
type phase int

const (
	beforeCommit phase = iota
	atCommit
)

// verdict is the classification of an error of a write.
type verdict struct {
	outcome Outcome
	poison  bool
	cause   error
}

// sqliteCode returns the primary result code of err, or 0 when err is not a SQLite error.
func sqliteCode(err error) int {
	var c interface{ Code() int }
	if errors.As(err, &c) {
		return c.Code() & 0xff
	}
	return 0
}

// noTransaction reports whether err says that no transaction was active, which a ROLLBACK answers when SQLite had
// already rolled the transaction back.
func noTransaction(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no transaction is active")
}

// classify maps an error of a write to its outcome. rolledBack is asked only where the answer depends on whether the
// transaction is gone: BUSY, LOCKED and FULL at COMMIT, and any error before COMMIT. It reports whether the
// connection is back in autocommit with the transaction rolled back.
//
// Before COMMIT nothing can have been committed, so the outcome is Refused; the handle is poisoned when the error is
// an I/O or corruption error, or the rollback did not succeed. At COMMIT the outcome is Refused only when the
// transaction is known to be rolled back (BUSY, LOCKED, FULL); every other error, and a failed rollback, is Unknown
// and poisons the handle.
func classify(ph phase, err error, rolledBack func() bool) verdict {
	code := sqliteCode(err)
	if ph == beforeCommit {
		v := verdict{outcome: Refused, cause: err}
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		case code == sqliteBusy, code == sqliteLocked:
			v.cause = fmt.Errorf("%w: %w", ErrBusy, err)
		case code == sqliteConstraint, code == sqliteTooBig, code == sqliteFull, code == sqliteReadOnly:
		case code == sqliteIOErr, code == sqliteCorrupt, code == sqliteNotADB:
			v.poison = true
		}
		if rolledBack != nil && !rolledBack() {
			v.poison = true
		}
		return v
	}
	switch code {
	case sqliteBusy, sqliteLocked:
		if rolledBack() {
			return verdict{outcome: Refused, cause: fmt.Errorf("%w: %w", ErrBusy, err)}
		}
	case sqliteFull:
		if rolledBack() {
			return verdict{outcome: Refused, cause: err}
		}
	}
	return verdict{outcome: Unknown, poison: true, cause: err}
}

// outcomeError wraps the cause of a non-committed outcome so that errors.Is matches both.
func outcomeError(o Outcome, cause error) error {
	var kind error
	switch o {
	case Conflict:
		kind = ErrConflict
	case Refused:
		kind = ErrRefused
	case Unknown:
		kind = ErrUnknown
	default:
		return cause
	}
	if cause == nil {
		return kind
	}
	return fmt.Errorf("%w: %w", kind, cause)
}
