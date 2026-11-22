package mxdb

import (
	"errors"
	"fmt"
)

// Sentinels for errors.Is. Each outcome of an update has its own.
var (
	// ErrAborted: a precondition failed and nothing was committed.
	ErrAborted = errors.New("mxdb: update aborted: a precondition failed, nothing committed")
	// ErrRejected: the server refused the update; nothing was committed.
	ErrRejected = errors.New("mxdb: update rejected")
	// ErrOutcomeUnknown: the update may or may not be committed; read back before retrying.
	ErrOutcomeUnknown = errors.New("mxdb: outcome unknown: read back before retrying")
	// ErrNotSent: the request provably never reached the server; it is safe to retry.
	ErrNotSent = errors.New("mxdb: request not sent")
	// ErrProtocol: the server answered ProtocolError (status 4: it could not parse the request), or the peer
	// sent a frame this client cannot parse. An update the server refused so is also ErrRejected.
	ErrProtocol = errors.New("mxdb: protocol error")

	ErrNoOps     = errors.New("mxdb: update has no operations")
	ErrEmptyName = errors.New("mxdb: empty database name or key")
	ErrTooLarge  = errors.New("mxdb: request exceeds the maximum frame")
	ErrClosed    = errors.New("mxdb: client closed")
)

// AbortedError is the outcome Aborted: precondition FailedPre (an index into
// Update.Pre) did not hold.
type AbortedError struct{ FailedPre int }

func (e *AbortedError) Error() string {
	return fmt.Sprintf("%v (precondition %d)", ErrAborted, e.FailedPre)
}
func (e *AbortedError) Is(target error) bool { return target == ErrAborted }

// RejectCode says why the server rejected a request; the wire value is the
// constant's number.
type RejectCode uint8

const (
	KeyTooLong RejectCode = iota
	ValueTooLong
	TransactionTooLarge
	Poisoned
	Io
	LockLost
	NoSuchCommit
	DamagedTail
	Other
)

var rejectNames = [...]string{"KeyTooLong", "ValueTooLong", "TransactionTooLarge", "Poisoned", "Io", "LockLost", "NoSuchCommit", "DamagedTail", "Other"}

func (c RejectCode) String() string {
	if int(c) < len(rejectNames) {
		return rejectNames[c]
	}
	return fmt.Sprintf("RejectCode(%d)", uint8(c))
}

// RejectedError is the outcome Rejected; nothing was committed.
type RejectedError struct {
	Code   RejectCode
	Reason string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("%v: %v: %s", ErrRejected, e.Code, e.Reason)
}
func (e *RejectedError) Is(target error) bool { return target == ErrRejected }

// ProtocolError is the server's status 4: it could not parse the request. As
// an update outcome it is Rejected (nothing was committed) and matches both
// ErrProtocol and ErrRejected. The client closes the connection it came on.
type ProtocolError struct{ Reason string }

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("%v: server refused the request: %s", ErrProtocol, e.Reason)
}
func (e *ProtocolError) Is(target error) bool { return target == ErrProtocol || target == ErrRejected }

// UnknownError is the outcome OutcomeUnknown; Cause says why it is unknown.
type UnknownError struct{ Cause error }

func (e *UnknownError) Error() string {
	return fmt.Sprintf("%v: %v", ErrOutcomeUnknown, e.Cause)
}
func (e *UnknownError) Is(target error) bool { return target == ErrOutcomeUnknown }
func (e *UnknownError) Unwrap() error        { return e.Cause }

// NotSentError means the request never left the client.
type NotSentError struct{ Err error }

func (e *NotSentError) Error() string {
	return fmt.Sprintf("%v: %v", ErrNotSent, e.Err)
}
func (e *NotSentError) Is(target error) bool { return target == ErrNotSent }
func (e *NotSentError) Unwrap() error        { return e.Err }

// unknownErr is the one place a transport failure after a send, a malformed
// reply or a server-reported unknown becomes the OutcomeUnknown error.
func unknownErr(cause error) error {
	return &UnknownError{Cause: cause}
}
